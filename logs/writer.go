package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Writer struct {
	basePath      string
	retentionDays int
	files         map[string]*os.File
	mu            sync.Mutex
}

func NewWriter(basePath string, retentionDays int) *Writer {
	return &Writer{
		basePath:      basePath,
		retentionDays: retentionDays,
		files:         make(map[string]*os.File),
	}
}

func (w *Writer) Write(serverName string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.getOrCreateFile(serverName)
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	return err
}

func (w *Writer) Rotate(serverName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Close existing file
	if f, exists := w.files[serverName]; exists {
		f.Close()
		delete(w.files, serverName)
	}

	// New file will be created on next write
	log.Infof("Rotated log for %s", serverName)
	return nil
}

func (w *Writer) getOrCreateFile(serverName string) (*os.File, error) {
	if f, exists := w.files[serverName]; exists {
		return f, nil
	}

	dir := filepath.Join(w.basePath, serverName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	filename := time.Now().Format("2006-01-02_15-04-05") + ".log"
	path := filepath.Join(dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	w.files[serverName] = f

	// Update current.log symlink
	symlinkPath := filepath.Join(dir, "current.log")
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
