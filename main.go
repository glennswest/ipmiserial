package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"ipmiserial/config"
	"ipmiserial/discovery"
	"ipmiserial/logs"
	"ipmiserial/server"
	"ipmiserial/sol"
)

// Version info - increment based on change magnitude:
// Major (x.0.0): Breaking changes, major rewrites
// Minor (0.y.0): New features, significant enhancements
// Patch (0.0.z): Bug fixes, minor improvements
var Version = "2.3.0"

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Log to file instead of stdout to avoid MikroTik container pipe saturation
	os.MkdirAll(cfg.Logs.Path, 0755)
	logFile, err := os.OpenFile(cfg.Logs.Path+"/ipmiserial.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		log.SetOutput(logFile)
	}

	log.Infof("Starting Console Server v%s", Version)
	log.Infof("  BMH API: %s (namespace: %s)", cfg.Discovery.BMHURL, cfg.Discovery.Namespace)
	log.Infof("  Log path: %s", cfg.Logs.Path)
	log.Infof("  Web port: %d", cfg.Server.Port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Info("Shutting down...")
		cancel()
	}()

	// Initialize components
	logWriter := logs.NewWriter(cfg.Logs.Path, cfg.Logs.RetentionDays)
	defer logWriter.Close()

	rebootDetector := sol.NewRebootDetector(cfg.RebootDetection.SOLPatterns)

	solManager := sol.NewManager(cfg.IPMI.Username, cfg.IPMI.Password, logWriter, rebootDetector, cfg.Logs.Path)

	dataDir := filepath.Dir(cfg.Logs.Path) // e.g. /var/lib/data from /var/lib/data/logs
	scanner := discovery.NewScanner(cfg.Discovery.BMHURL, cfg.Discovery.Namespace, dataDir)

	// Add any statically configured servers (optional override)
	for _, s := range cfg.Servers {
		scanner.AddServer(s.Name, s.Host)
	}

	scanner.OnChange(func(servers map[string]*discovery.Server) {
		for name, s := range servers {
			session := solManager.GetSession(name)
			if s.Online && session == nil {
				log.Infof("Starting SOL session for %s (%s) user=%s", name, s.IP, s.Username)
				solManager.StartSession(name, s.IP, s.Username, s.Password)
			} else if !s.Online && session != nil {
				log.Infof("Stopping SOL session for %s (server offline)", name)
				solManager.StopSession(name)
			} else if s.Online && session != nil {
				// Detect credential changes and restart session
				if session.Username != s.Username || session.Password != s.Password {
					log.Infof("Credentials changed for %s, restarting SOL session", name)
					solManager.StopSession(name)
					solManager.StartSession(name, s.IP, s.Username, s.Password)
				}
			}
		}
	})

	srv := server.New(cfg.Server.Port, scanner, solManager, logWriter, cfg.Servers, Version)

	// Start log cleanup routine
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logWriter.Cleanup()
			}
		}
	}()

	// Run components
	go scanner.Run(ctx)

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
