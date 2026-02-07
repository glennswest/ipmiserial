package discovery

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Server struct {
	IP       string
	Hostname string
	Online   bool
	MAC      string
}

// NetmanHost represents a host from the netman API
type NetmanHost struct {
	ID        int64  `json:"id"`
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname"`
	DNSName   string `json:"dns_name"`
	MAC       string `json:"mac_address"`
	IsOnline  bool   `json:"is_online"`
}

type Scanner struct {
	servers    map[string]*Server
	mu         sync.RWMutex
	onChange   func(servers map[string]*Server)
	netmanURL  string
	ipRangeMin int
	ipRangeMax int
	httpClient *http.Client
}

func NewScanner(netmanURL string, ipRangeMin, ipRangeMax int) *Scanner {
	return &Scanner{
		servers:    make(map[string]*Server),
		netmanURL:  netmanURL,
		ipRangeMin: ipRangeMin,
		ipRangeMax: ipRangeMax,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Scanner) AddServer(name, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Resolve hostname to IP
	ip := host
	if addrs, err := net.LookupHost(host); err == nil && len(addrs) > 0 {
		ip = addrs[0]
	}

	s.servers[name] = &Server{
		IP:       ip,
		Hostname: name,
		Online:   true,
	}

	log.Infof("Added server: %s (%s -> %s)", name, host, ip)
}

func (s *Scanner) OnChange(fn func(servers map[string]*Server)) {
	s.onChange = fn
}

func (s *Scanner) GetServers() map[string]*Server {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*Server)
	for k, v := range s.servers {
		result[k] = v
	}
	return result
}

// Refresh triggers an immediate fetch from netman
func (s *Scanner) Refresh() {
	s.fetchFromNetman()
}

func (s *Scanner) Run(ctx context.Context) {
	// Initial fetch from netman
	s.fetchFromNetman()

	// Trigger initial onChange
	if s.onChange != nil {
		s.onChange(s.GetServers())
	}

	// Periodic refresh from netman
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fetchFromNetman()
		}
	}
}

func (s *Scanner) fetchFromNetman() {
	if s.netmanURL == "" {
		return
	}

	// Only fetch hosts from the g11 network (IPMI network)
	resp, err := s.httpClient.Get(s.netmanURL + "/api/hosts?network=192.168.11.0/24")
	if err != nil {
		log.Warnf("Failed to fetch hosts from netman: %v", err)
		return
	}
	defer resp.Body.Close()

	var hosts []NetmanHost
	if err := json.NewDecoder(resp.Body).Decode(&hosts); err != nil {
		log.Warnf("Failed to decode netman response: %v", err)
		return
	}

	s.mu.Lock()

	// Build a set of IPs already known (from config or prior discovery)
	knownIPs := make(map[string]string) // IP -> name
	for name, srv := range s.servers {
		knownIPs[srv.IP] = name
	}

	// Track which servers we found
	hasNewServers := false

	for _, h := range hosts {
		// Check if IP is in our range (192.168.11.10-199)
		if !s.isInRange(h.IPAddress) {
			continue
		}

		// Determine server name: prefer hostname, then dns_name, fall back to IP
		name := h.Hostname
		if name == "" && h.DNSName != "" {
			name = h.DNSName
		}
		if name == "" {
			name = h.IPAddress
		}
		// Clean up the name - remove domain suffix if present
		// But don't truncate if it's an IP address
		if idx := strings.Index(name, "."); idx > 0 && !isIPAddress(name) {
			name = name[:idx]
		}

		// If this IP is already known under a different name, update that entry instead
		if existingName, exists := knownIPs[h.IPAddress]; exists && existingName != name {
			existing := s.servers[existingName]
			existing.Online = h.IsOnline
			if h.MAC != "" {
				existing.MAC = h.MAC
			}
			continue
		}

		// Add or update server
		if existing, exists := s.servers[name]; exists {
			// Update existing
			existing.Online = h.IsOnline
			if h.MAC != "" {
				existing.MAC = h.MAC
			}
		} else {
			// New server
			s.servers[name] = &Server{
				IP:       h.IPAddress,
				Hostname: name,
				Online:   h.IsOnline,
				MAC:      h.MAC,
			}
			knownIPs[h.IPAddress] = name
			log.Infof("Discovered server from netman: %s (%s)", name, h.IPAddress)
			hasNewServers = true
		}
	}

	s.mu.Unlock()

	// Trigger onChange for any new servers (after releasing the lock)
	if hasNewServers && s.onChange != nil {
		go s.onChange(s.GetServers())
	}
}

func isIPAddress(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

func (s *Scanner) isInRange(ip string) bool {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}

	// Check if it's 192.168.11.x
	if parts[0] != "192" || parts[1] != "168" || parts[2] != "11" {
		return false
	}

	// Check if last octet is in range
	lastOctet, err := strconv.Atoi(parts[3])
	if err != nil {
		return false
	}

	return lastOctet >= s.ipRangeMin && lastOctet <= s.ipRangeMax
}
