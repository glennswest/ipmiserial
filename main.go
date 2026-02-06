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

// Version info - increment based on change magnitude:
// Major (x.0.0): Breaking changes, major rewrites
// Minor (0.y.0): New features, significant enhancements
// Patch (0.0.z): Bug fixes, minor improvements
const Version = "1.3.0"

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

	log.Infof("Starting Console Server v%s", Version)
	log.Infof("  Netman: %s", cfg.Discovery.NetmanURL)
	log.Infof("  IP Range: 192.168.11.%d-%d", cfg.Discovery.IPRangeMin, cfg.Discovery.IPRangeMax)
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

	scanner := discovery.NewScanner(cfg.Discovery.NetmanURL, cfg.Discovery.IPRangeMin, cfg.Discovery.IPRangeMax)

	// Add any statically configured servers (optional override)
	for _, s := range cfg.Servers {
		scanner.AddServer(s.Name, s.Host)
	}

	scanner.OnChange(func(servers map[string]*discovery.Server) {
		// Start SOL sessions for all discovered servers
		for name, s := range servers {
			if solManager.GetSession(name) == nil {
				log.Infof("Starting SOL session for %s (%s)", name, s.IP)
				solManager.StartSession(name, s.IP)
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
