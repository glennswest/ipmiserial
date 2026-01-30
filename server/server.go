package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	"console-server/discovery"
	"console-server/logs"
	"console-server/sol"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	port       int
	scanner    *discovery.Scanner
	solManager *sol.Manager
	logWriter  *logs.Writer
	router     *mux.Router
	httpServer *http.Server
}

func New(port int, scanner *discovery.Scanner, solManager *sol.Manager, logWriter *logs.Writer) *Server {
	s := &Server{
		port:       port,
		scanner:    scanner,
		solManager: solManager,
		logWriter:  logWriter,
		router:     mux.NewRouter(),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/servers", s.handleListServers).Methods("GET")
	api.HandleFunc("/servers/{name}/stream", s.handleStream).Methods("GET")
	api.HandleFunc("/servers/{name}/logs", s.handleListLogs).Methods("GET")
	api.HandleFunc("/servers/{name}/logs/{filename}", s.handleGetLog).Methods("GET")
	api.HandleFunc("/servers/{name}/status", s.handleStatus).Methods("GET")
	api.HandleFunc("/servers/{name}/logs/clear", s.handleClearLogs).Methods("POST")
	api.HandleFunc("/logs/clear", s.handleClearAllLogs).Methods("POST")
	api.HandleFunc("/servers/{name}/analytics", s.handleAnalytics).Methods("GET")
	api.HandleFunc("/analytics", s.handleAllAnalytics).Methods("GET")

	// HTMX HTML fragment routes
	htmx := s.router.PathPrefix("/htmx").Subrouter()
	htmx.HandleFunc("/servers/{name}/analytics", s.handleAnalyticsHTML).Methods("GET")
	htmx.HandleFunc("/servers/{name}/logs", s.handleLogListHTML).Methods("GET")
	htmx.HandleFunc("/servers/{name}/logs/{filename}", s.handleLogContentHTML).Methods("GET")

	// Serve embedded web files
	webContent, _ := fs.Sub(webFS, "web")
	s.router.PathPrefix("/").Handler(http.FileServer(http.FS(webContent)))
}

func (s *Server) Run(ctx context.Context) error {
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: s.router,
	}

	go func() {
		<-ctx.Done()
		s.httpServer.Shutdown(context.Background())
	}()

	log.Infof("Starting web server on port %d", s.port)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
