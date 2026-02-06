package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	log.Infof("handleStream: %s from %s", name, r.RemoteAddr)

	// Validate server exists via scanner (works even during SOL reconnect)
	knownServers := s.scanner.GetServers()
	if _, ok := knownServers[name]; !ok {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", name)
	flusher.Flush()

	// Resolve current log file and send catchup (~last 4KB)
	curTarget, curPath, err := s.logWriter.GetCurrentLogTarget(name)
	if err != nil {
		log.Warnf("handleStream: no current log for %s: %v", name, err)
		// No log yet — just start polling
		curTarget = ""
		curPath = ""
	}

	var offset int64
	if curPath != "" {
		s.logWriter.SyncFile(name)
		offset = sendCatchup(w, flusher, curPath)
	}

	// Poll loop: 200ms interval
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			log.Infof("handleStream: client disconnected for %s", name)
			return
		case <-ticker.C:
			// Sync buffered writes to disk
			s.logWriter.SyncFile(name)

			// Check for log rotation (symlink target changed)
			newTarget, newPath, err := s.logWriter.GetCurrentLogTarget(name)
			if err != nil {
				continue
			}

			if curTarget == "" {
				// First time seeing a log file
				curTarget = newTarget
				curPath = newPath
				offset = 0
			} else if newTarget != curTarget {
				// Rotation happened — reset to new file
				log.Infof("handleStream: log rotated for %s: %s -> %s", name, curTarget, newTarget)
				curTarget = newTarget
				curPath = newPath
				offset = 0
			}

			// Stat for size change
			info, err := os.Stat(curPath)
			if err != nil {
				continue
			}
			size := info.Size()
			if size <= offset {
				continue
			}

			// Read new bytes
			f, err := os.Open(curPath)
			if err != nil {
				continue
			}
			f.Seek(offset, io.SeekStart)
			buf := make([]byte, size-offset)
			n, err := f.Read(buf)
			f.Close()
			if n > 0 {
				encoded := base64.StdEncoding.EncodeToString(buf[:n])
				fmt.Fprintf(w, "data: %s\n\n", encoded)
				flusher.Flush()
				offset += int64(n)
			}
		}
	}
}

// sendCatchup reads the last ~4KB of the log file and sends it as a catchup SSE event.
// Returns the file offset after reading (i.e. end of file).
func sendCatchup(w http.ResponseWriter, flusher http.Flusher, path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0
	}
	size := info.Size()

	const catchupSize = 4096
	var start int64
	if size > catchupSize {
		start = size - catchupSize
	}

	f.Seek(start, io.SeekStart)
	buf := make([]byte, size-start)
	n, _ := f.Read(buf)
	if n > 0 {
		encoded := base64.StdEncoding.EncodeToString(buf[:n])
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		flusher.Flush()
	}

	return size
}
