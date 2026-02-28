package sol

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gwest/go-sol"
	log "github.com/sirupsen/logrus"
)

type Session struct {
	ServerName   string
	IP           string
	Username     string
	Password     string
	Connected    bool
	LastError    string
	LastActivity time.Time
	cancel       context.CancelFunc
	solSession   *sol.Session
}

type Manager struct {
	username       string
	password       string
	logPath        string
	sessions       map[string]*Session
	mu             sync.RWMutex
	logWriter      LogWriter
	rebootDetector *RebootDetector
	analytics      *Analytics
	subscribers    map[string][]chan []byte
	subMu          sync.RWMutex
	screenBufs     map[string]*ScreenBuffer
}

type LogWriter interface {
	Write(serverName string, data []byte) error
	Rotate(serverName string) error
	CanRotate(serverName string) bool
}

func NewManager(username, password string, logWriter LogWriter, rebootDetector *RebootDetector, dataPath string) *Manager {
	m := &Manager{
		username:       username,
		password:       password,
		logPath:        dataPath,
		sessions:       make(map[string]*Session),
		logWriter:      logWriter,
		rebootDetector: rebootDetector,
		analytics:      NewAnalytics(dataPath),
		subscribers:    make(map[string][]chan []byte),
		screenBufs:     make(map[string]*ScreenBuffer),
	}
	go m.healthCheck()
	return m
}

func (m *Manager) GetAnalytics(serverName string) *ServerAnalytics {
	return m.analytics.GetServerAnalytics(serverName)
}

func (m *Manager) GetAllAnalytics() map[string]*ServerAnalytics {
	return m.analytics.GetAllAnalytics()
}

func (m *Manager) RecordRotation(serverName string) {
	m.analytics.RecordRotation(serverName)
}

func (m *Manager) StartSession(serverName, ip, username, password string) {
	m.mu.Lock()
	if existing, exists := m.sessions[serverName]; exists {
		if existing.cancel != nil {
			existing.cancel()
		}
		if existing.solSession != nil {
			existing.solSession.Close()
		}
	}

	// Use per-server credentials, fall back to global config
	if username == "" {
		username = m.username
	}
	if password == "" {
		password = m.password
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		ServerName: serverName,
		IP:         ip,
		Username:   username,
		Password:   password,
		Connected:  false,
		cancel:     cancel,
	}
	m.sessions[serverName] = session
	m.mu.Unlock()

	go m.runSession(ctx, session)
}

func (m *Manager) StopSession(serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists := m.sessions[serverName]; exists {
		if session.cancel != nil {
			session.cancel()
		}
		if session.solSession != nil {
			session.solSession.Close()
		}
		go clearBMCSessions(session.IP, session.Username, session.Password)
		delete(m.sessions, serverName)
	}
}

// RestartSession stops the current SOL session, clears stale BMC sessions,
// and starts a fresh connection. Used on log rotation to ensure clean SOL stream.
func (m *Manager) RestartSession(serverName string) {
	m.mu.Lock()
	session, exists := m.sessions[serverName]
	if !exists {
		m.mu.Unlock()
		return
	}
	ip := session.IP
	username := session.Username
	password := session.Password
	m.mu.Unlock()

	log.Infof("Restarting SOL session for %s", serverName)
	m.StopSession(serverName)
	clearBMCSessions(ip, username, password)
	m.StartSession(serverName, ip, username, password)
}

func (m *Manager) GetSession(serverName string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[serverName]
}

func (m *Manager) SendCommand(serverName string, data []byte) error {
	m.mu.RLock()
	session, exists := m.sessions[serverName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}
	if !session.Connected || session.solSession == nil {
		return fmt.Errorf("server not connected: %s", serverName)
	}
	return session.solSession.Write(data)
}

func (m *Manager) GetSessions() map[string]*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*Session)
	for k, v := range m.sessions {
		result[k] = v
	}
	return result
}

func (m *Manager) Subscribe(serverName string) chan []byte {
	ch := make(chan []byte, 64)
	m.subMu.Lock()
	m.subscribers[serverName] = append(m.subscribers[serverName], ch)
	m.subMu.Unlock()
	return ch
}

func (m *Manager) Unsubscribe(serverName string, ch chan []byte) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	subs := m.subscribers[serverName]
	for i, s := range subs {
		if s == ch {
			m.subscribers[serverName] = append(subs[:i], subs[i+1:]...)
			close(ch)
			return
		}
	}
}

func (m *Manager) getOrCreateScreenBuf(name string) *ScreenBuffer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.screenBufs[name] == nil {
		m.screenBufs[name] = NewScreenBuffer(defaultScreenBufSize)
	}
	return m.screenBufs[name]
}

func (m *Manager) GetScreenBuffer(serverName string) []byte {
	m.mu.RLock()
	sb := m.screenBufs[serverName]
	m.mu.RUnlock()
	if sb == nil {
		return nil
	}
	return sb.Bytes()
}

func (m *Manager) broadcast(serverName string, data []byte) {
	m.subMu.RLock()
	subs := m.subscribers[serverName]
	m.subMu.RUnlock()
	for _, ch := range subs {
		// Non-blocking send — drop data for slow clients
		select {
		case ch <- data:
		default:
		}
	}
}

