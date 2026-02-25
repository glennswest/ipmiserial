package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gorilla/mux"
)

var clearScreenSeq = []byte("\x1b[2J")

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

	// Skip catchup and clear screen on reconnect (terminal already has content).
	// Only send catchup on initial connection (?catchup=0 means skip).
	if r.URL.Query().Get("catchup") != "0" {
		// Send catchup from log file (last ~4KB of cleaned text)
		if _, curPath, err := s.logWriter.GetCurrentLogTarget(name); err == nil && curPath != "" {
			if f, err := os.Open(curPath); err == nil {
				if info, _ := f.Stat(); info != nil {
					size := info.Size()
					const catchupSize = 4096
					var offset int64
					if size > catchupSize {
						f.Seek(size-catchupSize, io.SeekStart)
						offset = size - catchupSize
					}
					buf := make([]byte, size-offset)
					n, _ := f.Read(buf)
					if n > 0 {
						encoded := base64.StdEncoding.EncodeToString(buf[:n])
						fmt.Fprintf(w, "data: %s\n\n", encoded)
						flusher.Flush()
					}
				}
				f.Close()
			}
		}

		// Clear screen before raw stream so BIOS cursor positioning works
		// against a clean terminal state (catchup text stays in scrollback)
		clearScreen := base64.StdEncoding.EncodeToString([]byte("\x1b[2J\x1b[H"))
		fmt.Fprintf(w, "data: %s\n\n", clearScreen)
		flusher.Flush()
	}

	// Subscribe to raw SOL broadcast
	ch := s.solManager.Subscribe(name)
	defer s.solManager.Unsubscribe(name, ch)

	lastDupCount := 0

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			// BIOS redraws screen by positioning to row 1 without clearing.
			// Inject clear screen so old content doesn't linger in xterm.js.
			if containsRow1Cursor(data) {
				data = append(clearScreenSeq, data...)
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			fmt.Fprintf(w, "data: %s\n\n", encoded)

			// Send dedup count if it changed
			dupCount := s.logWriter.GetDupCount(name)
			if dupCount != lastDupCount {
				if dupCount > 0 {
					fmt.Fprintf(w, "event: dedup\ndata: %d\n\n", dupCount)
				} else if lastDupCount > 0 {
					// Dedup ended
					fmt.Fprintf(w, "event: dedup\ndata: 0\n\n")
				}
				lastDupCount = dupCount
			}

			flusher.Flush()
		}
	}
}

// containsRow1Cursor detects BIOS screen redraws by checking for cursor
// positioning to row 1 in the zero-padded format that Intel PXE BIOS uses.
// Only matches \x1b[01;00H — generic sequences like \x1b[H or \x1b[1;1H
// are used by normal terminal applications (systemd, Fedora installer, etc.)
// and must NOT trigger screen clearing.
func containsRow1Cursor(data []byte) bool {
	return bytes.Contains(data, []byte("\x1b[01;00H"))
}
