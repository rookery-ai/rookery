package config

import (
	"fmt"
	"log/slog"
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

// dbFileName is the database's name inside the data dir. It lives in one place
// because it used to live in two — defaults() and applyEnv() — which is how the
// yaml path came to relocate everything except the database.
const dbFileName = "rookery.db"

func dbPathFor(dataDir string) string { return filepath.Join(dataDir, dbFileName) }

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Data     DataConfig     `yaml:"data"`
	Coder    CoderConfig    `yaml:"coder"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
	Chat     ChatConfig     `yaml:"chat"`

	// Warnings are resolution problems worth telling the operator about that are
	// not bad enough to refuse to start. `yaml:"-"` because this is an output of
	// loading, never an input to it.
	//
	// Load emits these itself rather than leaving it to its callers: there are
	// four load sites today and a fifth would silently not warn — which is the
	// same drift between two copies that produced the bug this field reports.
	Warnings []string `yaml:"-"`
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
// vault guard. Set via ROOKERY_SANDBOX (0/false/off disables).
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
			// A relocated data dir must carry the database with it. Only
			// applyEnv ever recomputed the path, so ROOKERY_DATA_DIR moved the
			// database while the config field mirroring it did not — leaving the
			// vaults, claude-homes, backups and both keys in the new location and
			// the database in the old one. The new dir then generated its own
			// system.key, so everything the old database holds under the previous
			// key — master passwords, OAuth tokens, bot tokens — silently stopped
			// decrypting, with a server that still booted and still reported ok.
			//
			// The file is parsed a SECOND time into a zero-valued Config to learn
			// which keys it actually set. Comparing the merged result against the
			// defaults cannot tell "unset" from "the user typed the default", and
			// getting that backwards would override a database path someone chose
			// deliberately.
			var fileCfg Config
			if err := yaml.Unmarshal(data, &fileCfg); err != nil {
				return nil, fmt.Errorf("parse config: %w", err)
			}
			if fileCfg.Data.Dir != "" && fileCfg.Database.Path == "" {
				cfg.Database.Path = dbPathFor(cfg.Data.Dir)
			}
		}
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	cfg.Warnings = append(cfg.Warnings, strandedDatabaseWarning(cfg.Database.Path)...)
	for _, w := range cfg.Warnings {
		slog.Warn("config: " + w)
	}
	return cfg, nil
}

// strandedDatabaseWarning reports a database left behind at the default location
// by a relocation.
//
// An install that relocated before this was fixed has its database at the old
// default. Pointing at the new path silently would find nothing, SQLite would
// create an empty database, and the data would look gone — the same boots-green-
// but-empty failure the derivation above exists to remove, just moved.
//
// A warning rather than a refusal to start: a legitimate fresh install can have
// an unrelated ~/.rookery, and dying on that would be worse than the case being
// reported.
//
// The remediation wording is load-bearing and was wrong once. "Move the database
// to the new path" and "set database.path back to the old one" both LOOK correct
// and both reproduce the very failure this warns about, because
// secrets.SystemKey reads <dataDir>/system.key and never follows Database.Path:
// under the first the database arrives beside a different key, under the second
// the data dir — and therefore the key — is still the new one. Only moving the
// whole directory, or not relocating, keeps a database with its key.
func strandedDatabaseWarning(resolved string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	legacy := dbPathFor(filepath.Join(home, ".rookery"))
	if resolved == legacy {
		return nil
	}
	if _, err := os.Stat(resolved); err == nil || !os.IsNotExist(err) {
		return nil // in place, or unreadable for a reason worth its own error
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	return []string{fmt.Sprintf(
		"database %s does not exist, but one is still at the default location %s — "+
			"this install will start with an EMPTY database. Move the whole data "+
			"directory (database, system.key, session.key, vaults/, claude-homes/, "+
			"backups/) to the new location, or point data.dir back at the old one. "+
			"Moving the database alone is NOT enough: the system key is resolved from "+
			"the data dir (<data_dir>/system.key), so a database that arrives without "+
			"its key can no longer decrypt any stored master password, OAuth token or "+
			"bot token", resolved, legacy)}
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".rookery")
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Database: DatabaseConfig{
			Path: dbPathFor(dataDir),
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
	if v := os.Getenv("ROOKERY_HOST"); v != "" {
		cfg.Server.Host = v // e.g. 127.0.0.1 to bind loopback-only (reachable only via localhost/SSH tunnel)
	}
	if v := os.Getenv("ROOKERY_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Server.Port)
	}
	if v := os.Getenv("ROOKERY_DATA_DIR"); v != "" {
		// Deliberately overrides an explicit database.path from the file too:
		// env-over-file is the ordinary precedence here and this variable is
		// documented as relocating the database as well.
		cfg.Data.Dir = v
		cfg.Database.Path = dbPathFor(v)
	}
	if v := os.Getenv("ROOKERY_SESSION_KEY"); v != "" {
		cfg.Server.SessionKey = v
	}
	if v := os.Getenv("ROOKERY_CLAUDE_BIN"); v != "" {
		cfg.Coder.ClaudeBin = v
	}
	if v := os.Getenv("ROOKERY_SANDBOX"); v != "" {
		cfg.Sandbox.Enabled = !(v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off"))
	}
	if v := os.Getenv("ROOKERY_CODER_MODE"); v != "" {
		cfg.Coder.Mode = strings.ToLower(strings.TrimSpace(v))
	}
	// Fail loudly on a typo rather than silently defaulting: an image whose
	// ROOKERY_CODER_MODE was misspelled would otherwise advertise a CLI coder kind
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
