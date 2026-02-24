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

// Orphaned ANSI fragments — bracket sequences left after ESC byte was stripped
// Matches: [=3h [0m [01;00H [?25l etc. Also catches incomplete [01;01 (no letter)
var orphanedAnsiRegex = regexp.MustCompile(`\[[=?]?[\d;]*[A-Za-z]|\[[=?]?[\d;]+$`)
var orphanedAnsiLineRegex = regexp.MustCompile(`(?m)\[[=?]?[\d;]+$`)

// repeatTracker detects repeating multi-line blocks.
// First copy is always written. On detecting a repeat, suppresses further
// copies and tracks count. When the pattern breaks (or on flush), emits
// "(Duplicated N times)" to the log.
type repeatTracker struct {
	ring     []string // circular buffer of recent lines
	pos      int      // next write position
	blockLen int      // detected repeating block length (0 = no repeat)
	dupCount int      // number of suppressed repetitions
	suppress bool     // currently suppressing output
}

const repeatRingSize = 64

func newRepeatTracker() *repeatTracker {
	return &repeatTracker{
		ring: make([]string, repeatRingSize),
	}
}

// DupCount returns the current suppressed repetition count (for live display).
func (rt *repeatTracker) DupCount() int {
	return rt.dupCount
}

// checkLine returns true if this line should be written, false if suppressed.
func (rt *repeatTracker) checkLine(line string) (write bool, banner string) {
	trimmed := bytes.TrimRight([]byte(line), " \t")
	line = string(trimmed)
	if line == "" {
		return true, ""
	}

	// Store line in ring
	rt.ring[rt.pos] = line
	rt.pos = (rt.pos + 1) % repeatRingSize

	if rt.suppress {
		// Check if we're still in the same repeating block
		idx := (rt.pos - 1 - rt.blockLen + repeatRingSize*2) % repeatRingSize
		if rt.ring[idx] == line {
			// Count full block repetitions
			rt.dupCount++
			return false, ""
		}
		// Pattern broken — emit final count and resume
		reps := rt.dupCount / rt.blockLen
		rt.suppress = false
		rt.blockLen = 0
		rt.dupCount = 0
		if reps > 0 {
			banner = fmt.Sprintf("\n(Duplicated %d times)\n", reps)
		}
		return true, banner
	}

	// Detect repeat: need 2 identical consecutive blocks (first copy already written)
	// Min block size 4 to avoid false positives on interleaved screen redraws
	for bl := 4; bl <= repeatRingSize/2; bl++ {
		match := true
		totalLen := 0
		for i := 0; i < bl; i++ {
			a := (rt.pos - 1 - i + repeatRingSize*2) % repeatRingSize
			b := (rt.pos - 1 - i - bl + repeatRingSize*2) % repeatRingSize
			if rt.ring[a] == "" || rt.ring[a] != rt.ring[b] {
				match = false
				break
			}
			totalLen += len(rt.ring[a])
		}
		// Require substantial content to avoid false positives on short fragments
		if match && totalLen >= 40 {
			rt.blockLen = bl
			rt.dupCount = bl
			rt.suppress = true
			return false, ""
		}
	}

	return true, ""
}

type Writer struct {
	basePath      string
	retentionDays int
	files         map[string]*os.File
	lastRotation  map[string]time.Time    // track last rotation time per server
	pending       map[string][]byte       // partial data buffer per server
	lastLine      map[string][]byte       // last written line per server (for dedup)
	trailingNL    map[string]int          // trailing newline count from last write
	repeats       map[string]*repeatTracker // multi-line block dedup per server
	mu            sync.Mutex
}

func NewWriter(basePath string, retentionDays int) *Writer {
	return &Writer{
		basePath:      basePath,
		retentionDays: retentionDays,
		files:         make(map[string]*os.File),
		lastRotation:  make(map[string]time.Time),
		pending:       make(map[string][]byte),
		lastLine:      make(map[string][]byte),
		trailingNL:    make(map[string]int),
		repeats:       make(map[string]*repeatTracker),
	}
}

