package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"console-server/config"
	"console-server/discovery"
	"console-server/logs"
	"console-server/server"
	"console-server/sol"
)

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

	log.Infof("Starting Console Server")
	log.Infof("  Servers: %d configured", len(cfg.Servers))
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

	scanner := discovery.NewScanner()

	// Add configured servers
	for _, s := range cfg.Servers {
		scanner.AddServer(s.Name, s.Host)
	}

	scanner.OnChange(func(servers map[string]*discovery.Server) {
		// Start SOL sessions for new servers
		for name, srv := range servers {
			if session := solManager.GetSession(name); session == nil {
				solManager.StartSession(name, srv.IP)
			}
		}
	})

	srv := server.New(cfg.Server.Port, scanner, solManager, logWriter)

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
