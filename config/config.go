package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Interval   int       `yaml:"interval"`
	RingBuffer int       `yaml:"ring_buffer"`
	Socket     string    `yaml:"socket"`
	ProcRoot   string    `yaml:"proc_root"` // default: /proc, use /host/proc in Docker
	Watchers   []Watcher `yaml:"watchers"`
}

type Watcher struct {
	Name       string     `yaml:"name"`
	Mask       string     `yaml:"mask"`
	Thresholds Thresholds `yaml:"thresholds"`
	OnAnomaly  OnAnomaly  `yaml:"on_anomaly"`
}

type Thresholds struct {
	RSSAbsoluteMB   int64  `yaml:"rss_absolute_mb"`
	GrowthSnapshots int    `yaml:"growth_snapshots"`
	PoolRSSTotalMB  int64  `yaml:"pool_rss_total_mb"`
	PoolKillStrategy string `yaml:"pool_kill_strategy"` // "heaviest" or "all"
}

type OnAnomaly struct {
	Kill     bool   `yaml:"kill"`
	DumpPath string `yaml:"dump_path"`
	Webhook  string `yaml:"webhook"`
	Exec     string `yaml:"exec"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Interval:   5,
		RingBuffer: 60,
		Socket:     "/var/run/watchdog.sock",
		ProcRoot:   "/proc",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
