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
	Name string   `yaml:"name"`
	Host string   `yaml:"host"`
	MACs []string `yaml:"macs"` // List of MAC addresses for this server
}

type IPMIConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type DiscoveryConfig struct {
	BMHURL    string `yaml:"bmh_url"`
	Namespace string `yaml:"namespace"` // filter BMH by namespace (e.g. "g11")
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
			BMHURL: "http://192.168.200.2:8082",
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
