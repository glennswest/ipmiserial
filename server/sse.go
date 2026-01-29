package server

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Subscribe to the session's output
	clientChan, unsubscribe := s.solManager.Subscribe(name)
	if clientChan == nil {
		http.Error(w, "Server not found or no active session", http.StatusNotFound)
		return
	}
	defer unsubscribe()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", name)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-clientChan:
			if !ok {
				return
			}
			// Base64 encode binary data to safely transmit over SSE
			encoded := base64.StdEncoding.EncodeToString(data)
			fmt.Fprintf(w, "data: %s\n\n", encoded)
			flusher.Flush()
		}
	}
}