func (w *Writer) Write(serverName string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.getOrCreateFile(serverName)
	if err != nil {
		return err
	}

	// Prepend any pending data from previous chunk to handle split escape sequences
	if prev, ok := w.pending[serverName]; ok && len(prev) > 0 {
		data = append(prev, data...)
		delete(w.pending, serverName)
	}

	// If data ends mid-escape sequence, buffer the incomplete tail
	if i := bytes.LastIndexByte(data, '\x1b'); i >= 0 && i > len(data)-6 {
		tail := data[i:]
		// Only buffer if the sequence is incomplete (doesn't end with a letter)
		last := tail[len(tail)-1]
		if !((last >= 'A' && last <= 'Z') || (last >= 'a' && last <= 'z')) {
			w.pending[serverName] = append([]byte{}, tail...)
			data = data[:i]
		}
	}

	cleaned := cleanLogData(data)
	if len(cleaned) == 0 {
		return nil
	}

	// Deduplicate consecutive spinner lines (e.g. BIOS "DHCP..../", "DHCP....-").
	// Strip leading newlines (cursor-position escapes become \n in cleaning)
	// so the dedup check sees the actual content, not the \n prefix.
	content := bytes.TrimLeft(cleaned, "\n")
	if len(content) > 0 && !bytes.Contains(content, []byte("\n")) {
		trimmed := bytes.TrimRight(content, " \t")
		normalized := bytes.TrimRight(trimmed, "/-\\|.")
		if last, ok := w.lastLine[serverName]; ok && bytes.Equal(normalized, last) {
			return nil
		}
		w.lastLine[serverName] = append([]byte{}, normalized...)
	} else if len(content) > 0 {
		// Multi-line write: track the last line
		if idx := bytes.LastIndexByte(content, '\n'); idx >= 0 {
			last := bytes.TrimRight(content[idx+1:], " \t")
			last = bytes.TrimRight(last, "/-\\|.")
			if len(last) > 0 {
				w.lastLine[serverName] = append([]byte{}, last...)
			}
		}
	}

	// Prevent runs of blank lines across write boundaries.
	// Allow at most 2 consecutive newlines (1 blank line) in the file.
	prevNL := w.trailingNL[serverName]
	if prevNL > 0 {
		leadingNL := 0
		for leadingNL < len(cleaned) && cleaned[leadingNL] == '\n' {
			leadingNL++
		}
		// Total consecutive newlines = trailing from last write + leading in this write.
		// Cap at 2 (one blank line).
		if total := prevNL + leadingNL; total > 2 {
			trim := total - 2
			if trim > leadingNL {
				trim = leadingNL
			}
			cleaned = cleaned[trim:]
		}
	}
	if len(cleaned) == 0 {
		return nil
	}

	// Multi-line block dedup: detect repeating blocks (e.g. PXE boot loops)
	rt := w.repeats[serverName]
	if rt == nil {
		rt = newRepeatTracker()
		w.repeats[serverName] = rt
	}

	lines := bytes.Split(cleaned, []byte("\n"))
	var out []byte
	for _, line := range lines {
		write, banner := rt.checkLine(string(line))
		if banner != "" {
			out = append(out, []byte(banner)...)
		}
		if write {
			out = append(out, line...)
			out = append(out, '\n')
		}
	}
	// Trim the trailing \n we added to the last line if cleaned didn't end with one
	if len(cleaned) > 0 && cleaned[len(cleaned)-1] != '\n' && len(out) > 0 {
		out = out[:len(out)-1]
	}
	cleaned = out

	if len(cleaned) == 0 {
		return nil
	}

	// Track trailing newlines for next write
	trailNL := 0
	for i := len(cleaned) - 1; i >= 0 && cleaned[i] == '\n'; i-- {
		trailNL++
	}
	w.trailingNL[serverName] = trailNL

	_, err = f.Write(cleaned)
	return err
}

// cleanLogData removes ANSI escape codes and control characters from log data
func cleanLogData(data []byte) []byte {
	// Replace cursor positioning with newlines (BIOS uses these instead of \n)
	data = cursorPosRegex.ReplaceAll(data, []byte("\n"))

	// Remove other ANSI escape sequences
	data = ansiRegex.ReplaceAll(data, nil)

	// Remove orphaned ANSI fragments (from previously split sequences)
	data = orphanedAnsiRegex.ReplaceAll(data, nil)
	data = orphanedAnsiLineRegex.ReplaceAll(data, nil)

	// Handle carriage returns: simulate terminal overwrite behavior.
	// First normalize \r\n line endings to \n (standard SOL line terminator),
	// then within each line, content after \r replaces content before it.
	// e.g. "foo\rbar" → "bar" (BIOS spinner frames)
	if bytes.ContainsRune(data, '\r') {
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		crLines := bytes.Split(data, []byte("\n"))
		for i, line := range crLines {
			if idx := bytes.LastIndexByte(line, '\r'); idx >= 0 {
				crLines[i] = line[idx+1:]
			}
		}
		data = bytes.Join(crLines, []byte("\n"))
	}

	// Remove control characters except newline and tab
	result := make([]byte, 0, len(data))
	for _, c := range data {
		if c == '\n' || c == '\t' || (c >= 32 && c < 127) {
			result = append(result, c)
		}
	}

	// Trim trailing whitespace from each line
	lines := bytes.Split(result, []byte("\n"))
	result = result[:0]
	for i, line := range lines {
		line = bytes.TrimRight(line, " \t")
		if i > 0 {
			result = append(result, '\n')
		}
		result = append(result, line...)
	}

	// Collapse runs of blank lines into a single blank line
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

	// Record rotation time and reset dedup state
	w.lastRotation[serverName] = time.Now()
	delete(w.lastLine, serverName)
	delete(w.trailingNL, serverName)
	delete(w.repeats, serverName)

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

	type logEntry struct {
		name    string
		modTime time.Time
	}
	var logs []logEntry
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" && entry.Name() != "current.log" {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			logs = append(logs, logEntry{name: entry.Name(), modTime: info.ModTime()})
		}
	}

	// Sort newest first by modification time
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].modTime.After(logs[j].modTime)
	})

	names := make([]string, len(logs))
	for i, l := range logs {
		names[i] = l.name
	}

	return names, nil
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

func (w *Writer) SyncFile(serverName string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if f, exists := w.files[serverName]; exists {
		f.Sync()
	}
}

func (w *Writer) GetCurrentLogTarget(serverName string) (filename, fullPath string, err error) {
	symlinkPath := filepath.Join(w.basePath, serverName, "current.log")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		return "", "", err
	}
	return target, filepath.Join(w.basePath, serverName, target), nil
}

func (w *Writer) ListServerDirs() []string {
	entries, err := os.ReadDir(w.basePath)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
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

// GetDupCount returns the current duplicate count for a server's repeat tracker.
func (w *Writer) GetDupCount(serverName string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if rt, ok := w.repeats[serverName]; ok {
		return rt.DupCount() / max(rt.blockLen, 1)
	}
	return 0
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
