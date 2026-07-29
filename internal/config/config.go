package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Coder modes. A slim build ships without any CLI coder binary, so the "local"
// coder kind must not be offered or accepted. This is POLICY ("this build does
// not support it"), deliberately distinct from DETECTION ("no coder binary is on
// PATH right now") — see coder.DetectInstalled. Auto-hiding on detection would
// confuse a user who installs a coder afterwards.
const (
	ModeFull = "full"
	ModeSlim = "slim"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Data     DataConfig     `yaml:"data"`
	Coder    CoderConfig    `yaml:"coder"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
	Chat     ChatConfig     `yaml:"chat"`
}

type ServerConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	SessionKey string `yaml:"session_key"` // hex-encoded 32-byte key
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
	Mode      string        `yaml:"mode"` // "full" (default) or "slim"; see ModeFull/ModeSlim
}

// SandboxConfig controls the Landlock filesystem confinement applied to every
// coder subprocess. When Enabled and the kernel supports Landlock, an agent can
// only read/write its own user's files; otherwise it falls back to the detective
// vault guard. Set via SA_SANDBOX (0/false/off disables).
type SandboxConfig struct {
	Enabled         bool          `yaml:"enabled"`
	PythonBin       string        `yaml:"python_bin"`
	DefaultTimeout  time.Duration `yaml:"default_timeout"`
	DefaultMemoryMB int           `yaml:"default_memory_mb"`
}

type ChatConfig struct {
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

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
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
			Mode:      ModeFull,
		},
		Sandbox: SandboxConfig{
			Enabled:         true,
			PythonBin:       "python3",
			DefaultTimeout:  5 * time.Minute,
			DefaultMemoryMB: 256,
		},
		Chat: ChatConfig{
			InactivityTimeout: 30 * time.Minute,
		},
	}
}

func applyEnv(cfg *Config) error {
	if v := os.Getenv("SA_HOST"); v != "" {
		cfg.Server.Host = v // e.g. 127.0.0.1 to bind loopback-only (reachable only via localhost/SSH tunnel)
	}
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
	if v := os.Getenv("SA_SANDBOX"); v != "" {
		cfg.Sandbox.Enabled = !(v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off"))
	}
	if v := os.Getenv("SA_CODER_MODE"); v != "" {
		cfg.Coder.Mode = strings.ToLower(strings.TrimSpace(v))
	}
	// Fail loudly on a typo rather than silently defaulting: an image whose
	// SA_CODER_MODE was misspelled would otherwise advertise a CLI coder kind
	// it does not contain.
	switch cfg.Coder.Mode {
	case ModeFull, ModeSlim:
	case "":
		cfg.Coder.Mode = ModeFull
	default:
		return fmt.Errorf("invalid coder mode %q: want %q or %q",
			cfg.Coder.Mode, ModeFull, ModeSlim)
	}
	return nil
}
