package sol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type NetworkEvent struct {
	Interface string    `json:"interface"`
	Event     string    `json:"event"` // "up" or "down"
	Time      time.Time `json:"time"`
}

type NetworkStats struct {
	Interface string `json:"interface"`
	UpCount   int    `json:"upCount"`
	DownCount int    `json:"downCount"`
}

type BootEvent struct {
	StartTime     time.Time      `json:"startTime"`
	EndTime       time.Time      `json:"endTime,omitempty"`
	BootDuration  float64        `json:"bootDuration,omitempty"`    // seconds
	PowerOnDelay  float64        `json:"powerOnDelay,omitempty"`    // seconds from rotation to first console output
	RotationTime  *time.Time     `json:"rotationTime,omitempty"`   // when log rotation triggered this boot
	Complete      bool           `json:"complete"`
	DetectedOS    string         `json:"detectedOS,omitempty"`
	NetworkEvents []NetworkEvent `json:"networkEvents,omitempty"`
	NetworkStats  []NetworkStats `json:"networkStats,omitempty"`
}

type ServerAnalytics struct {
	ServerName    string       `json:"serverName"`
	CurrentBoot   *BootEvent   `json:"currentBoot,omitempty"`
	BootHistory   []BootEvent  `json:"bootHistory"`
	LastSeen      time.Time    `json:"lastSeen"`
	OSUpSince     *time.Time   `json:"osUpSince,omitempty"`
	TotalReboots  int          `json:"totalReboots"`
	CurrentOS     string       `json:"currentOS,omitempty"`
	Hostname      string       `json:"hostname,omitempty"`

	// Unexported: pending rotation tracking
	pendingRotation *time.Time `json:"-"`
	rotationDelay   float64    `json:"-"` // computed when first console output arrives
	rotationTime    *time.Time `json:"-"` // carried until BIOS creates new boot event
}

type osDetector struct {
	name    string
	pattern *regexp.Regexp
}

type Analytics struct {
	servers        map[string]*ServerAnalytics
	biosPatterns   []*regexp.Regexp
	osPatterns     []*regexp.Regexp
	osDetectors    []osDetector
	hostPattern    *regexp.Regexp
	netUpPattern   *regexp.Regexp
	netDownPattern *regexp.Regexp
	dataPath       string
	mu             sync.RWMutex
}

