package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	"console-server/config"
	"console-server/discovery"
	"console-server/logs"
	"console-server/sol"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	port       int
	version    string
	scanner    *discovery.Scanner
	solManager *sol.Manager
	logWriter  *logs.Writer
	router     *mux.Router
	httpServer *http.Server
	macLookup  map[string]string // MAC -> server name
}

func New(port int, scanner *discovery.Scanner, solManager *sol.Manager, logWriter *logs.Writer, servers []config.ServerEntry, version string) *Server {
	s := &Server{
		port:       port,
		version:    version,
		scanner:    scanner,
		solManager: solManager,
		logWriter:  logWriter,
		router:     mux.NewRouter(),
		macLookup:  make(map[string]string),
	}

	// Build MAC lookup table
	for _, srv := range servers {
		for _, mac := range srv.MACs {
			// Normalize MAC: lowercase, no separators
			normalized := normalizeMac(mac)
			s.macLookup[normalized] = srv.Name
			log.Debugf("MAC lookup: %s -> %s", normalized, srv.Name)
		}
	}
	if len(s.macLookup) > 0 {
		log.Infof("Loaded %d MAC address mappings", len(s.macLookup))
	}

	s.setupRoutes()
	return s
}

// normalizeMac converts MAC to lowercase without separators
func normalizeMac(mac string) string {
	mac = strings.ToLower(mac)
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, "-", "")
	mac = strings.ReplaceAll(mac, ".", "")
	return mac
}

func (s *Server) setupRoutes() {
	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/version", s.handleVersion).Methods("GET")
	api.HandleFunc("/servers", s.handleListServers).Methods("GET")
	api.HandleFunc("/servers/{name}/stream", s.handleStream).Methods("GET")
	log.Info("Registered route: /api/servers/{name}/stream")
	api.HandleFunc("/servers/{name}/logs", s.handleListLogs).Methods("GET")
	api.HandleFunc("/servers/{name}/logs/{filename}", s.handleGetLog).Methods("GET")
	api.HandleFunc("/servers/{name}/logs/{filename}/info", s.handleLogInfo).Methods("GET")
	api.HandleFunc("/servers/{name}/status", s.handleStatus).Methods("GET")
	api.HandleFunc("/servers/{name}/logs/clear", s.handleClearLogs).Methods("POST")
	api.HandleFunc("/servers/{name}/logs/rotate", s.handleRotateLogs).Methods("POST")
	api.HandleFunc("/logs/clear", s.handleClearAllLogs).Methods("POST")
	api.HandleFunc("/servers/{name}/analytics", s.handleAnalytics).Methods("GET")
	api.HandleFunc("/analytics", s.handleAllAnalytics).Methods("GET")
	api.HandleFunc("/lookup/mac/{mac}", s.handleMacLookup).Methods("GET")
	api.HandleFunc("/refresh", s.handleRefresh).Methods("POST")

	// HTMX HTML fragment routes
	htmx := s.router.PathPrefix("/htmx").Subrouter()
	htmx.HandleFunc("/servers/{name}/analytics", s.handleAnalyticsHTML).Methods("GET")
	htmx.HandleFunc("/servers/{name}/logs", s.handleLogListHTML).Methods("GET")
	htmx.HandleFunc("/servers/{name}/logs/{filename}", s.handleLogContentHTML).Methods("GET")

	// Serve embedded web files
	webContent, _ := fs.Sub(webFS, "web")
	s.router.PathPrefix("/").Handler(http.FileServer(http.FS(webContent)))
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Infof("MIDDLEWARE: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Run(ctx context.Context) error {
	s.router.Use(loggingMiddleware)
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: s.router,
	}

	go func() {
		<-ctx.Done()
		log.Info("Context done, shutting down HTTP server")
		s.httpServer.Shutdown(context.Background())
	}()

	log.Infof("Starting web server on port %d", s.port)
	log.Infof("Routes configured: /api/version, /api/servers, etc.")
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		log.Info("HTTP server closed cleanly")
		return nil
	}
	log.Errorf("HTTP server error: %v", err)
	return err
}
