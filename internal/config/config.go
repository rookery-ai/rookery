package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Data     DataConfig     `yaml:"data"`
	Coder    CoderConfig    `yaml:"coder"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
	Session  SessionConfig  `yaml:"session"`
}

type ServerConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	SessionKey   string `yaml:"session_key"` // hex-encoded 32-byte key
	TemplatesDir string `yaml:"templates_dir"`
	StaticDir    string `yaml:"static_dir"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type DataConfig struct {
	Dir string `yaml:"dir"` // root for agents/, memory/, sessions/, claude-homes/
}

type CoderConfig struct {
	ClaudeBin string        `yaml:"claude_bin"` // path to claude binary
	Timeout   time.Duration `yaml:"timeout"`
}

type SandboxConfig struct {
	FirejailBin   string        `yaml:"firejail_bin"`
	PythonBin     string        `yaml:"python_bin"`
	DefaultTimeout time.Duration `yaml:"default_timeout"`
	DefaultMemoryMB int          `yaml:"default_memory_mb"`
}

type SessionConfig struct {
	InactivityTimeout time.Duration `yaml:"inactivity_timeout"`
}

func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse config: %w", err)
			}
		}
	}

	applyEnv(cfg)
	return cfg, nil
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".simple-agents-v2")
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Database: DatabaseConfig{
			Path: filepath.Join(dataDir, "simple-agents.db"),
		},
		Data: DataConfig{
			Dir: dataDir,
		},
		Coder: CoderConfig{
			ClaudeBin: "claude",
			Timeout:   20 * time.Minute,
		},
		Sandbox: SandboxConfig{
			FirejailBin:     "firejail",
			PythonBin:       "python3",
			DefaultTimeout:  5 * time.Minute,
			DefaultMemoryMB: 256,
		},
		Session: SessionConfig{
			InactivityTimeout: 30 * time.Minute,
		},
	}
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("SA_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Server.Port)
	}
	if v := os.Getenv("SA_DATA_DIR"); v != "" {
		cfg.Data.Dir = v
		cfg.Database.Path = filepath.Join(v, "simple-agents.db")
	}
	if v := os.Getenv("SA_SESSION_KEY"); v != "" {
		cfg.Server.SessionKey = v
	}
	if v := os.Getenv("SA_CLAUDE_BIN"); v != "" {
		cfg.Coder.ClaudeBin = v
	}
	if v := os.Getenv("SA_TEMPLATES_DIR"); v != "" {
		cfg.Server.TemplatesDir = v
	}
	if v := os.Getenv("SA_STATIC_DIR"); v != "" {
		cfg.Server.StaticDir = v
	}
}
