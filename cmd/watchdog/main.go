package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yangusik/php-watchdog/config"
	"github.com/yangusik/php-watchdog/internal/proc"
	"github.com/yangusik/php-watchdog/internal/socket"
	"github.com/yangusik/php-watchdog/internal/watcher"
)

func main() {
	configPath := flag.String("config", "watchdog.yml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if err := validate(cfg); err != nil {
		log.Fatalf("startup check failed: %v", err)
	}

	jobs := socket.NewStore()

	if cfg.Socket != "" {
		srv := socket.NewServer(cfg.Socket, jobs)
		go func() {
			if err := srv.Listen(); err != nil {
				log.Printf("socket server error: %v", err)
			}
		}()
	}

	stop := make(chan struct{})

	reader := proc.New(cfg.ProcRoot)
	w := watcher.New(cfg, reader, jobs)
	go w.Run(stop)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	log.Println("shutting down...")
	close(stop)
}

// validate checks config and environment before starting.
func validate(cfg *config.Config) error {
	if len(cfg.Watchers) == 0 {
		return fmt.Errorf("no watchers configured — add at least one [[watchers]] entry")
	}

	for i, wc := range cfg.Watchers {
		if wc.Mask == "" {
			return fmt.Errorf("watcher[%d]: mask is required", i)
		}
		if wc.Thresholds.RSSAbsoluteMB == 0 && wc.Thresholds.GrowthSnapshots == 0 {
			return fmt.Errorf("watcher[%d] %q: at least one threshold must be set", i, wc.Name)
		}

		if wc.Thresholds.PoolRSSTotalMB > 0 {
			strategy := wc.Thresholds.PoolKillStrategy
			if strategy != "heaviest" && strategy != "all" {
				return fmt.Errorf("watcher[%d] %q: pool_kill_strategy is required when pool_rss_total_mb is set (use \"heaviest\" or \"all\")",
					i, wc.Name)
			}
		}

		if wc.OnAnomaly.DumpPath != "" {
			if err := os.MkdirAll(wc.OnAnomaly.DumpPath, 0755); err != nil {
				return fmt.Errorf("watcher[%d] %q: cannot create dump_path %q: %v",
					i, wc.Name, wc.OnAnomaly.DumpPath, err)
			}
			// check write permission by creating a temp file
			tmp, err := os.CreateTemp(wc.OnAnomaly.DumpPath, ".watchdog-check-*")
			if err != nil {
				return fmt.Errorf("watcher[%d] %q: no write permission on dump_path %q (running as %s uid=%d): %v",
					i, wc.Name, wc.OnAnomaly.DumpPath, currentUser(), os.Getuid(), err)
			}
			tmp.Close()
			os.Remove(tmp.Name())
		}

		if wc.OnAnomaly.Exec != "" {
			if _, err := os.Stat(wc.OnAnomaly.Exec); err != nil {
				return fmt.Errorf("watcher[%d] %q: exec binary not found: %q", i, wc.Name, wc.OnAnomaly.Exec)
			}
		}
	}

	// check proc root is accessible
	if _, err := os.Stat(cfg.ProcRoot + "/1"); err != nil {
		return fmt.Errorf("proc_root %q is not accessible: %v", cfg.ProcRoot, err)
	}

	log.Printf("running as user %s (uid=%d)", currentUser(), os.Getuid())
	return nil
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}
