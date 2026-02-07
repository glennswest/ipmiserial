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

	// Validate server exists — check log target first (no locks), fall back to scanner
	_, _, logErr := s.logWriter.GetCurrentLogTarget(name)
	if logErr != nil {
		knownServers := s.scanner.GetServers()
		if _, ok := knownServers[name]; !ok {
			http.Error(w, "Server not found", http.StatusNotFound)
			return
		}
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

	// Resolve current log file, open for reading, send catchup
	curTarget, curPath, _ := s.logWriter.GetCurrentLogTarget(name)

	var readFile *os.File
	var offset int64
	if curPath != "" {
		var err error
		readFile, err = os.Open(curPath)
		if err == nil {
			// Send last ~4KB as catchup
			if info, _ := readFile.Stat(); info != nil {
				size := info.Size()
				const catchupSize = 4096
				if size > catchupSize {
					readFile.Seek(size-catchupSize, io.SeekStart)
					offset = size - catchupSize
				}
				buf := make([]byte, size-offset)
				n, _ := readFile.Read(buf)
				if n > 0 {
					encoded := base64.StdEncoding.EncodeToString(buf[:n])
					fmt.Fprintf(w, "data: %s\n\n", encoded)
					flusher.Flush()
					offset += int64(n)
				}
			}
		}
	}
	defer func() {
		if readFile != nil {
			readFile.Close()
		}
	}()

	// Fast poll for new data, slow poll for rotation
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	rotationCheck := time.NewTicker(3 * time.Second)
	defer rotationCheck.Stop()

	buf := make([]byte, 16384)

	for {
		select {
		case <-r.Context().Done():
			return

		case <-rotationCheck.C:
			newTarget, newPath, err := s.logWriter.GetCurrentLogTarget(name)
			if err != nil {
				continue
			}
			if curTarget == "" || newTarget != curTarget {
				if curTarget != "" {
					log.Infof("handleStream: log rotated for %s: %s -> %s", name, curTarget, newTarget)
					fmt.Fprintf(w, "event: logchange\ndata: %s\n\n", newTarget)
					flusher.Flush()
				}
				curTarget = newTarget
				curPath = newPath
				if readFile != nil {
					readFile.Close()
				}
				readFile, _ = os.Open(curPath)
				offset = 0
			}

		case <-ticker.C:
			if readFile == nil {
				// Try to open if we didn't have a file yet
				if curPath == "" {
					curTarget, curPath, _ = s.logWriter.GetCurrentLogTarget(name)
				}
				if curPath != "" {
					readFile, _ = os.Open(curPath)
				}
				continue
			}
			n, _ := readFile.Read(buf)
			if n > 0 {
				encoded := base64.StdEncoding.EncodeToString(buf[:n])
				fmt.Fprintf(w, "data: %s\n\n", encoded)
				flusher.Flush()
				offset += int64(n)
			}
		}
	}
}
