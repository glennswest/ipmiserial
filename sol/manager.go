package sol

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gwest/go-sol"
	log "github.com/sirupsen/logrus"
)

type Session struct {
	ServerName  string
	IP          string
	Connected   bool
	LastError   string
	cancel     context.CancelFunc
	solSession *sol.Session
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
}

type LogWriter interface {
	Write(serverName string, data []byte) error
	Rotate(serverName string) error
	CanRotate(serverName string) bool
}

func NewManager(username, password string, logWriter LogWriter, rebootDetector *RebootDetector, dataPath string) *Manager {
	return &Manager{
		username:       username,
		password:       password,
		logPath:        dataPath,
		sessions:       make(map[string]*Session),
		logWriter:      logWriter,
		rebootDetector: rebootDetector,
		analytics:      NewAnalytics(dataPath),
	}
}

func (m *Manager) GetAnalytics(serverName string) *ServerAnalytics {
	return m.analytics.GetServerAnalytics(serverName)
}

func (m *Manager) GetAllAnalytics() map[string]*ServerAnalytics {
	return m.analytics.GetAllAnalytics()
}

func (m *Manager) StartSession(serverName, ip string) {
	m.mu.Lock()
	if existing, exists := m.sessions[serverName]; exists {
		if existing.cancel != nil {
			existing.cancel()
		}
		if existing.solSession != nil {
			existing.solSession.Close()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		ServerName: serverName,
		IP:         ip,
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
		delete(m.sessions, serverName)
	}
}

func (m *Manager) GetSession(serverName string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[serverName]
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

func (m *Manager) connectSOL(ctx context.Context, session *Session) error {
	// Ensure log directory exists
	logDir := filepath.Join(m.logPath, session.ServerName)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}

	// Create native SOL session
	solSession := sol.New(sol.Config{
		Host:              session.IP,
		Port:              623,
		Username:          m.username,
		Password:          m.password,
		Timeout:           30 * time.Second,
		InactivityTimeout: 5 * time.Minute,
		Logf: func(format string, args ...interface{}) {
			log.Infof("[go-sol] "+format, args...)
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
	log.Infof("Native SOL connected to %s", session.ServerName)

	// Read data from SOL and distribute
	readCh := solSession.Read()
	errCh := solSession.Err()

	for {
		select {
		case <-ctx.Done():
			solSession.Close()
			session.Connected = false
			return ctx.Err()

		case err := <-errCh:
			solSession.Close()
			session.Connected = false
			return fmt.Errorf("SOL error: %w", err)

		case data, ok := <-readCh:
			if !ok {
				session.Connected = false
				return fmt.Errorf("SOL session closed")
			}

			// Write to log file
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
