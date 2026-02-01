package server

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Check if session exists (without subscribing for now)
	session := s.solManager.GetSession(name)
	if session == nil {
		http.Error(w, "Server not found or no active session", http.StatusNotFound)
		return
	}

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

	// Subscribe to live updates
	dataCh, unsubscribe := s.solManager.Subscribe(name)
	if dataCh == nil {
		http.Error(w, "Failed to subscribe", http.StatusInternalServerError)
		return
	}
	defer unsubscribe()

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", name)
	flusher.Flush()

	// Stream data to client
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-dataCh:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: data\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}
