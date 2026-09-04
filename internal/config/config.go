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

// The retired spellings of the default-coder-binary setting, and what to say
// about each. Deprecation is a warning rather than a hard error on purpose: the
// variable is documented and released, so an install that sets it is doing what
// it was told, and refusing to start would punish following the instructions.
const (
	legacyCoderBinEnv = "ROOKERY_CLAUDE_BIN is deprecated and will be removed — " +
		"use ROOKERY_CODER_BIN. It names the default coder binary, and Rookery has " +
		"supported five (claude, opencode, codex, gemini, cursor) since the name was chosen."
	legacyCoderBinKey = "config.yaml: coder.claude_bin is deprecated and will be removed — " +
		"rename it to coder.coder_bin."
)

func dbPathFor(dataDir string) string { return filepath.Join(dataDir, dbFileName) }

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Data     DataConfig     `yaml:"data"`
	Coder    CoderConfig    `yaml:"coder"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
	Browser  BrowserConfig  `yaml:"browser"`
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
	// Bin is the DEFAULT coder binary — the one a workspace gets when it has
	// not set `coder_bin` of its own. It was `ClaudeBin`/`claude_bin`/
	// `ROOKERY_CLAUDE_BIN`, from when Claude Code was the only supported CLI;
	// five are supported now (claude, opencode, codex, gemini, cursor), so a
	// name that hardcodes one of them describes the wrong thing.
	Bin string `yaml:"coder_bin"`
	// LegacyClaudeBin accepts the retired `claude_bin` key so an existing
	// config.yaml keeps working. Read and cleared in Load; never used
	// directly. See legacyCoderBinKey above.
	LegacyClaudeBin string        `yaml:"claude_bin"`
	Timeout         time.Duration `yaml:"timeout"`
	Mode            string        `yaml:"mode"` // "full" (default) or "slim"; see ModeFull/ModeSlim
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

// BrowserConfig controls the headless browser used for JavaScript-rendered
// pages.
//
// AllowPrivate turns OFF the private-address guard, letting the browser reach
// RFC1918, loopback and Tailscale addresses. It is off by default and should
// stay off on any install where an agent browses the public web: the browser
// follows URLs chosen from search results and page content, which is exactly
// the threat nethttp's guard exists for, and the loopback interface hosts this
// server's own connector, knowledge-base and MCP bridges along with their
// per-run bearer tokens.
//
// It exists because reading a self-hosted dashboard on the owner's own LAN is a
// legitimate thing to want, and the alternative — no escape at all — pushes
// people toward worse workarounds. Set via ROOKERY_BROWSER_ALLOW_PRIVATE.
type BrowserConfig struct {
	AllowPrivate bool `yaml:"allow_private"`
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
			// The retired `claude_bin` key still works. Handled here rather
			// than in applyEnv because only the second parse can tell whether
			// the file ALSO set the current key — without that, a config
			// carrying both would let the legacy spelling win, which is the
			// wrong way round for a name being migrated away from.
			if fileCfg.Coder.LegacyClaudeBin != "" && fileCfg.Coder.Bin == "" {
				cfg.Coder.Bin = fileCfg.Coder.LegacyClaudeBin
				cfg.Warnings = append(cfg.Warnings, legacyCoderBinKey)
			}
			// Cleared unconditionally: the field is a parsing shim, and leaving
			// it populated would give the binary a second apparent home that
			// nothing reads.
			cfg.Coder.LegacyClaudeBin = ""
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
			Port: 8899,
		},
		Database: DatabaseConfig{
			Path: dbPathFor(dataDir),
		},
		Data: DataConfig{
			Dir: dataDir,
		},
		Coder: CoderConfig{
			Bin: "claude",
			// An agent BUILD is the long pole, not a chat turn: the coder writes
			// files, runs them against live services, reads the failures and
			// fixes them, sometimes over dozens of tool calls. 20 minutes cut
			// real builds off mid-repair, and the user saw a timeout rather than
			// an agent. Nothing between the browser and here imposes an earlier
			// deadline — the build is detached onto context.Background() and the
			// server sets no write timeout — so this value is the one that
			// decides.
			Timeout: 30 * time.Minute,
			Mode:    ModeFull,
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
	// The retired variable still works and says so, rather than being ignored
	// silently or failing outright. A host that sets both during a migration
	// gets the new one — the one it is going to keep.
	if v := os.Getenv("ROOKERY_CLAUDE_BIN"); v != "" {
		cfg.Coder.Bin = v
		cfg.Warnings = append(cfg.Warnings, legacyCoderBinEnv)
	}
	if v := os.Getenv("ROOKERY_CODER_BIN"); v != "" {
		cfg.Coder.Bin = v
	}
	if v := os.Getenv("ROOKERY_SANDBOX"); v != "" {
		cfg.Sandbox.Enabled = !(v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off"))
	}
	// Opt-IN, so the parse is the mirror image of ROOKERY_SANDBOX's opt-out: an
	// unset or unrecognised value must leave the guard ON. Reusing the sandbox
	// form here would make any non-empty string — including "0" — disable it.
	if v := os.Getenv("ROOKERY_BROWSER_ALLOW_PRIVATE"); v != "" {
		cfg.Browser.AllowPrivate = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "on")
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
