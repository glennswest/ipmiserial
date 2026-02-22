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
	IP       string
	Hostname string
	Online   bool
	MAC      string
	Username string
	Password string
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
	httpClient *http.Client
}

func NewScanner(bmhURL string) *Scanner {
	return &Scanner{
		servers:    make(map[string]*Server),
		bmhURL:     bmhURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
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
	// Initial list
	s.fetchBMH()

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
		return
	}

	resp, err := s.httpClient.Get(s.bmhURL + "/api/v1/baremetalhosts")
	if err != nil {
		log.Warnf("Failed to fetch BMH list: %v", err)
		return
	}
	defer resp.Body.Close()

	var list BareMetalHostList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Warnf("Failed to decode BMH response: %v", err)
		return
	}

	hasNew := false
	s.mu.Lock()
	for _, bmh := range list.Items {
		if s.applyBMH(bmh) {
			hasNew = true
		}
	}
	s.mu.Unlock()

	if hasNew && s.onChange != nil {
		go s.onChange(s.GetServers())
	}
}

func (s *Scanner) watchBMH(ctx context.Context) {
	if s.bmhURL == "" {
		return
	}

	req, err := http.NewRequestWithContext(ctx, "GET", s.bmhURL+"/api/v1/baremetalhosts?watch=true", nil)
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
			name := event.Object.Metadata.Name
			if _, exists := s.servers[name]; exists {
				delete(s.servers, name)
				log.Infof("BMH removed: %s", name)
				changed = true
			}
		}
		s.mu.Unlock()

		if changed && s.onChange != nil {
			go s.onChange(s.GetServers())
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
		if existing.Online != bmh.Status.PowerOn {
			existing.Online = bmh.Status.PowerOn
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
		Online:   bmh.Status.PowerOn,
		MAC:      bmh.Spec.BootMACAddress,
		Username: bmh.Spec.BMC.Username,
		Password: bmh.Spec.BMC.Password,
	}
	log.Infof("Discovered BMH: %s (%s)", name, addr)
	return true
}
