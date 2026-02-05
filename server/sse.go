package server

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleStream ENTRY from %s for path %s", r.RemoteAddr, r.URL.Path)
	vars := mux.Vars(r)
	name := vars["name"]
	log.Infof("handleStream called for %s from %s", name, r.RemoteAddr)

	// Check if session exists (without subscribing for now)
	session := s.solManager.GetSession(name)
	if session == nil {
		log.Warnf("handleStream: session not found for %s", name)
		http.Error(w, "Server not found or no active session", http.StatusNotFound)
		return
	}
	log.Infof("handleStream: found session for %s, connected=%v", name, session.Connected)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Error("handleStream: flusher not supported")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to live updates
	log.Infof("handleStream: subscribing to %s", name)
	dataCh, unsubscribe := s.solManager.Subscribe(name)
	if dataCh == nil {
		log.Errorf("handleStream: subscribe returned nil for %s", name)
		http.Error(w, "Failed to subscribe", http.StatusInternalServerError)
		return
	}
	defer unsubscribe()
	log.Infof("handleStream: subscribed to %s, sending connected event", name)

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", name)
	flusher.Flush()
	log.Infof("handleStream: flushed connected event for %s", name)

	// Stream data to client
	for {
		select {
		case <-r.Context().Done():
			log.Infof("handleStream: context done for %s", name)
			return
		case data, ok := <-dataCh:
			if !ok {
				log.Infof("handleStream: channel closed for %s", name)
				return
			}
			log.Infof("handleStream: sending %d bytes to %s", len(data), name)
			encoded := base64.StdEncoding.EncodeToString(data)
			fmt.Fprintf(w, "data: %s\n\n", encoded)
			flusher.Flush()
		}
	}
}
