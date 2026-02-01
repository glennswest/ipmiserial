package sol

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

type Session struct {
	ServerName  string
	IP          string
	Connected   bool
	LastError   string
	cancel      context.CancelFunc
	cmd         *exec.Cmd
	subscribers map[chan []byte]struct{}
	subMu       sync.RWMutex
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
		// Kill existing helper process
		if existing.cmd != nil && existing.cmd.Process != nil {
			existing.cmd.Process.Kill()
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
		// Kill helper process
		if session.cmd != nil && session.cmd.Process != nil {
			session.cmd.Process.Kill()
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

		log.Infof("Starting SOL helper for %s (%s)", session.ServerName, session.IP)

		err := m.runHelper(ctx, session)
		if err != nil {
			session.Connected = false
			session.LastError = err.Error()
			log.Errorf("SOL helper failed for %s: %v", session.ServerName, err)
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

func (m *Manager) runHelper(ctx context.Context, session *Session) error {
	// Get current log file path
	logFile := m.getLogFilePath(session.ServerName)

	// Ensure log directory exists
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}

	// Start the helper script
	cmd := exec.CommandContext(ctx, "/usr/local/bin/sol_helper.sh",
		session.ServerName,
		session.IP,
		m.username,
		m.password,
		logFile,
	)

	session.cmd = cmd

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start helper: %w", err)
	}

	session.Connected = true
	session.LastError = ""
	log.Infof("SOL helper started for %s, PID %d, log: %s", session.ServerName, cmd.Process.Pid, logFile)

	// Watch log file for changes and broadcast to subscribers
	go m.watchLogFile(ctx, session, logFile)

	// Wait for helper to exit
	err := cmd.Wait()
	session.Connected = false

	if err != nil {
		return fmt.Errorf("helper exited: %w", err)
	}
	return fmt.Errorf("helper exited")
}

func (m *Manager) getLogFilePath(serverName string) string {
	// Use current.log symlink - it should already exist from rotate call
	dir := filepath.Join(m.logPath, serverName)
	symlinkPath := filepath.Join(dir, "current.log")

	// Follow the symlink to get actual file path
	if target, err := os.Readlink(symlinkPath); err == nil {
		return filepath.Join(dir, target)
	}

	// Fallback: use current.log directly (helper will create it)
	return symlinkPath
}

func (m *Manager) watchLogFile(ctx context.Context, session *Session, logFile string) {
	// Open file for tailing
	f, err := os.Open(logFile)
	if err != nil {
		log.Errorf("Failed to open log file for watching: %v", err)
		return
	}
	defer f.Close()

	// Seek to end
	f.Seek(0, io.SeekEnd)

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Errorf("Failed to create file watcher: %v", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(logFile); err != nil {
		log.Errorf("Failed to watch log file: %v", err)
		return
	}

	buf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				// Read new data
				for {
					n, err := f.Read(buf)
					if n > 0 {
						data := make([]byte, n)
						copy(data, buf[:n])

						// Process for analytics
						if m.analytics != nil {
							m.analytics.ProcessText(session.ServerName, string(data))
						}

						// Broadcast to subscribers
						session.subMu.RLock()
						for ch := range session.subscribers {
							select {
							case ch <- data:
							default:
								// Drop if full
							}
						}
						session.subMu.RUnlock()
					}
					if err != nil {
						break
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Errorf("Watcher error for %s: %v", session.ServerName, err)
		}
	}
}