func NewAnalytics(dataPath string) *Analytics {
	a := &Analytics{
		servers:      make(map[string]*ServerAnalytics),
		biosPatterns: make([]*regexp.Regexp, 0),
		osPatterns:   make([]*regexp.Regexp, 0),
		dataPath:     dataPath,
	}

	// Load existing data
	a.load()

	// BIOS boot start patterns
	biosPatterns := []string{
		`American Megatrends`,
		`Press <DEL> to run Setup`,
		`Press DEL to run Setup`,
		`BIOS Date:`,
		`Supermicro`,
		`Version \d+\.\d+\.\d+.*Copyright`,
		`Intel\(R\) Boot Agent`,
		`CLIENT MAC ADDR:`,
		`PXE-`,
		`PXE->`,
		`iPXE initialising`,
		`iPXE \d+\.\d+`,
		`Open Source Network Boot Firmware`,
		`Booting baremetalservices`,
		`UNDI code segment`,
		`free base memory after PXE`,
	}

	// OS up patterns - indicates boot complete
	osPatterns := []string{
		`login:`,
		`Welcome to`,
		`Started .*Service`,
		`Reached target`,
		`systemd.*Startup finished`,
		`Bare Metal Services Ready`,
		`SSH:.*port 22`,
	}

	for _, p := range biosPatterns {
		if re, err := regexp.Compile("(?i)" + p); err == nil {
			a.biosPatterns = append(a.biosPatterns, re)
		}
	}

	for _, p := range osPatterns {
		if re, err := regexp.Compile("(?i)" + p); err == nil {
			a.osPatterns = append(a.osPatterns, re)
		}
	}

	// OS/Image detection patterns
	osDetectors := []struct {
		name    string
		pattern string
	}{
		{"Bare Metal Services", `Bare Metal Services Ready`},
		{"OpenShift", `openshift|Red Hat OpenShift|CoreOS`},
		{"Kubernetes", `kubelet|kube-apiserver|k3s|k8s`},
		{"Docker", `dockerd|Docker Engine`},
		{"VMware ESXi", `VMware ESXi|vmkernel`},
		{"Ubuntu", `Ubuntu \d+\.\d+`},
		{"Debian", `Debian GNU/Linux`},
		{"CentOS", `CentOS Linux|CentOS Stream`},
		{"Rocky Linux", `Rocky Linux`},
		{"AlmaLinux", `AlmaLinux`},
		{"Red Hat Enterprise Linux", `Red Hat Enterprise Linux`},
		{"Fedora", `Fedora release`},
		{"Alpine Linux", `Alpine Linux`},
		{"Arch Linux", `Arch Linux`},
		{"FreeBSD", `FreeBSD`},
	}

	for _, d := range osDetectors {
		if re, err := regexp.Compile("(?i)" + d.pattern); err == nil {
			a.osDetectors = append(a.osDetectors, osDetector{
				name:    d.name,
				pattern: re,
			})
		}
	}

	// Hostname detection pattern (common login prompts)
	a.hostPattern = regexp.MustCompile(`(?m)^([a-zA-Z0-9][a-zA-Z0-9\-]{0,62}) login:`)

	// Network interface up/down patterns
	// Common patterns: "eth0: link up", "enp0s31f6: link down", "NIC Link is Up", etc.
	a.netUpPattern = regexp.MustCompile(`(?i)([a-z]{2,}[0-9]+[a-z0-9]*):?\s+(?:link\s+)?(?:is\s+)?up|NIC Link is Up.*?([a-z]{2,}[0-9]+)`)
	a.netDownPattern = regexp.MustCompile(`(?i)([a-z]{2,}[0-9]+[a-z0-9]*):?\s+(?:link\s+)?(?:is\s+)?down|NIC Link is Down.*?([a-z]{2,}[0-9]+)`)

	return a
}

func (a *Analytics) ProcessText(serverName, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	server, exists := a.servers[serverName]
	if !exists {
		server = &ServerAnalytics{
			ServerName:  serverName,
			BootHistory: make([]BootEvent, 0),
		}
		a.servers[serverName] = server
	}

	server.LastSeen = time.Now()
	changed := false

	// Consume pending rotation on first console output after rotation
	if server.pendingRotation != nil {
		server.rotationDelay = time.Since(*server.pendingRotation).Seconds()
		server.rotationTime = server.pendingRotation
		server.pendingRotation = nil
		log.Infof("Power-on delay for %s: %.1fs", serverName, server.rotationDelay)
	}

	// Check for BIOS (boot start)
	if a.matchesBIOS(text) {
		log.Debugf("BIOS detected for %s, CurrentBoot=%v", serverName, server.CurrentBoot != nil)
		// If we have a current boot, this is a reboot - archive the old boot
		if server.CurrentBoot != nil {
			// Only archive if the boot has been running for more than 30 seconds
			// This prevents multiple BIOS messages in same boot from creating duplicates
			elapsed := time.Since(server.CurrentBoot.StartTime)
			log.Debugf("Existing boot elapsed: %v", elapsed)
			if elapsed > 30*time.Second {
				log.Debugf("Archiving previous boot for %s (was complete=%v)", serverName, server.CurrentBoot.Complete)
				server.BootHistory = append(server.BootHistory, *server.CurrentBoot)
				// Keep only last 10 boots
				if len(server.BootHistory) > 10 {
					server.BootHistory = server.BootHistory[1:]
				}
				server.CurrentBoot = nil
				server.OSUpSince = nil
				changed = true
			}
		}

		// Start new boot tracking if not already tracking
		if server.CurrentBoot == nil {
			log.Infof("Starting new boot tracking for %s, TotalReboots will be %d", serverName, server.TotalReboots+1)
			server.CurrentBoot = &BootEvent{
				StartTime: time.Now(),
				Complete:  false,
			}
			// Apply rotation data if available
			if server.rotationTime != nil {
				server.CurrentBoot.RotationTime = server.rotationTime
				server.CurrentBoot.PowerOnDelay = server.rotationDelay
				server.rotationTime = nil
				server.rotationDelay = 0
			}
			server.TotalReboots++
			changed = true
		}
	}

	// Check for OS up (boot complete)
	if a.matchesOS(text) {
		if server.CurrentBoot != nil && !server.CurrentBoot.Complete {
			server.CurrentBoot.EndTime = time.Now()
			server.CurrentBoot.BootDuration = server.CurrentBoot.EndTime.Sub(server.CurrentBoot.StartTime).Seconds()
			server.CurrentBoot.Complete = true
			now := time.Now()
			server.OSUpSince = &now
			changed = true
		} else if server.OSUpSince == nil {
			// OS is up but we didn't see boot (service started after boot)
			now := time.Now()
			server.OSUpSince = &now
			changed = true
		}
	}

	// Detect OS/Image type
	if detectedOS := a.detectOS(text); detectedOS != "" {
		if server.CurrentOS != detectedOS {
			server.CurrentOS = detectedOS
			if server.CurrentBoot != nil {
				server.CurrentBoot.DetectedOS = detectedOS
			}
			changed = true
		}
	}

	// Detect hostname
	if hostname := a.detectHostname(text); hostname != "" {
		if server.Hostname != hostname {
			server.Hostname = hostname
			changed = true
		}
	}

	// Track network interface events
	a.trackNetworkEvents(server, text)

	// Save on significant changes
	if changed {
		a.save()
	}
}

