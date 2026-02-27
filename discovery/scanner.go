package discovery

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Server struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	Online   bool   `json:"online"`
	MAC      string `json:"mac,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// BareMetalHost represents a BMH object from the mkube API
type BareMetalHost struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		BMC struct {
			Address  string `json:"address"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"bmc"`
		BootMACAddress string `json:"bootMACAddress"`
	} `json:"spec"`
	Status struct {
		Phase    string `json:"phase"`
		PowerOn  bool   `json:"poweredOn"`
		IP       string `json:"ip"`
	} `json:"status"`
}

type BareMetalHostList struct {
	Items []BareMetalHost `json:"items"`
}

type WatchEvent struct {
	Type   string        `json:"type"`
	Object BareMetalHost `json:"object"`
}

type Scanner struct {
	servers    map[string]*Server
	mu         sync.RWMutex
	onChange   func(servers map[string]*Server)
	bmhURL     string
	namespace  string
	httpClient *http.Client
	cache      *Cache
}

func NewScanner(bmhURL, namespace, dataDir string) *Scanner {
	return &Scanner{
		servers:    make(map[string]*Server),
		bmhURL:     bmhURL,
		namespace:  namespace,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		cache:      NewCache(dataDir),
	}
}

// BMHListURL returns the URL for listing BMH objects, scoped by namespace if configured.
func (s *Scanner) BMHListURL() string {
	if s.namespace != "" {
		return s.bmhURL + "/api/v1/namespaces/" + s.namespace + "/baremetalhosts"
	}
	return s.bmhURL + "/api/v1/baremetalhosts"
}

func (s *Scanner) AddServer(name, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *Scanner) BMHURL() string {
	return s.bmhURL
}

func (s *Scanner) Refresh() {
	s.fetchBMH()
}

func (s *Scanner) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Scanner goroutine panicked: %v", r)
		}
	}()

	// Load from cache first for immediate availability
	if cached := s.cache.Load(); len(cached) > 0 {
		s.mu.Lock()
		for name, srv := range cached {
			if _, exists := s.servers[name]; !exists {
				s.servers[name] = srv
				log.Infof("Cache loaded: %s (ip=%s)", name, srv.IP)
			}
		}
		s.mu.Unlock()
		log.Infof("Loaded %d servers from cache, calling onChange", len(cached))
		if s.onChange != nil {
			s.onChange(s.GetServers())
		}
	} else {
		log.Info("No BMH cache found or cache empty")
	}

	// Fetch live data (updates cache)
	s.fetchBMH()

	s.mu.RLock()
	serverCount := len(s.servers)
	s.mu.RUnlock()
	log.Infof("After fetchBMH: %d servers in map", serverCount)

	if s.onChange != nil {
		s.onChange(s.GetServers())
	}

	// Watch with reconnect loop
	for {
		select {
		case <-ctx.Done():
			return
		default:
			s.watchBMH(ctx)
			// Watch disconnected, wait before reconnecting
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				log.Info("Reconnecting BMH watch...")
				s.fetchBMH()
			}
		}
	}
}

func (s *Scanner) fetchBMH() {
	if s.bmhURL == "" {
		log.Warn("fetchBMH: bmhURL is empty, skipping")
		return
	}

	url := s.BMHListURL()
	log.Infof("fetchBMH: fetching %s", url)

	resp, err := s.httpClient.Get(url)
	if err != nil {
		log.Warnf("fetchBMH: HTTP request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Warnf("fetchBMH: unexpected status %d", resp.StatusCode)
		return
	}

	var list BareMetalHostList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Warnf("fetchBMH: JSON decode failed: %v", err)
		return
	}

	log.Infof("fetchBMH: decoded %d BMH items", len(list.Items))

	hasNew := false
	s.mu.Lock()
	for _, bmh := range list.Items {
		if s.applyBMH(bmh) {
			hasNew = true
		}
	}
	s.mu.Unlock()

	if hasNew {
		s.cache.Save(s.GetServers())
		if s.onChange != nil {
			go s.onChange(s.GetServers())
		}
	}
}

func (s *Scanner) watchBMH(ctx context.Context) {
	if s.bmhURL == "" {
		return
	}

	req, err := http.NewRequestWithContext(ctx, "GET", s.BMHListURL()+"?watch=true", nil)
	if err != nil {
		log.Warnf("Failed to create BMH watch request: %v", err)
		return
	}

	// Use a client without timeout for the long-lived watch connection
	watchClient := &http.Client{}
	resp, err := watchClient.Do(req)
	if err != nil {
		log.Warnf("BMH watch failed: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Info("BMH watch connected")

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event WatchEvent
		if err := json.Unmarshal(line, &event); err != nil {
			log.Warnf("Failed to decode watch event: %v", err)
			continue
		}

		changed := false
		s.mu.Lock()
		switch event.Type {
		case "ADDED", "MODIFIED":
			changed = s.applyBMH(event.Object)
		case "DELETED":
			// Ignore DELETE events — BMH objects represent physical hardware.
			// Watch DELETE events are often spurious (namespace scoping issues,
			// object recreation). Rely on fetchBMH list for authoritative state.
			log.Debugf("BMH watch DELETE ignored for %s", event.Object.Metadata.Name)
		}
		s.mu.Unlock()

		if changed {
			s.cache.Save(s.GetServers())
			if s.onChange != nil {
				go s.onChange(s.GetServers())
			}
		}
	}
}

// applyBMH updates the server map from a BMH object. Must be called with s.mu held.
// Returns true if there was a change.
func (s *Scanner) applyBMH(bmh BareMetalHost) bool {
	addr := bmh.Spec.BMC.Address
	if addr == "" {
		return false
	}

	name := bmh.Metadata.Name

	existing, exists := s.servers[name]
	if exists {
		changed := false
		if existing.IP != addr {
			existing.IP = addr
			changed = true
		}
		// BMC is always reachable regardless of host power state
		if !existing.Online {
			existing.Online = true
			changed = true
		}
		if bmh.Spec.BootMACAddress != "" && existing.MAC != bmh.Spec.BootMACAddress {
			existing.MAC = bmh.Spec.BootMACAddress
			changed = true
		}
		if bmh.Spec.BMC.Username != "" && existing.Username != bmh.Spec.BMC.Username {
			existing.Username = bmh.Spec.BMC.Username
			changed = true
		}
		if bmh.Spec.BMC.Password != "" && existing.Password != bmh.Spec.BMC.Password {
			existing.Password = bmh.Spec.BMC.Password
			changed = true
		}
		return changed
	}

	s.servers[name] = &Server{
		IP:       addr,
		Hostname: name,
		Online:   true,
		MAC:      bmh.Spec.BootMACAddress,
		Username: bmh.Spec.BMC.Username,
		Password: bmh.Spec.BMC.Password,
	}
	log.Infof("Discovered BMH: %s (%s)", name, addr)
	return true
}
