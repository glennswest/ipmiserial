package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
)

var clearScreenSeq = []byte("\x1b[2J")

// sseWrite writes an SSE frame and flushes. Returns false if the connection is dead.
func sseWrite(w http.ResponseWriter, rc *http.ResponseController, format string, args ...interface{}) bool {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return false
	}
	if err := rc.Flush(); err != nil {
		return false
	}
	return true
}

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

	rc := http.NewResponseController(w)

	if !sseWrite(w, rc, "event: connected\ndata: %s\n\n", name) {
		return
	}

	// Catchup: prefer raw screen buffer (preserves ANSI/cursor positioning
	// for correct terminal state). Fall back to cleaned log for servers
	// without an active SOL session.
	screenBuf := s.solManager.GetScreenBuffer(name)
	if len(screenBuf) > 0 {
		clearAndBuf := append([]byte("\x1b[2J\x1b[H"), screenBuf...)
		encoded := base64.StdEncoding.EncodeToString(clearAndBuf)
		if !sseWrite(w, rc, "data: %s\n\n", encoded) {
			return
		}
	} else if _, curPath, err := s.logWriter.GetCurrentLogTarget(name); err == nil && curPath != "" {
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
					if !sseWrite(w, rc, "data: %s\n\n", encoded) {
						f.Close()
						return
					}
				}
			}
			f.Close()
		}
	}

	// Subscribe to raw SOL broadcast and notification events
	ch := s.solManager.Subscribe(name)
	defer s.solManager.Unsubscribe(name, ch)
	notifyCh := s.solManager.SubscribeNotify(name)
	defer s.solManager.UnsubscribeNotify(name, notifyCh)

	// Heartbeat keeps the SSE connection alive when no SOL data is flowing
	// (e.g. server sitting at login prompt). Uses a named event so proxies
	// see real SSE data frames, not just comments they might ignore.
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !sseWrite(w, rc, "event: heartbeat\ndata: \n\n") {
				return
			}
		case event := <-notifyCh:
			if !sseWrite(w, rc, "event: %s\ndata: %s\n\n", event.Name, event.Data) {
				return
			}
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
			if !sseWrite(w, rc, "data: %s\n\n", encoded) {
				return
			}
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