func (a *Analytics) GetServerAnalytics(serverName string) *ServerAnalytics {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if server, exists := a.servers[serverName]; exists {
		// Return a deep copy
		copy := *server
		if server.CurrentBoot != nil {
			copy.CurrentBoot = copyBootEvent(server.CurrentBoot)
		}
		copy.BootHistory = make([]BootEvent, len(server.BootHistory))
		for i, b := range server.BootHistory {
			copy.BootHistory[i] = *copyBootEvent(&b)
		}
		return &copy
	}
	return &ServerAnalytics{
		ServerName:  serverName,
		BootHistory: make([]BootEvent, 0),
	}
}

func (a *Analytics) GetAllAnalytics() map[string]*ServerAnalytics {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]*ServerAnalytics)
	for name, server := range a.servers {
		copy := *server
		if server.CurrentBoot != nil {
			copy.CurrentBoot = copyBootEvent(server.CurrentBoot)
		}
		copy.BootHistory = make([]BootEvent, len(server.BootHistory))
		for i, b := range server.BootHistory {
			copy.BootHistory[i] = *copyBootEvent(&b)
		}
		result[name] = &copy
	}
	return result
}

func (a *Analytics) RecordRotation(serverName string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	server, exists := a.servers[serverName]
	if !exists {
		server = &ServerAnalytics{
			ServerName:  serverName,
			BootHistory: make([]BootEvent, 0),
		}
		a.servers[serverName] = server
	}

	now := time.Now()
	server.pendingRotation = &now
	log.Infof("Recorded rotation for %s at %s", serverName, now.Format(time.RFC3339))
}

func copyBootEvent(b *BootEvent) *BootEvent {
	if b == nil {
		return nil
	}
	copy := *b
	if len(b.NetworkEvents) > 0 {
		copy.NetworkEvents = make([]NetworkEvent, len(b.NetworkEvents))
		for i, e := range b.NetworkEvents {
			copy.NetworkEvents[i] = e
		}
	}
	if len(b.NetworkStats) > 0 {
		copy.NetworkStats = make([]NetworkStats, len(b.NetworkStats))
		for i, s := range b.NetworkStats {
			copy.NetworkStats[i] = s
		}
	}
	return &copy
}

