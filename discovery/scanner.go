package discovery

import (
	"context"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Server struct {
	IP       string
	Hostname string
	Online   bool
}

type Scanner struct {
	servers  map[string]*Server
	mu       sync.RWMutex
	onChange func(servers map[string]*Server)
}

func NewScanner() *Scanner {
	return &Scanner{
		servers: make(map[string]*Server),
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

func (s *Scanner) Run(ctx context.Context) {
	// Trigger initial onChange
	if s.onChange != nil {
		s.onChange(s.GetServers())
	}

	// Periodic health check
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkHealth()
		}
	}
}

func (s *Scanner) checkHealth() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, srv := range s.servers {
		// Try UDP ping to IPMI port 623
		conn, err := net.DialTimeout("udp", srv.IP+":623", 2*time.Second)
		if err != nil {
			if srv.Online {
				log.Warnf("Server %s (%s) is offline", name, srv.IP)
				srv.Online = false
			}
			continue
		}
		conn.Close()

		if !srv.Online {
			log.Infof("Server %s (%s) is back online", name, srv.IP)
			srv.Online = true
		}
	}
}
