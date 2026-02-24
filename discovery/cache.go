package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	log "github.com/sirupsen/logrus"
)

// Cache persists discovered BMH servers to disk so they're available
// immediately on startup before the BMH API is reachable.
type Cache struct {
	path string
	mu   sync.Mutex
}

func NewCache(dataDir string) *Cache {
	return &Cache{
		path: filepath.Join(dataDir, "bmh-cache.json"),
	}
}

// Load reads cached servers from disk. Returns nil map if no cache exists.
func (c *Cache) Load() map[string]*Server {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warnf("Failed to read BMH cache: %v", err)
		}
		return nil
	}

	var servers map[string]*Server
	if err := json.Unmarshal(data, &servers); err != nil {
		log.Warnf("Failed to parse BMH cache: %v", err)
		return nil
	}

	log.Infof("Loaded %d servers from BMH cache", len(servers))
	return servers
}

// Save writes the current server map to disk atomically.
func (c *Cache) Save(servers map[string]*Server) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		log.Warnf("Failed to marshal BMH cache: %v", err)
		return
	}

	// Atomic write: tmp file + rename
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Warnf("Failed to create cache dir: %v", err)
		return
	}

	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Warnf("Failed to write BMH cache tmp: %v", err)
		return
	}

	if err := os.Rename(tmp, c.path); err != nil {
		log.Warnf("Failed to rename BMH cache: %v", err)
		os.Remove(tmp)
		return
	}

	log.Debugf("Saved %d servers to BMH cache", len(servers))
}