func (a *Analytics) matchesBIOS(text string) bool {
	// Simple substring checks for common boot indicators
	lowerText := strings.ToLower(text)
	simplePatterns := []string{
		"ipxe",
		"pxe->",
		"pxe-e",
		"client mac addr",
		"boot agent",
		"undi code",
		"bios date",
		"american megatrends",
		"supermicro",
		"booting baremetalservices",
		"network boot",
	}
	for _, p := range simplePatterns {
		if strings.Contains(lowerText, p) {
			log.Debugf("BIOS pattern matched (simple): %q in text len=%d", p, len(text))
			return true
		}
	}

	for _, p := range a.biosPatterns {
		if p.MatchString(text) {
			log.Debugf("BIOS pattern matched (regex): %v", p)
			return true
		}
	}
	return false
}

func (a *Analytics) matchesOS(text string) bool {
	for _, p := range a.osPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

func (a *Analytics) getFilePath() string {
	return filepath.Join(a.dataPath, "analytics.json")
}

func (a *Analytics) save() {
	if a.dataPath == "" {
		return
	}

	data := struct {
		Servers map[string]*ServerAnalytics `json:"servers"`
	}{
		Servers: a.servers,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Errorf("Failed to marshal analytics: %v", err)
		return
	}

	if err := os.MkdirAll(a.dataPath, 0755); err != nil {
		log.Errorf("Failed to create analytics directory: %v", err)
		return
	}

	if err := os.WriteFile(a.getFilePath(), jsonData, 0644); err != nil {
		log.Errorf("Failed to save analytics: %v", err)
	}
}

func (a *Analytics) load() {
	if a.dataPath == "" {
		return
	}

	jsonData, err := os.ReadFile(a.getFilePath())
	if err != nil {
		if !os.IsNotExist(err) {
			log.Errorf("Failed to read analytics: %v", err)
		}
		return
	}

	var data struct {
		Servers map[string]*ServerAnalytics `json:"servers"`
	}

	if err := json.Unmarshal(jsonData, &data); err != nil {
		log.Errorf("Failed to unmarshal analytics: %v", err)
		return
	}

	if data.Servers != nil {
		a.servers = data.Servers
		log.Infof("Loaded analytics for %d servers", len(a.servers))
	}
}

func (a *Analytics) detectOS(text string) string {
	for _, detector := range a.osDetectors {
		if detector.pattern.MatchString(text) {
			return detector.name
		}
	}
	return ""
}

func (a *Analytics) detectHostname(text string) string {
	if a.hostPattern == nil {
		return ""
	}
	matches := a.hostPattern.FindStringSubmatch(text)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func (a *Analytics) trackNetworkEvents(server *ServerAnalytics, text string) {
	if server.CurrentBoot == nil {
		return
	}

	now := time.Now()

	// Check for link up events
	if a.netUpPattern != nil {
		matches := a.netUpPattern.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			iface := ""
			for i := 1; i < len(match); i++ {
				if match[i] != "" {
					iface = match[i]
					break
				}
			}
			if iface != "" {
				server.CurrentBoot.NetworkEvents = append(server.CurrentBoot.NetworkEvents, NetworkEvent{
					Interface: iface,
					Event:     "up",
					Time:      now,
				})
				a.updateNetworkStats(server.CurrentBoot, iface, "up")
			}
		}
	}

	// Check for link down events
	if a.netDownPattern != nil {
		matches := a.netDownPattern.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			iface := ""
			for i := 1; i < len(match); i++ {
				if match[i] != "" {
					iface = match[i]
					break
				}
			}
			if iface != "" {
				server.CurrentBoot.NetworkEvents = append(server.CurrentBoot.NetworkEvents, NetworkEvent{
					Interface: iface,
					Event:     "down",
					Time:      now,
				})
				a.updateNetworkStats(server.CurrentBoot, iface, "down")
			}
		}
	}
}

func (a *Analytics) updateNetworkStats(boot *BootEvent, iface, event string) {
	// Find or create stats for this interface
	var stats *NetworkStats
	for i := range boot.NetworkStats {
		if boot.NetworkStats[i].Interface == iface {
			stats = &boot.NetworkStats[i]
			break
		}
	}

	if stats == nil {
		boot.NetworkStats = append(boot.NetworkStats, NetworkStats{Interface: iface})
		stats = &boot.NetworkStats[len(boot.NetworkStats)-1]
	}

	if event == "up" {
		stats.UpCount++
	} else {
		stats.DownCount++
	}
}