// healthCheck periodically inspects all connected sessions for staleness.
// It checks go-sol's lastRecvTime (which tracks ALL BMC packets, including
// keepalive responses) rather than LastActivity (which only tracks SOL data).
// This correctly handles idle servers that produce no console output.
func (m *Manager) healthCheck() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	const staleThreshold = 90 * time.Second

	for range ticker.C {
		m.mu.RLock()
		var stale []string
		for name, session := range m.sessions {
			if !session.Connected {
				continue
			}
			if session.solSession == nil {
				log.Warnf("Health check: %s marked connected but solSession is nil, will restart", name)
				stale = append(stale, name)
				continue
			}
			lastRecv := session.solSession.LastRecvTime()
			idle := time.Since(lastRecv)
			if idle > staleThreshold {
				log.Warnf("Health check: %s no BMC packets for %v (threshold %v), will restart", name, idle.Round(time.Second), staleThreshold)
				stale = append(stale, name)
				continue
			}
			log.Debugf("Health check: %s ok (last BMC packet %v ago)", name, idle.Round(time.Second))
		}
		m.mu.RUnlock()

		for _, name := range stale {
			m.RestartSession(name)
		}
	}
}

func (m *Manager) runSession(ctx context.Context, session *Session) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Infof("Connecting native SOL to %s (%s)", session.ServerName, session.IP)

		connectTime := time.Now()
		err := m.connectSOL(ctx, session)
		if err != nil {
			session.Connected = false
			session.LastError = err.Error()
			log.Errorf("SOL connection failed for %s: %v", session.ServerName, err)

			// If we were connected for more than 30 seconds, reset backoff
			// (this was a session that worked, not an immediate connection failure)
			if time.Since(connectTime) > 30*time.Second {
				backoff = time.Second
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff = backoff * 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}
	}
}

// clearBMCSessions clears stale Redfish sessions on Dell iDRAC before/after SOL operations.
// Non-Dell BMCs will simply not respond and we skip silently.
func clearBMCSessions(ip, username, password string) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	sessURL := fmt.Sprintf("https://%s/redfish/v1/Sessions", ip)
	req, err := http.NewRequest("GET", sessURL, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(username, password)

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return
	}

	var result struct {
		Members []struct {
			ID string `json:"@odata.id"`
		} `json:"Members"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	cleared := 0
	for _, m := range result.Members {
		delURL := fmt.Sprintf("https://%s%s", ip, m.ID)
		delReq, err := http.NewRequest("DELETE", delURL, nil)
		if err != nil {
			continue
		}
		delReq.SetBasicAuth(username, password)
		delResp, err := client.Do(delReq)
		if err == nil {
			delResp.Body.Close()
			cleared++
		}
	}
	if cleared > 0 {
		log.Infof("Cleared %d stale BMC sessions on %s", cleared, ip)
	}
}

func (m *Manager) connectSOL(ctx context.Context, session *Session) error {
	// Ensure log directory exists
	logDir := filepath.Join(m.logPath, session.ServerName)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}

	// Clear stale sessions before connecting
	clearBMCSessions(session.IP, session.Username, session.Password)

	// Create native SOL session using per-server credentials
	solSession := sol.New(sol.Config{
		Host:              session.IP,
		Port:              623,
		Username:          session.Username,
		Password:          session.Password,
		Timeout:           30 * time.Second,
		InactivityTimeout: 2 * time.Minute,
		Logf: func(format string, args ...interface{}) {
			log.Debugf("[go-sol] "+format, args...)
		},
	})

	// Connect with timeout
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := solSession.Connect(connectCtx)
	cancel()

	if err != nil {
		return fmt.Errorf("SOL connect failed: %w", err)
	}

	session.solSession = solSession
	session.Connected = true
	session.LastError = ""
	session.LastActivity = time.Now()
	log.Infof("Native SOL connected to %s", session.ServerName)

	// Clear screen for all SSE subscribers so xterm.js starts fresh
	m.broadcast(session.ServerName, []byte("\x1b[2J\x1b[H"))

	// Reset screen buffer for new connection
	sb := m.getOrCreateScreenBuf(session.ServerName)
	sb.Reset()

	// Read data from SOL and distribute
	readCh := solSession.Read()
	errCh := solSession.Err()

	for {
		select {
		case <-ctx.Done():
			solSession.Close()
			session.Connected = false
			go clearBMCSessions(session.IP, session.Username, session.Password)
			return ctx.Err()

		case err := <-errCh:
			solSession.Close()
			session.Connected = false
			go clearBMCSessions(session.IP, session.Username, session.Password)
			return fmt.Errorf("SOL error: %w", err)

		case data, ok := <-readCh:
			if !ok {
				session.Connected = false
				return fmt.Errorf("SOL session closed")
			}

			session.LastActivity = time.Now()

			// Broadcast raw data to SSE subscribers
			m.broadcast(session.ServerName, data)

			// Write to screen buffer for catchup on server switch
			sb.Write(data)

			// Write to log file (cleaned)
			if m.logWriter != nil {
				m.logWriter.Write(session.ServerName, data)
			}

			// Process for analytics
			if m.analytics != nil {
				m.analytics.ProcessText(session.ServerName, string(data))
			}
		}
	}
}
