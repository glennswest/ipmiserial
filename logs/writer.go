package logs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Cursor position pattern - these should become newlines
var cursorPosRegex = regexp.MustCompile(`\x1b\[\d+;\d*[Hf]|\x1b\[\d+[Hf]`)

// ANSI escape code pattern - matches all other escape sequences
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][AB012]|\x1b[=>]|\x1b[78]|\x1b[DMEHc]`)

type Writer struct {
	basePath       string
	retentionDays  int
	files          map[string]*os.File
	lastRotation   map[string]time.Time // track last rotation time per server
	mu             sync.Mutex
}

func NewWriter(basePath string, retentionDays int) *Writer {
	return &Writer{
		basePath:      basePath,
		retentionDays: retentionDays,
		files:         make(map[string]*os.File),
		lastRotation:  make(map[string]time.Time),
	}
}

func (w *Writer) Write(serverName string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.getOrCreateFile(serverName)
	if err != nil {
		return err
	}

	// Clean the data before writing
	cleaned := cleanLogData(data)
	if len(cleaned) == 0 {
		return nil
	}

	_, err = f.Write(cleaned)
	return err
}

// cleanLogData removes ANSI escape codes and control characters from log data
func cleanLogData(data []byte) []byte {
	// Replace cursor positioning with newlines (BIOS uses these instead of \n)
	data = cursorPosRegex.ReplaceAll(data, []byte("\n"))

	// Remove other ANSI escape sequences
	data = ansiRegex.ReplaceAll(data, nil)

	// Remove control characters except newline and tab
	result := make([]byte, 0, len(data))
	for _, c := range data {
		if c == '\n' || c == '\t' || (c >= 32 && c < 127) {
			result = append(result, c)
		}
	}

	// Remove carriage returns
	result = bytes.ReplaceAll(result, []byte{'\r'}, nil)

	// Collapse multiple consecutive newlines into max 2
	for bytes.Contains(result, []byte("\n\n\n")) {
		result = bytes.ReplaceAll(result, []byte("\n\n\n"), []byte("\n\n"))
	}

	return result
}

// CanRotate checks if enough time has passed since last rotation (2 minute cooldown)
func (w *Writer) CanRotate(serverName string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if lastTime, exists := w.lastRotation[serverName]; exists {
		if time.Since(lastTime) < 2*time.Minute {
			return false
		}
	}
	return true
}

func (w *Writer) Rotate(serverName string) error {
	_, err := w.RotateWithName(serverName, "")
	return err
}

func (w *Writer) RotateWithName(serverName, logName string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Close existing file
	if f, exists := w.files[serverName]; exists {
		f.Close()
		delete(w.files, serverName)
	}

	dir := filepath.Join(w.basePath, serverName)
	symlinkPath := filepath.Join(dir, "current.log")

	// Remove current.log symlink
	os.Remove(symlinkPath)

	// Record rotation time
	w.lastRotation[serverName] = time.Now()

	// Create directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	// Use custom name or generate timestamp name
	if logName == "" {
		logName = time.Now().Format("2006-01-02_15-04-05")
	} else {
		logName = filepath.Base(logName)
	}
	if filepath.Ext(logName) != ".log" {
		logName = logName + ".log"
	}

	path := filepath.Join(dir, logName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create log file: %w", err)
	}

	w.files[serverName] = f

	// Update current.log symlink
	os.Symlink(logName, symlinkPath)

	log.Infof("Rotated log for %s to %s", serverName, logName)
	return logName, nil
}

func (w *Writer) getOrCreateFile(serverName string) (*os.File, error) {
	if f, exists := w.files[serverName]; exists {
		return f, nil
	}

	dir := filepath.Join(w.basePath, serverName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Try to continue existing current.log if it exists
	symlinkPath := filepath.Join(dir, "current.log")
	if target, err := os.Readlink(symlinkPath); err == nil {
		existingPath := filepath.Join(dir, target)
		if f, err := os.OpenFile(existingPath, os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			w.files[serverName] = f
			log.Infof("Continuing existing log file: %s", existingPath)
			return f, nil
		}
	}

	// Create new log file
	filename := time.Now().Format("2006-01-02_15-04-05") + ".log"
	path := filepath.Join(dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	w.files[serverName] = f

	// Update current.log symlink
	os.Remove(symlinkPath)
	os.Symlink(filename, symlinkPath)

	log.Infof("Created log file: %s", path)

	return f, nil
}

func (w *Writer) ListLogs(serverName string) ([]string, error) {
	dir := filepath.Join(w.basePath, serverName)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var logs []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" && entry.Name() != "current.log" {
			logs = append(logs, entry.Name())
		}
	}

	// Sort newest first
	sort.Sort(sort.Reverse(sort.StringSlice(logs)))

	return logs, nil
}

func (w *Writer) GetLogPath(serverName, filename string) string {
	return filepath.Join(w.basePath, serverName, filename)
}

func (w *Writer) GetCurrentLogContent(serverName string) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Sync current file to disk first
	if f, exists := w.files[serverName]; exists {
		f.Sync()
	}

	// Read the current log file
	currentPath := filepath.Join(w.basePath, serverName, "current.log")
	data, err := os.ReadFile(currentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, err
	}
	return data, nil
}

func (w *Writer) Cleanup() {
	if w.retentionDays <= 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)

	entries, err := os.ReadDir(w.basePath)
	if err != nil {
		return
	}

	for _, serverDir := range entries {
		if !serverDir.IsDir() {
			continue
		}

		serverPath := filepath.Join(w.basePath, serverDir.Name())
		logFiles, err := os.ReadDir(serverPath)
		if err != nil {
			continue
		}

		for _, logFile := range logFiles {
			if logFile.IsDir() || filepath.Ext(logFile.Name()) != ".log" {
				continue
			}

			info, err := logFile.Info()
			if err != nil {
				continue
			}

			if info.ModTime().Before(cutoff) {
				path := filepath.Join(serverPath, logFile.Name())
				os.Remove(path)
				log.Infof("Cleaned up old log: %s", path)
			}
		}
	}
}

func (w *Writer) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, f := range w.files {
		f.Close()
	}
	w.files = make(map[string]*os.File)
}

func (w *Writer) ClearLogs(serverName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Close the current file if open
	if f, exists := w.files[serverName]; exists {
		f.Close()
		delete(w.files, serverName)
	}

	dir := filepath.Join(w.basePath, serverName)

	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			path := filepath.Join(dir, entry.Name())
			os.Remove(path)
		}
	}

	// Create a fresh log file immediately
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	filename := time.Now().Format("2006-01-02_15-04-05") + ".log"
	path := filepath.Join(dir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.files[serverName] = f

	// Update symlink
	symlinkPath := filepath.Join(dir, "current.log")
	os.Remove(symlinkPath)
	os.Symlink(filename, symlinkPath)

	log.Infof("Cleared logs for %s, created %s", serverName, filename)
	return nil
}

func (w *Writer) ClearAllLogs() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Close all open files
	for _, f := range w.files {
		f.Close()
	}
	w.files = make(map[string]*os.File)

	entries, err := os.ReadDir(w.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Clear and recreate logs for each server
	for _, serverDir := range entries {
		if !serverDir.IsDir() {
			continue
		}

		serverName := serverDir.Name()
		serverPath := filepath.Join(w.basePath, serverName)
		logFiles, err := os.ReadDir(serverPath)
		if err != nil {
			continue
		}

		for _, logFile := range logFiles {
			if !logFile.IsDir() {
				path := filepath.Join(serverPath, logFile.Name())
				os.Remove(path)
			}
		}

		// Create fresh log file
		filename := time.Now().Format("2006-01-02_15-04-05") + ".log"
		path := filepath.Join(serverPath, filename)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			continue
		}
		w.files[serverName] = f

		// Update symlink
		symlinkPath := filepath.Join(serverPath, "current.log")
		os.Symlink(filename, symlinkPath)
	}

	log.Info("Cleared all logs")
	return nil
}
