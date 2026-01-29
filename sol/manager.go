package sol

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	log "github.com/sirupsen/logrus"
)

type Session struct {
	ServerName  string
	IP          string
	Connected   bool
	LastError   string
	cancel      context.CancelFunc
	subscribers map[chan []byte]struct{}
	subMu       sync.RWMutex
}

type Manager struct {
	username       string
	password       string
	sessions       map[string]*Session
	mu             sync.RWMutex
	logWriter      LogWriter
	rebootDetector *RebootDetector
}

type LogWriter interface {
	Write(serverName string, data []byte) error
	Rotate(serverName string) error
}

func NewManager(username, password string, logWriter LogWriter, rebootDetector *RebootDetector) *Manager {
	return &Manager{
		username:       username,
		password:       password,
		sessions:       make(map[string]*Session),
		logWriter:      logWriter,
		rebootDetector: rebootDetector,
	}
}

func (m *Manager) StartSession(serverName, ip string) {
	m.mu.Lock()
	if existing, exists := m.sessions[serverName]; exists {
		if existing.cancel != nil {
			existing.cancel()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		ServerName:  serverName,
		IP:          ip,
		Connected:   false,
		cancel:      cancel,
		subscribers: make(map[chan []byte]struct{}),
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
		// Close all subscriber channels
		session.subMu.Lock()
		for ch := range session.subscribers {
			close(ch)
		}
		session.subscribers = nil
		session.subMu.Unlock()
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

func (m *Manager) Subscribe(serverName string) (<-chan []byte, func()) {
	m.mu.RLock()
	session, exists := m.sessions[serverName]
	m.mu.RUnlock()

	if !exists {
		return nil, nil
	}

	ch := make(chan []byte, 100)

	session.subMu.Lock()
	session.subscribers[ch] = struct{}{}
	session.subMu.Unlock()

	unsubscribe := func() {
		session.subMu.Lock()
		delete(session.subscribers, ch)
		session.subMu.Unlock()
	}

	return ch, unsubscribe
}

func (m *Manager) runSession(ctx context.Context, session *Session) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Infof("Connecting SOL to %s (%s)", session.ServerName, session.IP)

		err := m.connectSOL(ctx, session)
		if err != nil {
			session.Connected = false
			session.LastError = err.Error()
			log.Errorf("SOL connection failed for %s: %v", session.ServerName, err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			// Exponential backoff with max 60s
			backoff = backoff * 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}
	}
}

func (m *Manager) connectSOL(ctx context.Context, session *Session) error {
	// First deactivate any existing SOL session
	deactivate := exec.CommandContext(ctx, "ipmitool",
		"-I", "lanplus",
		"-H", session.IP,
		"-U", m.username,
		"-P", m.password,
		"sol", "deactivate",
	)
	deactivate.Run() // Ignore errors

	// Start SOL session with PTY
	cmd := exec.CommandContext(ctx, "ipmitool",
		"-I", "lanplus",
		"-H", session.IP,
		"-U", m.username,
		"-P", m.password,
		"sol", "activate",
	)

	// Start command with PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start with pty: %w", err)
	}
	defer ptmx.Close()

	session.Connected = true
	session.LastError = ""

	// Read output in goroutine
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				if err != io.EOF {
					done <- err
				}
				close(done)
				return
			}

			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])

				// Write to log file
				if m.logWriter != nil {
					m.logWriter.Write(session.ServerName, data)
				}

				// Check for reboot
				if m.rebootDetector != nil && m.rebootDetector.Check(string(data)) {
					log.Infof("Reboot detected for %s", session.ServerName)
					if m.logWriter != nil {
						m.logWriter.Rotate(session.ServerName)
					}
				}

				// Broadcast to all subscribers (non-blocking)
				session.subMu.RLock()
				for ch := range session.subscribers {
					select {
					case ch <- data:
					default:
						// Drop if subscriber channel full
					}
				}
				session.subMu.RUnlock()
			}
		}
	}()

	// Wait for context cancellation or command exit
	select {
	case <-ctx.Done():
		cmd.Process.Kill()
		return ctx.Err()
	case err := <-done:
		session.Connected = false
		cmd.Wait()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}
		return fmt.Errorf("SOL session ended")
	}
}
