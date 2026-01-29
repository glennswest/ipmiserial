package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	IPMI            IPMIConfig            `yaml:"ipmi"`
	Servers         []ServerEntry         `yaml:"servers"`
	Discovery       DiscoveryConfig       `yaml:"discovery"`
	RebootDetection RebootDetectionConfig `yaml:"reboot_detection"`
	Logs            LogsConfig            `yaml:"logs"`
	Server          ServerConfig          `yaml:"server"`
}

type ServerEntry struct {
	Name string `yaml:"name"`
	Host string `yaml:"host"`
}

type IPMIConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type DiscoveryConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Subnet   string        `yaml:"subnet"`
	Interval time.Duration `yaml:"interval"`
}

type RebootDetectionConfig struct {
	SOLPatterns         []string      `yaml:"sol_patterns"`
	ChassisPollInterval time.Duration `yaml:"chassis_poll_interval"`
}

type LogsConfig struct {
	Path          string `yaml:"path"`
	RetentionDays int    `yaml:"retention_days"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Discovery: DiscoveryConfig{
			Subnet:   "192.168.11.0/24",
			Interval: 5 * time.Minute,
		},
		RebootDetection: RebootDetectionConfig{
			SOLPatterns:         []string{"POST", "BIOS", "Booting"},
			ChassisPollInterval: 30 * time.Second,
		},
		Logs: LogsConfig{
			Path:          "/data/logs",
			RetentionDays: 30,
		},
		Server: ServerConfig{
			Port: 8080,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
