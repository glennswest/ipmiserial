package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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

func (s *Server) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if err := s.logWriter.ClearLogs(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleClearAllLogs(w http.ResponseWriter, r *http.Request) {
	if err := s.logWriter.ClearAllLogs(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleRotateLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Get optional log name from query param or form
	logName := r.URL.Query().Get("name")
	if logName == "" {
		logName = r.FormValue("name")
	}

	newFile, err := s.logWriter.RotateWithName(name, logName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "log rotated",
		"file":    newFile,
	})
}

func (s *Server) handleMacLookup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mac := vars["mac"]

	// Normalize the input MAC
	normalized := normalizeMac(mac)

	serverName, found := s.macLookup[normalized]
	if !found {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"MAC address not found"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"mac":    mac,
		"server": serverName,
	})
}

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	analytics := s.solManager.GetAnalytics(name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(analytics)
}

func (s *Server) handleAllAnalytics(w http.ResponseWriter, r *http.Request) {
	analytics := s.solManager.GetAllAnalytics()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(analytics)
}

// HTML fragment handlers for htmx

func (s *Server) handleAnalyticsHTML(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	data := s.solManager.GetAnalytics(name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Current Status
	var statusClass, statusText, uptimeHTML string
	if data.OSUpSince != nil {
		statusClass = "text-success"
		statusText = "OS Running"
		uptime := formatDuration(time.Since(*data.OSUpSince).Seconds())
		uptimeHTML = fmt.Sprintf(`<p class="mb-1"><strong>Uptime:</strong> %s</p>`, uptime)
	} else if data.CurrentBoot != nil && !data.CurrentBoot.Complete {
		statusClass = "text-warning"
		statusText = "Booting..."
		bootTime := formatDuration(time.Since(data.CurrentBoot.StartTime).Seconds())
		uptimeHTML = fmt.Sprintf(`<p class="mb-1"><strong>Boot time:</strong> %s</p>`, bootTime)
	} else {
		statusClass = "text-muted"
		statusText = "Unknown"
		uptimeHTML = ""
	}

	hostnameHTML := ""
	if data.Hostname != "" {
		hostnameHTML = fmt.Sprintf(`<p class="mb-1"><strong>Hostname:</strong> <span class="text-info">%s</span></p>`, html.EscapeString(data.Hostname))
	}

	osHTML := ""
	if data.CurrentOS != "" {
		osHTML = fmt.Sprintf(`<p class="mb-1"><strong>OS/Image:</strong> <span class="text-info">%s</span></p>`, html.EscapeString(data.CurrentOS))
	}

	// Current Boot
	currentBootHTML := `<p class="text-muted mb-0">No boot data</p>`
	if data.CurrentBoot != nil {
		currentBootHTML = fmt.Sprintf(`<p class="mb-1"><strong>Started:</strong> %s</p>`, data.CurrentBoot.StartTime.Local().Format("Jan 2 15:04:05"))
		if data.CurrentBoot.Complete {
			currentBootHTML += fmt.Sprintf(`<p class="mb-1"><strong>Completed:</strong> %s</p>`, data.CurrentBoot.EndTime.Local().Format("Jan 2 15:04:05"))
			currentBootHTML += fmt.Sprintf(`<p class="mb-1"><strong>Boot Duration:</strong> <span class="text-info">%.1fs</span></p>`, data.CurrentBoot.BootDuration)
		} else {
			currentBootHTML += `<p class="mb-1"><strong>Status:</strong> <span class="text-warning">In Progress...</span></p>`
		}
		if data.CurrentBoot.DetectedOS != "" {
			currentBootHTML += fmt.Sprintf(`<p class="mb-1"><strong>Detected OS:</strong> <span class="text-info">%s</span></p>`, html.EscapeString(data.CurrentBoot.DetectedOS))
		}
	}

	// Network Stats
	networkHTML := `<p class="text-muted mb-0">No network events detected</p>`
	if data.CurrentBoot != nil && len(data.CurrentBoot.NetworkStats) > 0 {
		networkHTML = `<table class="table table-sm mb-0"><thead><tr><th>Interface</th><th>Up</th><th>Down</th></tr></thead><tbody>`
		for _, stat := range data.CurrentBoot.NetworkStats {
			downClass := "text-muted"
			if stat.DownCount > 0 {
				downClass = "text-danger"
			}
			networkHTML += fmt.Sprintf(`<tr><td>%s</td><td class="text-success">%d</td><td class="%s">%d</td></tr>`,
				html.EscapeString(stat.Interface), stat.UpCount, downClass, stat.DownCount)
		}
		networkHTML += `</tbody></table>`
	}

	// Boot History rows
	bootHistoryHTML := ""
	if data.CurrentBoot == nil && len(data.BootHistory) == 0 {
		bootHistoryHTML = `<tr><td colspan="5" class="text-muted text-center">No boot history</td></tr>`
	} else {
		if data.CurrentBoot != nil {
			networkIssues := ""
			for _, s := range data.CurrentBoot.NetworkStats {
				if s.DownCount > 0 {
					if networkIssues != "" {
						networkIssues += ", "
					}
					networkIssues += fmt.Sprintf("%s: %d down", s.Interface, s.DownCount)
				}
			}
			networkCell := `<span class="text-muted">None</span>`
			if networkIssues != "" {
				networkCell = fmt.Sprintf(`<span class="text-danger">%s</span>`, html.EscapeString(networkIssues))
			}
			durationCell := `<span class="text-warning">...</span>`
			if data.CurrentBoot.Complete {
				durationCell = fmt.Sprintf("%.1fs", data.CurrentBoot.BootDuration)
			}
			osCell := `<span class="text-muted">-</span>`
			if data.CurrentBoot.DetectedOS != "" {
				osCell = html.EscapeString(data.CurrentBoot.DetectedOS)
			}
			statusCell := `<span class="text-warning">In Progress</span>`
			if data.CurrentBoot.Complete {
				statusCell = `<span class="text-success">Complete</span>`
			}
			bootHistoryHTML += fmt.Sprintf(`<tr><td>%s <span class="badge bg-info">Current</span></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				data.CurrentBoot.StartTime.Local().Format("Jan 2 15:04:05"), durationCell, osCell, networkCell, statusCell)
		}
		for i := len(data.BootHistory) - 1; i >= 0; i-- {
			b := data.BootHistory[i]
			networkIssues := ""
			for _, s := range b.NetworkStats {
				if s.DownCount > 0 {
					if networkIssues != "" {
						networkIssues += ", "
					}
					networkIssues += fmt.Sprintf("%s: %d down", s.Interface, s.DownCount)
				}
			}
			networkCell := `<span class="text-muted">None</span>`
			if networkIssues != "" {
				networkCell = fmt.Sprintf(`<span class="text-danger">%s</span>`, html.EscapeString(networkIssues))
			}
			durationCell := "-"
			if b.BootDuration > 0 {
				durationCell = fmt.Sprintf("%.1fs", b.BootDuration)
			}
			osCell := `<span class="text-muted">-</span>`
			if b.DetectedOS != "" {
				osCell = html.EscapeString(b.DetectedOS)
			}
			statusCell := `<span class="text-success">Complete</span>`
			if !b.Complete {
				statusCell = `<span class="text-warning">Incomplete</span>`
			}
			bootHistoryHTML += fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				b.StartTime.Local().Format("Jan 2 15:04:05"), durationCell, osCell, networkCell, statusCell)
		}
	}

	fmt.Fprintf(w, `<div class="row">
<div class="col-md-4 mb-3">
<div class="card"><div class="card-header">Current Status</div>
<div class="card-body">
<p class="mb-1"><strong>Status:</strong> <span class="%s">%s</span></p>
%s%s%s
<p class="mb-0"><strong>Total Reboots:</strong> %d</p>
</div></div></div>
<div class="col-md-4 mb-3">
<div class="card"><div class="card-header">Current Boot</div>
<div class="card-body">%s</div></div></div>
<div class="col-md-4 mb-3">
<div class="card"><div class="card-header">Network (Current Boot)</div>
<div class="card-body">%s</div></div></div>
</div>
<div class="card mt-3"><div class="card-header">Boot History</div>
<div class="card-body p-0">
<table class="table table-striped mb-0">
<thead><tr><th>Boot Time</th><th>Duration</th><th>OS/Image</th><th>Network Issues</th><th>Status</th></tr></thead>
<tbody>%s</tbody></table></div></div>`,
		statusClass, statusText, uptimeHTML, hostnameHTML, osHTML, data.TotalReboots,
		currentBootHTML, networkHTML, bootHistoryHTML)
}

func (s *Server) handleLogListHTML(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	logs, err := s.logWriter.ListLogs(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if len(logs) == 0 {
		fmt.Fprint(w, `<div class="list-group-item text-muted small">No logs</div>`)
		return
	}

	for i, log := range logs {
		activeClass := ""
		autoLoad := ""
		if i == 0 {
			activeClass = " active"
			// Auto-load the first (most recent) log
			autoLoad = fmt.Sprintf(` hx-get="/htmx/servers/%s/logs/%s" hx-target="#log-content-%s" hx-trigger="load"`, name, log, name)
		}
		fmt.Fprintf(w, `<a href="#" class="list-group-item list-group-item-action small%s" hx-get="/htmx/servers/%s/logs/%s" hx-target="#log-content-%s" hx-swap="innerHTML" hx-on::before-request="setActiveLog(this)"%s>%s</a>`,
			activeClass, name, log, name, autoLoad, html.EscapeString(log))
	}
}

func (s *Server) handleLogContentHTML(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	filename := vars["filename"]

	// Get pagination params
	lines := 1000 // default
	offset := 0
	if l := r.URL.Query().Get("lines"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			lines = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	path := s.logWriter.GetLogPath(name, filename)

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<div class="text-muted p-3">Log not found</div>`)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	defer file.Close()

	// Read all lines into memory, stripping ANSI escape codes
	var allLines []string
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line != "" {
			allLines = append(allLines, line)
		}
	}

	totalLines := len(allLines)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if totalLines == 0 {
		fmt.Fprint(w, `<div class="text-muted p-3">Empty log file</div>`)
		return
	}

	// Calculate start index (show last N lines by default)
	startIdx := totalLines - lines - offset
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := totalLines - offset
	if endIdx > totalLines {
		endIdx = totalLines
	}
	if endIdx < 0 {
		endIdx = 0
	}

	// Check if there are more lines above
	hasMore := startIdx > 0

	var result string
	if hasMore {
		newOffset := offset + lines
		result = fmt.Sprintf(`<div class="text-center py-2"><button class="btn btn-sm btn-outline-secondary" hx-get="/htmx/servers/%s/logs/%s?lines=%d&amp;offset=%d" hx-target="#log-content-%s" hx-swap="innerHTML">Load older lines (%d more)</button></div>`,
			name, filename, lines, newOffset, name, startIdx)
	}

	result += `<pre class="log-content mb-0">`
	for i := startIdx; i < endIdx; i++ {
		result += html.EscapeString(allLines[i]) + "\n"
	}
	result += `</pre>`

	fmt.Fprint(w, result)
}

func formatDuration(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", int(seconds))
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", int(seconds)/60, int(seconds)%60)
	}
	hours := int(seconds) / 3600
	mins := (int(seconds) % 3600) / 60
	return fmt.Sprintf("%dh %dm", hours, mins)
}
