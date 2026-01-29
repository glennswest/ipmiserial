package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/gorilla/mux"
)

type ServerInfo struct {
	Name      string `json:"name"`
	IP        string `json:"ip"`
	Online    bool   `json:"online"`
	Connected bool   `json:"connected"`
	LastError string `json:"lastError,omitempty"`
}

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers := s.scanner.GetServers()
	sessions := s.solManager.GetSessions()

	result := make([]ServerInfo, 0, len(servers))
	for name, srv := range servers {
		info := ServerInfo{
			Name:   name,
			IP:     srv.IP,
			Online: srv.Online,
		}

		if session, exists := sessions[name]; exists {
			info.Connected = session.Connected
			info.LastError = session.LastError
		}

		result = append(result, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	logs, err := s.logWriter.ListLogs(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	filename := vars["filename"]

	path := s.logWriter.GetLogPath(name, filename)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Log not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	servers := s.scanner.GetServers()
	srv, exists := servers[name]
	if !exists {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}

	session := s.solManager.GetSession(name)

	info := ServerInfo{
		Name:   name,
		IP:     srv.IP,
		Online: srv.Online,
	}

	if session != nil {
		info.Connected = session.Connected
		info.LastError = session.LastError
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}
