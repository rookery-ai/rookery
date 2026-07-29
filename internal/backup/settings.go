package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ilijad1/simple-agents/internal/secrets"
)

// SettingsKey is the system_settings row holding the owner's backup config.
const SettingsKey = "backup.config"

// Schedule cadences.
const (
	ScheduleDaily  = "daily"
	ScheduleWeekly = "weekly"
)

// Destination kinds.
const (
	DestLocal = "local"
	DestS3    = "s3"
)

// SettingStore is the slice of *db.DB this package needs. Keeping it an
// interface is what lets the settings tests run without a database.
type SettingStore interface {
	GetSystemSetting(key string) (string, error)
	SetSystemSetting(key, value string) error
}

// LocalConfig configures the filesystem destination.
type LocalConfig struct {
	Dir string `json:"dir"`
}

// S3Config configures any S3-compatible destination: AWS, Backblaze B2,
// Cloudflare R2, MinIO, Wasabi.
type S3Config struct {
	Endpoint           string `json:"endpoint"` // empty means AWS
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix"`
	AccessKey          string `json:"access_key"`
	EncryptedSecretKey string `json:"encrypted_secret_key"`
	PathStyle          bool   `json:"path_style"`
}

// Config is the owner's backup configuration. Secret fields are encrypted under
// the system key — the same pattern as workspaces.encrypted_master_password,
// and for the same reason: the scheduler runs headless and must decrypt without
// anyone typing anything.
type Config struct {
	Enabled             bool        `json:"enabled"`
	Destination         string      `json:"destination"`
	Schedule            string      `json:"schedule"`
	Hour                int         `json:"hour"`
	Weekday             int         `json:"weekday"` // 0=Sunday, weekly only
	Retention           int         `json:"retention"`
	EncryptedPassphrase string      `json:"encrypted_passphrase"`
	Local               LocalConfig `json:"local"`
	S3                  S3Config    `json:"s3"`

	LastRunAt  time.Time `json:"last_run_at"`
	LastStatus string    `json:"last_status"` // "ok" | "error" | ""
	LastError  string    `json:"last_error"`
	LastSize   int64     `json:"last_size"`
	NextRunAt  time.Time `json:"next_run_at"`
}

// DefaultConfig is what an unconfigured install reports.
func DefaultConfig() *Config {
	return &Config{
		Enabled:     false,
		Destination: DestLocal,
		Schedule:    ScheduleDaily,
		Hour:        3,
		Retention:   7,
	}
}

// LoadConfig reads the owner's config, returning defaults when unset.
func LoadConfig(store SettingStore, systemKey []byte) (*Config, error) {
	raw, err := store.GetSystemSetting(SettingsKey)
	if err != nil || raw == "" {
		// Any read failure here means "not configured": the caller cannot act on
		// the difference, and an unconfigured install must not fail to boot.
		return DefaultConfig(), nil
	}
	c := DefaultConfig()
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		return nil, fmt.Errorf("backup: parse config: %w", err)
	}
	return c, nil
}

// SaveConfig persists the config.
func SaveConfig(store SettingStore, systemKey []byte, c *Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("backup: encode config: %w", err)
	}
	return store.SetSystemSetting(SettingsKey, string(raw))
}

// SetPassphrase encrypts and stores the envelope passphrase.
func (c *Config) SetPassphrase(systemKey []byte, pw string) error {
	enc, err := secrets.EncryptWithSystemKey(pw, systemKey)
	if err != nil {
		return fmt.Errorf("backup: encrypt passphrase: %w", err)
	}
	c.EncryptedPassphrase = enc
	return nil
}

// Passphrase decrypts the envelope passphrase.
func (c *Config) Passphrase(systemKey []byte) (string, error) {
	if c.EncryptedPassphrase == "" {
		return "", errors.New("backup: no passphrase configured")
	}
	return secrets.DecryptWithSystemKey(c.EncryptedPassphrase, systemKey)
}

// SetS3SecretKey encrypts and stores the S3 secret access key.
func (c *Config) SetS3SecretKey(systemKey []byte, key string) error {
	enc, err := secrets.EncryptWithSystemKey(key, systemKey)
	if err != nil {
		return fmt.Errorf("backup: encrypt s3 secret: %w", err)
	}
	c.S3.EncryptedSecretKey = enc
	return nil
}

// S3SecretKey decrypts the S3 secret access key.
func (c *Config) S3SecretKey(systemKey []byte) (string, error) {
	if c.S3.EncryptedSecretKey == "" {
		return "", errors.New("backup: no s3 secret key configured")
	}
	return secrets.DecryptWithSystemKey(c.S3.EncryptedSecretKey, systemKey)
}

// Validate checks a config that is about to be enabled. A disabled config may
// be incomplete so a half-filled form is still savable.
func (c *Config) Validate() error {
	if c.Schedule != ScheduleDaily && c.Schedule != ScheduleWeekly {
		return fmt.Errorf("schedule must be %q or %q", ScheduleDaily, ScheduleWeekly)
	}
	if c.Hour < 0 || c.Hour > 23 {
		return errors.New("hour must be between 0 and 23")
	}
	if c.Weekday < 0 || c.Weekday > 6 {
		return errors.New("weekday must be between 0 (Sunday) and 6")
	}
	if !c.Enabled {
		return nil
	}
	if c.Retention < 1 {
		return errors.New("retention must keep at least one snapshot")
	}
	if c.EncryptedPassphrase == "" {
		return errors.New("a passphrase is required: a snapshot is never written unencrypted")
	}
	switch c.Destination {
	case DestLocal:
		if c.Local.Dir == "" {
			return errors.New("a backup directory is required")
		}
	case DestS3:
		if c.S3.Bucket == "" {
			return errors.New("an S3 bucket is required")
		}
		if c.S3.AccessKey == "" || c.S3.EncryptedSecretKey == "" {
			return errors.New("S3 access key and secret key are required")
		}
		if c.S3.Region == "" {
			return errors.New("an S3 region is required")
		}
	default:
		return fmt.Errorf("unknown destination %q", c.Destination)
	}
	return nil
}

// BuildDestination constructs the configured Destination.
func (c *Config) BuildDestination(systemKey []byte) (Destination, error) {
	switch c.Destination {
	case DestLocal:
		if c.Local.Dir == "" {
			return nil, errors.New("backup: no backup directory configured")
		}
		return NewLocalDestination(c.Local.Dir), nil
	case DestS3:
		secret, err := c.S3SecretKey(systemKey)
		if err != nil {
			return nil, err
		}
		return NewS3Destination(c.S3, secret), nil
	default:
		return nil, fmt.Errorf("backup: unknown destination %q", c.Destination)
	}
}
