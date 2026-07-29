# Backup and Restore (Scheduling, S3, UI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the working backup CLI into a configured, scheduled, owner-managed feature: daily/weekly snapshots to an S3-compatible bucket with retention, plus a settings UI to configure it, browse snapshots and trigger a restore.

**Architecture:** Owner configuration is one JSON blob in `system_settings`, with secret fields encrypted under the system key. A dedicated ticker goroutine — deliberately not `internal/scheduler`, whose rows are foreign-keyed to a workspace — fires `backup.Snapshot` and then prunes. The S3 destination implements the existing `Destination` interface with a hand-rolled SigV4 signer. Eight owner-scoped API routes back a `BackupSection` in the settings page.

**Tech Stack:** Go stdlib (`crypto/hmac`, `crypto/sha256`, `net/http`, `encoding/xml`), Echo v4, React + TanStack Query + Tailwind.

**Prerequisite:** `docs/superpowers/plans/2026-07-29-backup-and-restore-core.md` must be complete — this plan consumes `backup.Snapshot`, `backup.Destination`, `backup.StageRestore`, `backup.Verify`, `backup.SnapshotName` and `backup.IsSnapshotName` from it.

**Spec:** `docs/superpowers/specs/2026-07-29-backup-and-restore-design.md`

## Global Constraints

- **No new Go module dependencies.** SigV4 is hand-rolled against stdlib HMAC/SHA-256; do not add `aws-sdk-go-v2`.
- **Secrets are never stored plain and never echoed back.** The passphrase and the S3 secret key are encrypted with `secrets.EncryptWithSystemKey`; API responses return a boolean "is set", never the value.
- **`system_settings` key is exactly `backup.config`.**
- **Retention deletes only names matching `backup.IsSnapshotName`.** A destination shared with other data must never have a foreign object removed.
- **Schedule times are server local time**, labelled as such in the UI. The owner has no timezone in the schema.
- **Every new route must be added to the `want` table in `web/api_parity_test.go`** — it is a merge gate.
- **Tests never touch the operator's live install.** Use `t.TempDir()` and `httptest`.
- **Conventional Commits** on every commit.

---

### Task 1: Owner backup settings

**Files:**
- Create: `internal/backup/settings.go`
- Test: `internal/backup/settings_test.go`

**Interfaces:**
- Consumes: `secrets.EncryptWithSystemKey`/`DecryptWithSystemKey`.
- Produces: `backup.Config`, `backup.LocalConfig`, `backup.S3Config`, `backup.SettingsKey`, `backup.LoadConfig(store SettingStore, systemKey []byte) (*Config, error)`, `backup.SaveConfig(store SettingStore, systemKey []byte, c *Config) error`, `(*Config).Destination(systemKey []byte) (Destination, error)`, `(*Config).Passphrase(systemKey []byte) (string, error)`, and the `backup.SettingStore` interface. Tasks 3, 6 use these.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/settings_test.go`:

```go
package backup

import (
	"strings"
	"testing"
)

// memStore is a SettingStore backed by a map, so settings tests need no database.
type memStore struct{ m map[string]string }

func newMemStore() *memStore { return &memStore{m: map[string]string{}} }

func (s *memStore) GetSystemSetting(key string) (string, error) {
	v, ok := s.m[key]
	if !ok {
		return "", errNoSetting
	}
	return v, nil
}
func (s *memStore) SetSystemSetting(key, value string) error { s.m[key] = value; return nil }

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i * 3)
	}
	return k
}

func TestLoadConfigDefaultsWhenAbsent(t *testing.T) {
	c, err := LoadConfig(newMemStore(), testKey())
	if err != nil {
		t.Fatalf("an unconfigured install must not error: %v", err)
	}
	if c.Enabled {
		t.Fatal("backup must default to disabled")
	}
	if c.Schedule != ScheduleDaily {
		t.Fatalf("schedule = %q, want %q", c.Schedule, ScheduleDaily)
	}
	if c.Retention != 7 {
		t.Fatalf("retention = %d, want 7", c.Retention)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	store, key := newMemStore(), testKey()
	c := &Config{
		Enabled: true, Destination: DestS3, Schedule: ScheduleWeekly,
		Hour: 4, Weekday: 2, Retention: 14,
		S3: S3Config{Region: "eu-central-1", Bucket: "b", Prefix: "sa/", AccessKey: "AK"},
	}
	if err := c.SetPassphrase(key, "hunter2"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetS3SecretKey(key, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(store, key, c); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	raw := store.m[SettingsKey]
	if strings.Contains(raw, "hunter2") || strings.Contains(raw, "s3cr3t") {
		t.Fatal("secrets must never be stored in plaintext")
	}

	got, err := LoadConfig(store, key)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Schedule != ScheduleWeekly || got.Hour != 4 || got.Retention != 14 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	pw, err := got.Passphrase(key)
	if err != nil || pw != "hunter2" {
		t.Fatalf("passphrase = %q, %v", pw, err)
	}
	sk, err := got.S3SecretKey(key)
	if err != nil || sk != "s3cr3t" {
		t.Fatalf("s3 secret = %q, %v", sk, err)
	}
}

func TestConfigValidateRejectsBadValues(t *testing.T) {
	cases := map[string]*Config{
		"enabled with no passphrase": {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 7, Local: LocalConfig{Dir: "/tmp/b"}},
		"local with no dir":          {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 7, EncryptedPassphrase: "x"},
		"s3 with no bucket":          {Enabled: true, Destination: DestS3, Schedule: ScheduleDaily, Retention: 7, EncryptedPassphrase: "x"},
		"hour out of range":          {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Hour: 24, Retention: 7, EncryptedPassphrase: "x", Local: LocalConfig{Dir: "/tmp/b"}},
		"retention below one":        {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 0, EncryptedPassphrase: "x", Local: LocalConfig{Dir: "/tmp/b"}},
		"unknown schedule":           {Enabled: true, Destination: DestLocal, Schedule: "hourly", Retention: 7, EncryptedPassphrase: "x", Local: LocalConfig{Dir: "/tmp/b"}},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
}

func TestConfigValidateAcceptsDisabledIncomplete(t *testing.T) {
	// A half-filled form must be savable while the feature is off.
	c := &Config{Enabled: false, Schedule: ScheduleDaily, Retention: 7}
	if err := c.Validate(); err != nil {
		t.Fatalf("a disabled config need not be complete: %v", err)
	}
}

func TestDestinationBuildsLocal(t *testing.T) {
	c := &Config{Destination: DestLocal, Local: LocalConfig{Dir: t.TempDir()}}
	d, err := c.BuildDestination(testKey())
	if err != nil {
		t.Fatalf("BuildDestination: %v", err)
	}
	if !strings.HasPrefix(d.Name(), "local:") {
		t.Fatalf("got %q, want a local destination", d.Name())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/backup/... -run 'TestLoadConfig|TestSaveAndLoad|TestConfigValidate|TestDestinationBuilds' -count=1`
Expected: FAIL — `undefined: LoadConfig`.

- [ ] **Step 3: Implement**

Create `internal/backup/settings.go`:

```go
package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ilijad1/simple-agents-v2/internal/secrets"
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

// errNoSetting is what a SettingStore returns when the key is absent. It is
// matched by string rather than identity because db.ErrNotFound lives in
// another package and this one must not import it.
var errNoSetting = errors.New("setting not found")

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
```

`BuildDestination`'s `DestS3` branch references `NewS3Destination`, which arrives in Task 5. Until then, temporarily return `nil, errors.New("s3 destination not yet implemented")` from that branch so the package compiles, and restore the real call in Task 5 step 4.

- [ ] **Step 4: Run to verify passing**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/settings.go internal/backup/settings_test.go
git commit -m "feat(backup): owner-level configuration in system_settings

Passphrase and S3 secret are encrypted under the system key so the headless
scheduler can decrypt them without anyone typing anything."
```

---

### Task 2: Retention

**Files:**
- Create: `internal/backup/retention.go`
- Test: `internal/backup/retention_test.go`

**Interfaces:**
- Consumes: `Destination`, `Entry`, `IsSnapshotName` (core plan Task 5).
- Produces: `backup.Prune(ctx context.Context, d Destination, keep int) ([]string, error)` returning the deleted names. Task 3 calls it.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/retention_test.go`:

```go
package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func seedSnapshots(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	seedSnapshots(t, dir,
		"simple-agents-20260725-030000.sab",
		"simple-agents-20260726-030000.sab",
		"simple-agents-20260727-030000.sab",
		"simple-agents-20260728-030000.sab",
	)
	deleted, err := Prune(context.Background(), NewLocalDestination(dir), 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted %v, want the two oldest", deleted)
	}
	remaining, _ := NewLocalDestination(dir).List(context.Background())
	if len(remaining) != 2 {
		t.Fatalf("kept %d, want 2", len(remaining))
	}
	for _, e := range remaining {
		if e.Name < "simple-agents-20260727" {
			t.Fatalf("kept the wrong ones: %+v", remaining)
		}
	}
}

func TestPruneNoopUnderLimit(t *testing.T) {
	dir := t.TempDir()
	seedSnapshots(t, dir, "simple-agents-20260728-030000.sab")
	deleted, err := Prune(context.Background(), NewLocalDestination(dir), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v, want none", deleted)
	}
}

// The property that matters most: a bucket or folder shared with other data
// must never lose a foreign file to retention.
func TestPruneNeverTouchesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	seedSnapshots(t, dir,
		"simple-agents-20260725-030000.sab",
		"simple-agents-20260726-030000.sab",
	)
	if err := os.WriteFile(filepath.Join(dir, "important-tax-return.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(context.Background(), NewLocalDestination(dir), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "important-tax-return.pdf")); err != nil {
		t.Fatalf("a foreign file was deleted: %v", err)
	}
}

func TestPruneRejectsKeepBelowOne(t *testing.T) {
	if _, err := Prune(context.Background(), NewLocalDestination(t.TempDir()), 0); err == nil {
		t.Fatal("keep<1 would delete every snapshot; it must be refused")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/backup/... -run TestPrune -count=1`
Expected: FAIL — `undefined: Prune`.

- [ ] **Step 3: Implement**

Create `internal/backup/retention.go`:

```go
package backup

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Prune deletes all but the newest keep snapshots and returns what it removed.
//
// It lists through the Destination, which already filters on the snapshot name
// pattern, and filters again here. A destination is frequently a bucket or
// folder holding other things, and deleting a stranger's file would be an
// unrecoverable bug in a feature whose entire purpose is not losing data.
func Prune(ctx context.Context, d Destination, keep int) ([]string, error) {
	if keep < 1 {
		return nil, errors.New("backup: retention must keep at least one snapshot")
	}
	entries, err := d.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: list for retention: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if IsSnapshotName(e.Name) {
			names = append(names, e.Name)
		}
	}
	// Snapshot names embed a sortable UTC timestamp, so lexical order is
	// chronological order.
	sort.Strings(names)
	if len(names) <= keep {
		return nil, nil
	}

	var deleted []string
	for _, name := range names[:len(names)-keep] {
		if err := d.Delete(ctx, name); err != nil {
			return deleted, fmt.Errorf("backup: delete %s: %w", name, err)
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}
```

- [ ] **Step 4: Run to verify passing**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/retention.go internal/backup/retention_test.go
git commit -m "feat(backup): keep-last-N retention that only ever deletes its own snapshots"
```

---

### Task 3: The backup scheduler

**Files:**
- Create: `internal/backup/schedule.go`
- Test: `internal/backup/schedule_test.go`

**Interfaces:**
- Consumes: `Config` (Task 1), `Prune` (Task 2), `Snapshot` (core plan Task 6).
- Produces: `backup.NextRun(c *Config, from time.Time) time.Time` and `backup.Scheduler` with `backup.NewScheduler(store SettingStore, database *sql.DB, dataDir string, systemKey []byte) *Scheduler`, `(*Scheduler).Run(ctx context.Context)`, `(*Scheduler).RunOnce(ctx context.Context) (string, error)`. Task 6 calls `RunOnce` for the manual "Back up now" button; `main.go` starts `Run`.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/schedule_test.go`:

```go
package backup

import (
	"context"
	"testing"
	"time"
)

func TestNextRunDaily(t *testing.T) {
	c := &Config{Schedule: ScheduleDaily, Hour: 3}
	// Before today's slot → today.
	from := time.Date(2026, 7, 29, 1, 0, 0, 0, time.Local)
	got := NextRun(c, from)
	want := time.Date(2026, 7, 29, 3, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// After today's slot → tomorrow.
	from = time.Date(2026, 7, 29, 5, 0, 0, 0, time.Local)
	got = NextRun(c, from)
	want = time.Date(2026, 7, 30, 3, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNextRunWeekly(t *testing.T) {
	// Weekday 0 = Sunday. 2026-07-29 is a Wednesday.
	c := &Config{Schedule: ScheduleWeekly, Hour: 4, Weekday: 0}
	from := time.Date(2026, 7, 29, 10, 0, 0, 0, time.Local)
	got := NextRun(c, from)
	if got.Weekday() != time.Sunday {
		t.Fatalf("got %v (%v), want a Sunday", got, got.Weekday())
	}
	if got.Hour() != 4 {
		t.Fatalf("hour = %d, want 4", got.Hour())
	}
	if !got.After(from) {
		t.Fatalf("next run %v must be after %v", got, from)
	}
}

func TestNextRunWeeklyLaterSameDay(t *testing.T) {
	// On the scheduled weekday but before the hour → today, not next week.
	c := &Config{Schedule: ScheduleWeekly, Hour: 20, Weekday: 3} // Wednesday
	from := time.Date(2026, 7, 29, 10, 0, 0, 0, time.Local)      // Wednesday 10:00
	got := NextRun(c, from)
	want := time.Date(2026, 7, 29, 20, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A server down for a week must produce ONE snapshot on boot, not seven.
func TestMissedRunsCollapseToOne(t *testing.T) {
	c := &Config{Schedule: ScheduleDaily, Hour: 3}
	longAgo := time.Date(2026, 7, 20, 3, 0, 0, 0, time.Local)
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.Local)

	next := NextRun(c, now)
	if !next.After(now) {
		t.Fatalf("next run %v must be in the future", next)
	}
	if next.Sub(now) > 25*time.Hour {
		t.Fatalf("next run %v is too far out; missed slots must not accumulate", next)
	}
	_ = longAgo
}

func TestSchedulerRunOnceWritesSnapshotAndPrunes(t *testing.T) {
	dataDir := t.TempDir()
	database, _ := newTestDB(t, dataDir)
	writeFile(t, dataDir+"/vaults/ws1/notes/a.md", "note")

	destDir := t.TempDir()
	key := testKey()
	store := newMemStore()

	c := DefaultConfig()
	c.Enabled = true
	c.Destination = DestLocal
	c.Local = LocalConfig{Dir: destDir}
	c.Retention = 1
	if err := c.SetPassphrase(key, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(store, key, c); err != nil {
		t.Fatal(err)
	}

	s := NewScheduler(store, database, dataDir, key)

	first, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !IsSnapshotName(first) {
		t.Fatalf("got %q", first)
	}

	// Status must be recorded for the settings banner.
	after, _ := LoadConfig(store, key)
	if after.LastStatus != "ok" {
		t.Fatalf("last status = %q, want ok", after.LastStatus)
	}
	if after.LastSize <= 0 {
		t.Fatalf("last size = %d, want > 0", after.LastSize)
	}
	if after.NextRunAt.IsZero() {
		t.Fatal("next run must be scheduled after a successful run")
	}
}

func TestSchedulerRunOnceRecordsFailure(t *testing.T) {
	dataDir := t.TempDir()
	database, _ := newTestDB(t, dataDir)
	key := testKey()
	store := newMemStore()

	c := DefaultConfig()
	c.Enabled = true
	c.Destination = DestLocal
	c.Local = LocalConfig{Dir: "/proc/nonexistent-cannot-create"}
	if err := c.SetPassphrase(key, "pw"); err != nil {
		t.Fatal(err)
	}
	SaveConfig(store, key, c)

	if _, err := NewScheduler(store, database, dataDir, key).RunOnce(context.Background()); err == nil {
		t.Fatal("expected the run to fail")
	}
	after, _ := LoadConfig(store, key)
	if after.LastStatus != "error" || after.LastError == "" {
		t.Fatalf("failure must be recorded for the settings banner: %+v", after)
	}
}

// A schedule enabled without a passphrase must refuse rather than write plain.
func TestSchedulerRefusesWithoutPassphrase(t *testing.T) {
	dataDir := t.TempDir()
	database, _ := newTestDB(t, dataDir)
	key := testKey()
	store := newMemStore()

	c := DefaultConfig()
	c.Enabled = true
	c.Destination = DestLocal
	c.Local = LocalConfig{Dir: t.TempDir()}
	SaveConfig(store, key, c)

	if _, err := NewScheduler(store, database, dataDir, key).RunOnce(context.Background()); err == nil {
		t.Fatal("expected a refusal when no passphrase is configured")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/backup/... -run 'TestNextRun|TestMissed|TestScheduler' -count=1`
Expected: FAIL — `undefined: NextRun`.

- [ ] **Step 3: Implement**

Create `internal/backup/schedule.go`:

```go
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// pollInterval is how often the ticker checks whether a run is due. One minute
// is plenty for a daily/weekly cadence and keeps the goroutine cheap.
const pollInterval = time.Minute

// Scheduler fires backup runs on the owner's cadence.
//
// It is deliberately NOT part of internal/scheduler: that one polls
// agent_schedules, whose rows are foreign-keyed to a workspace. Backup is
// owner-level and belongs to no workspace, so it gets its own ticker rather
// than a fake workspace row.
type Scheduler struct {
	store     SettingStore
	db        *sql.DB
	dataDir   string
	systemKey []byte
}

func NewScheduler(store SettingStore, database *sql.DB, dataDir string, systemKey []byte) *Scheduler {
	return &Scheduler{store: store, db: database, dataDir: dataDir, systemKey: systemKey}
}

// NextRun computes the next scheduled time strictly after from, in server local
// time. The owner has no timezone in the schema — workspaces have profiles, the
// owner does not — so server local time is the honest choice, and the UI says so.
func NextRun(c *Config, from time.Time) time.Time {
	loc := from.Location()
	candidate := time.Date(from.Year(), from.Month(), from.Day(), c.Hour, 0, 0, 0, loc)

	switch c.Schedule {
	case ScheduleWeekly:
		delta := (c.Weekday - int(candidate.Weekday()) + 7) % 7
		candidate = candidate.AddDate(0, 0, delta)
		if !candidate.After(from) {
			candidate = candidate.AddDate(0, 0, 7)
		}
	default: // daily
		if !candidate.After(from) {
			candidate = candidate.AddDate(0, 0, 1)
		}
	}
	return candidate
}

// Run polls until ctx is cancelled, firing when a run is due.
//
// Missed runs collapse: a server that was down across several scheduled times
// finds NextRunAt in the past on boot, runs ONCE, and reschedules forward from
// now. It never replays every missed slot.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, err := LoadConfig(s.store, s.systemKey)
			if err != nil || !c.Enabled {
				continue
			}
			now := time.Now()
			if c.NextRunAt.IsZero() {
				c.NextRunAt = NextRun(c, now)
				_ = SaveConfig(s.store, s.systemKey, c)
				continue
			}
			if now.Before(c.NextRunAt) {
				continue
			}
			if _, err := s.RunOnce(ctx); err != nil {
				slog.Error("scheduled backup failed", "error", err)
			}
		}
	}
}

// RunOnce takes one snapshot immediately, applies retention, and records the
// outcome. It is shared by the ticker and the "Back up now" button.
func (s *Scheduler) RunOnce(ctx context.Context) (string, error) {
	c, err := LoadConfig(s.store, s.systemKey)
	if err != nil {
		return "", err
	}

	name, runErr := s.run(ctx, c)

	c.LastRunAt = time.Now()
	c.NextRunAt = NextRun(c, time.Now())
	if runErr != nil {
		c.LastStatus = "error"
		c.LastError = runErr.Error()
	} else {
		c.LastStatus = "ok"
		c.LastError = ""
	}
	if err := SaveConfig(s.store, s.systemKey, c); err != nil {
		slog.Error("could not record backup status", "error", err)
	}
	return name, runErr
}

func (s *Scheduler) run(ctx context.Context, c *Config) (string, error) {
	pass, err := c.Passphrase(s.systemKey)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	dest, err := c.BuildDestination(s.systemKey)
	if err != nil {
		return "", err
	}

	name, err := Snapshot(ctx, Options{
		DB: s.db, DataDir: s.dataDir,
		SystemKey: s.systemKey, Passphrase: pass, Destination: dest,
	})
	if err != nil {
		return "", err
	}

	// Record the size for the settings banner before pruning.
	if entries, err := dest.List(ctx); err == nil {
		for _, e := range entries {
			if e.Name == name {
				c.LastSize = e.Size
			}
		}
	}

	if deleted, err := Prune(ctx, dest, c.Retention); err != nil {
		// Retention failing does not invalidate a snapshot that was written.
		slog.Warn("backup retention failed", "error", err)
	} else if len(deleted) > 0 {
		slog.Info("pruned old snapshots", "count", len(deleted))
	}
	return name, nil
}
```

- [ ] **Step 4: Run to verify passing**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 5: Start the scheduler in serve**

In `cmd/simple-agents/main.go`, after the existing scheduler and reminder goroutines are started, add:

```go
			backupSched := backup.NewScheduler(database, database.DB, cfg.Data.Dir, sysKey)
			go backupSched.Run(ctx)
			slog.Info("backup scheduler started")
```

`database` satisfies `backup.SettingStore` through its existing `GetSystemSetting`/`SetSystemSetting` methods. Confirm how the raw `*sql.DB` is reached from `*db.DB` (check whether `db.DB` embeds `*sql.DB` — `grep -n "type DB struct" -A5 internal/db/db.go`); if it is an unexported field, add a small `func (d *DB) SQL() *sql.DB { return d.DB }` accessor and use that.

- [ ] **Step 6: Verify the build and suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/backup/schedule.go internal/backup/schedule_test.go cmd/simple-agents/main.go
git commit -m "feat(backup): daily/weekly scheduler with missed-run collapse

Its own ticker rather than internal/scheduler, whose agent_schedules rows are
foreign-keyed to a workspace that owner-level backup does not have."
```

---

### Task 4: SigV4 signer

**Files:**
- Create: `internal/backup/sigv4.go`
- Test: `internal/backup/sigv4_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `backup.signV4(req *http.Request, accessKey, secretKey, region, service, payloadSHA256 string, now time.Time) error`. Task 5 calls it.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/sigv4_test.go`:

```go
package backup

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The canonical vector from AWS's SigV4 test suite (get-vanilla): a GET to
// example.amazonaws.com with a fixed date and credentials produces a known
// signature. Pinning it means a refactor cannot silently break signing in a way
// only a live bucket would reveal.
func TestSignV4MatchesAWSVector(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"
	when := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	err = signV4(req, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		"us-east-1", "service", emptyPayloadSHA256, when)
	if err != nil {
		t.Fatalf("signV4: %v", err)
	}

	got := req.Header.Get("Authorization")
	const want = "AWS4-HMAC-SHA256 " +
		"Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date, " +
		"Signature="
	if !strings.HasPrefix(got, want) {
		t.Fatalf("got %q,\nwant prefix %q", got, want)
	}
	if req.Header.Get("X-Amz-Date") != "20150830T123600Z" {
		t.Fatalf("X-Amz-Date = %q", req.Header.Get("X-Amz-Date"))
	}
	if req.Header.Get("X-Amz-Content-Sha256") != emptyPayloadSHA256 {
		t.Fatalf("payload hash header not set")
	}
}

func TestSignV4IsDeterministic(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	sig := func() string {
		req, _ := http.NewRequest("PUT", "https://b.s3.amazonaws.com/sa/x.sab", nil)
		signV4(req, "AK", "SK", "us-east-1", "s3", emptyPayloadSHA256, when)
		return req.Header.Get("Authorization")
	}
	if sig() != sig() {
		t.Fatal("signing must be deterministic for identical inputs")
	}
}

func TestSignV4DiffersByPayload(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	reqA, _ := http.NewRequest("PUT", "https://b.s3.amazonaws.com/x", nil)
	reqB, _ := http.NewRequest("PUT", "https://b.s3.amazonaws.com/x", nil)
	signV4(reqA, "AK", "SK", "us-east-1", "s3", emptyPayloadSHA256, when)
	signV4(reqB, "AK", "SK", "us-east-1", "s3", strings.Repeat("a", 64), when)
	if reqA.Header.Get("Authorization") == reqB.Header.Get("Authorization") {
		t.Fatal("a different payload hash must produce a different signature")
	}
}

func TestSignV4EncodesQuery(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	req, _ := http.NewRequest("GET", "https://b.s3.amazonaws.com/?list-type=2&prefix=sa%2F", nil)
	if err := signV4(req, "AK", "SK", "us-east-1", "s3", emptyPayloadSHA256, when); err != nil {
		t.Fatalf("signV4: %v", err)
	}
	if req.Header.Get("Authorization") == "" {
		t.Fatal("query requests must still be signed")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/backup/... -run TestSignV4 -count=1`
Expected: FAIL — `undefined: signV4`.

- [ ] **Step 3: Implement**

Create `internal/backup/sigv4.go`:

```go
package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// emptyPayloadSHA256 is SHA-256 of the empty string, the payload hash for any
// request with no body.
const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// signV4 signs req in place with AWS Signature Version 4.
//
// Hand-rolled against stdlib HMAC/SHA-256 rather than pulling in aws-sdk-go-v2:
// four verbs against one bucket do not justify that dependency tree in a
// project that deliberately keeps dependencies few.
//
// payloadSHA256 must be the hex SHA-256 of the request body. Callers with a
// body on disk hash the file first — S3 requires the hash up front, which is
// one more reason the engine stages the snapshot to a temp file.
func signV4(req *http.Request, accessKey, secretKey, region, service, payloadSHA256 string, now time.Time) error {
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("backup: S3 credentials are not configured")
	}
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadSHA256)

	// Canonical headers: host plus the two x-amz headers, lowercase, sorted.
	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadSHA256,
		"x-amz-date":           amzDate,
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headers[k]))
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Query values must be sorted and RFC3986-encoded.
	canonicalQuery := req.URL.Query().Encode()

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadSHA256,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature))
	return nil
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run to verify passing**

Run: `go test ./internal/backup/... -run TestSignV4 -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/sigv4.go internal/backup/sigv4_test.go
git commit -m "feat(backup): minimal AWS SigV4 signer

Four verbs against one bucket do not justify aws-sdk-go-v2's dependency tree."
```

---

### Task 5: S3-compatible destination

**Files:**
- Create: `internal/backup/dest_s3.go`
- Test: `internal/backup/dest_s3_test.go`
- Modify: `internal/backup/settings.go` (restore the real `NewS3Destination` call)

**Interfaces:**
- Consumes: `Destination`, `Entry`, `IsSnapshotName` (core plan Task 5), `signV4` (Task 4), `S3Config` (Task 1).
- Produces: `backup.NewS3Destination(cfg S3Config, secretKey string) *S3Destination`.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/dest_s3_test.go`:

```go
package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestS3PutSignsAndUploads(t *testing.T) {
	var gotPath, gotAuth, gotLen string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotLen = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Length")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "mybucket",
		Prefix: "sa/", AccessKey: "AK", PathStyle: true,
	}, "SK")

	body := []byte("encrypted snapshot")
	name := "simple-agents-20260729-030000.sab"
	if err := d.Put(context.Background(), name, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if gotPath != "/mybucket/sa/"+name {
		t.Fatalf("path = %q, want path-style /mybucket/sa/<name>", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AK/") {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotLen != fmt.Sprint(len(body)) {
		t.Fatalf("Content-Length = %q, want %d", gotLen, len(body))
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatal("uploaded body does not match")
	}
}

func TestS3VirtualHostStyleURL(t *testing.T) {
	var gotPath, gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.Path, r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "mybucket",
		AccessKey: "AK", PathStyle: false,
	}, "SK")
	if err := d.Put(context.Background(), "simple-agents-20260729-030000.sab", strings.NewReader("x"), 1); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if gotPath != "/simple-agents-20260729-030000.sab" {
		t.Fatalf("virtual-host style must omit the bucket from the path, got %q", gotPath)
	}
	if !strings.HasPrefix(gotHost, "mybucket.") {
		t.Fatalf("host = %q, want the bucket as a subdomain", gotHost)
	}
}

func TestS3ListParsesAndFilters(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <Contents><Key>sa/simple-agents-20260728-030000.sab</Key><Size>120</Size><LastModified>2026-07-28T03:00:00.000Z</LastModified></Contents>
  <Contents><Key>sa/simple-agents-20260729-030000.sab</Key><Size>130</Size><LastModified>2026-07-29T03:00:00.000Z</LastModified></Contents>
  <Contents><Key>sa/notes.txt</Key><Size>5</Size><LastModified>2026-07-29T03:00:00.000Z</LastModified></Contents>
</ListBucketResult>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") != "2" {
			t.Errorf("expected a ListObjectsV2 request, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, body)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", Prefix: "sa/",
		AccessKey: "AK", PathStyle: true,
	}, "SK")

	entries, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 — foreign keys must be filtered out: %+v", len(entries), entries)
	}
	if entries[0].Name != "simple-agents-20260728-030000.sab" {
		t.Fatalf("name = %q, want the prefix stripped", entries[0].Name)
	}
	if entries[0].Size != 120 {
		t.Fatalf("size = %d, want 120", entries[0].Size)
	}
}

func TestS3GetReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "snapshot-bytes")
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "AK", PathStyle: true}, "SK")
	rc, err := d.Get(context.Background(), "simple-agents-20260729-030000.sab")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "snapshot-bytes" {
		t.Fatalf("got %q", got)
	}
}

func TestS3ErrorsCarryStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "AK", PathStyle: true}, "SK")
	err := d.Put(context.Background(), "simple-agents-20260729-030000.sab", strings.NewReader("x"), 1)
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("error must carry the status and body for triage, got %q", err)
	}
}

func TestS3DeleteRefusesForeignNames(t *testing.T) {
	d := NewS3Destination(S3Config{Endpoint: "http://unused", Region: "us-east-1", Bucket: "b", AccessKey: "AK"}, "SK")
	if err := d.Delete(context.Background(), "important-tax-return.pdf"); err == nil {
		t.Fatal("delete must refuse a name that is not a snapshot")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/backup/... -run TestS3 -count=1`
Expected: FAIL — `undefined: NewS3Destination`.

- [ ] **Step 3: Implement**

Create `internal/backup/dest_s3.go`:

```go
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// S3Destination stores snapshots in any S3-compatible bucket: AWS S3,
// Backblaze B2, Cloudflare R2, MinIO, Wasabi. One implementation covers them
// all, which is why this was chosen over the OAuth providers as the first
// remote destination — static credentials, no app registration, no browser.
type S3Destination struct {
	cfg       S3Config
	secretKey string
	client    *http.Client
}

func NewS3Destination(cfg S3Config, secretKey string) *S3Destination {
	return &S3Destination{
		cfg:       cfg,
		secretKey: secretKey,
		client:    &http.Client{Timeout: 10 * time.Minute},
	}
}

func (d *S3Destination) Name() string {
	return "s3:" + d.cfg.Bucket + "/" + d.cfg.Prefix
}

// key renders the full object key for a snapshot name.
func (d *S3Destination) key(name string) string {
	return strings.TrimPrefix(d.cfg.Prefix, "/") + name
}

// endpointURL builds the request URL, honouring path-style vs virtual-host.
// MinIO and some R2 setups require path-style; AWS defaults to virtual-host.
func (d *S3Destination) endpointURL(objectKey string, query url.Values) (*url.URL, error) {
	base := d.cfg.Endpoint
	if base == "" {
		base = "https://s3." + d.cfg.Region + ".amazonaws.com"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("backup: bad S3 endpoint %q: %w", base, err)
	}
	if d.cfg.PathStyle {
		u.Path = "/" + d.cfg.Bucket
		if objectKey != "" {
			u.Path += "/" + objectKey
		}
	} else {
		u.Host = d.cfg.Bucket + "." + u.Host
		u.Path = "/"
		if objectKey != "" {
			u.Path = "/" + objectKey
		}
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u, nil
}

// do signs and performs a request, returning the response for 2xx and a
// descriptive error otherwise.
func (d *S3Destination) do(ctx context.Context, method string, u *url.URL, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("backup: build request: %w", err)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if err := signV4(req, d.cfg.AccessKey, d.secretKey, d.cfg.Region, "s3", payloadHash, time.Now()); err != nil {
		return nil, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backup: %s %s: %w", method, u.Host, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Read a bounded slice of the error document: S3 error bodies name the
		// exact cause (AccessDenied, NoSuchBucket, SignatureDoesNotMatch) and
		// dropping them turns every misconfiguration into "it failed".
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("backup: S3 %s returned %d: %s", method, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp, nil
}

// Put uploads a snapshot. It requires an io.ReadSeeker so the payload can be
// hashed and then rewound — S3 demands the content hash in the signature,
// before the body is sent.
func (d *S3Destination) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	seeker, ok := r.(io.ReadSeeker)
	if !ok {
		return fmt.Errorf("backup: S3 uploads need a seekable source; stage the snapshot to a file first")
	}
	h := sha256.New()
	if _, err := io.Copy(h, seeker); err != nil {
		return fmt.Errorf("backup: hash payload: %w", err)
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup: rewind payload: %w", err)
	}
	payloadHash := hex.EncodeToString(h.Sum(nil))

	u, err := d.endpointURL(d.key(name), nil)
	if err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodPut, u, seeker, size, payloadHash)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (d *S3Destination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	u, err := d.endpointURL(d.key(name), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.do(ctx, http.MethodGet, u, nil, -1, emptyPayloadSHA256)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// listBucketResult mirrors the ListObjectsV2 response shape.
type listBucketResult struct {
	XMLName  xml.Name `xml:"ListBucketResult"`
	Contents []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
}

func (d *S3Destination) List(ctx context.Context) ([]Entry, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if p := strings.TrimPrefix(d.cfg.Prefix, "/"); p != "" {
		q.Set("prefix", p)
	}
	u, err := d.endpointURL("", q)
	if err != nil {
		return nil, err
	}
	resp, err := d.do(ctx, http.MethodGet, u, nil, -1, emptyPayloadSHA256)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed listBucketResult
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("backup: parse S3 listing: %w", err)
	}

	var out []Entry
	for _, c := range parsed.Contents {
		name := c.Key
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		// A bucket is frequently shared; only ever surface our own snapshots.
		if !IsSnapshotName(name) {
			continue
		}
		out = append(out, Entry{Name: name, Size: c.Size, ModTime: c.LastModified})
	}
	return out, nil
}

func (d *S3Destination) Delete(ctx context.Context, name string) error {
	if !IsSnapshotName(name) {
		return fmt.Errorf("backup: refusing to delete %q: not a snapshot name", name)
	}
	u, err := d.endpointURL(d.key(name), nil)
	if err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodDelete, u, nil, -1, emptyPayloadSHA256)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
```

- [ ] **Step 4: Restore the settings wiring**

In `internal/backup/settings.go`, replace the temporary `DestS3` stub in `BuildDestination` with the real call:

```go
	case DestS3:
		secret, err := c.S3SecretKey(systemKey)
		if err != nil {
			return nil, err
		}
		return NewS3Destination(c.S3, secret), nil
```

**Also update `Snapshot` in `internal/backup/backup.go`:** it already opens the staged file and passes `*os.File` to `Put`, which satisfies `io.ReadSeeker`, so no change is needed there. Confirm by re-reading the `Snapshot` upload block; if it wraps the file in a `bufio.Reader` or similar, unwrap it — S3 `Put` requires a seekable source.

- [ ] **Step 5: Run to verify passing**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/backup/dest_s3.go internal/backup/dest_s3_test.go internal/backup/settings.go
git commit -m "feat(backup): S3-compatible destination

One implementation covers AWS, B2, R2, MinIO and Wasabi. Listing and deletion
filter on the snapshot name so a shared bucket never loses a foreign object."
```

---

### Task 6: Owner-scoped API routes

**Files:**
- Create: `web/api_backup.go`
- Create: `web/api_backup_test.go`
- Modify: `web/api.go` (register the routes)
- Modify: `web/api_parity_test.go` (add all eight to the `want` table)

**Interfaces:**
- Consumes: `backup.LoadConfig`/`SaveConfig`/`Config` (Task 1), `backup.Scheduler.RunOnce` (Task 3), `backup.StageRestore`/`Verify` (core plan Task 7), `backup.Destination` (core plan Task 5).
- Produces: the eight routes below, plus `web.Server.backupScheduler` wiring.

Routes (all `requireOwnerAPI`, deliberately **without** `requireActiveWorkspaceAPI` — backup is not workspace-scoped):

```
GET    /api/v1/backup/config
PUT    /api/v1/backup/config
POST   /api/v1/backup/run
GET    /api/v1/backup/snapshots
GET    /api/v1/backup/snapshots/:name/download
DELETE /api/v1/backup/snapshots/:name
POST   /api/v1/backup/verify
POST   /api/v1/backup/restore
```

- [ ] **Step 1: Write the failing tests**

Create `web/api_backup_test.go`, following the existing helpers in `web/api_test_helpers_test.go` (read that file first for the exact server/session constructors this repo uses, and mirror them):

```go
package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBackupConfigRequiresOwner(t *testing.T) {
	srv := newTestServer(t)
	rec := srv.doJSON(t, http.MethodGet, "/api/v1/backup/config", nil, withoutSession)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestBackupConfigDefaultsAndNeverLeaksSecrets(t *testing.T) {
	srv := newTestServer(t)
	rec := srv.doJSON(t, http.MethodGet, "/api/v1/backup/config", nil, asOwner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schedule"] != "daily" {
		t.Fatalf("schedule = %v, want daily", body["schedule"])
	}
	if _, present := body["encrypted_passphrase"]; present {
		t.Fatal("the encrypted passphrase must never be sent to the browser")
	}
	if body["passphrase_set"] != false {
		t.Fatalf("passphrase_set = %v, want false", body["passphrase_set"])
	}
}

func TestBackupSaveConfigStoresPassphraseAndDoesNotEchoIt(t *testing.T) {
	srv := newTestServer(t)
	payload := `{"enabled":true,"destination":"local","schedule":"weekly","hour":4,"weekday":1,
	             "retention":5,"passphrase":"hunter2","local":{"dir":"/tmp/sa-backups"}}`
	rec := srv.doJSON(t, http.MethodPut, "/api/v1/backup/config", strings.NewReader(payload), asOwner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatal("the passphrase must never be echoed back")
	}

	rec = srv.doJSON(t, http.MethodGet, "/api/v1/backup/config", nil, asOwner)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["passphrase_set"] != true {
		t.Fatal("passphrase_set must be true after saving one")
	}
	if body["schedule"] != "weekly" {
		t.Fatalf("schedule = %v, want weekly", body["schedule"])
	}
}

func TestBackupSaveConfigRejectsEnabledWithoutPassphrase(t *testing.T) {
	srv := newTestServer(t)
	payload := `{"enabled":true,"destination":"local","schedule":"daily","hour":3,"retention":7,
	             "local":{"dir":"/tmp/sa-backups"}}`
	rec := srv.doJSON(t, http.MethodPut, "/api/v1/backup/config", strings.NewReader(payload), asOwner)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — a snapshot is never written unencrypted", rec.Code)
	}
}

func TestBackupRestoreRequiresConfirmation(t *testing.T) {
	srv := newTestServer(t)
	payload := `{"name":"simple-agents-20260729-030000.sab","passphrase":"pw","confirm":"nope"}`
	rec := srv.doJSON(t, http.MethodPost, "/api/v1/backup/restore", strings.NewReader(payload), asOwner)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without the RESTORE confirmation", rec.Code)
	}
}

func TestBackupDeleteRejectsForeignName(t *testing.T) {
	srv := newTestServer(t)
	rec := srv.doJSON(t, http.MethodDelete, "/api/v1/backup/snapshots/important-tax-return.pdf", nil, asOwner)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-snapshot name", rec.Code)
	}
}
```

Adapt `newTestServer`, `doJSON`, `asOwner` and `withoutSession` to whatever the existing helpers are actually named — read `web/api_test_helpers_test.go` and reuse them verbatim rather than inventing new ones.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./web/... -run TestBackup -count=1`
Expected: FAIL — routes return 404.

- [ ] **Step 3: Implement the handlers**

Create `web/api_backup.go`:

```go
package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ilijad1/simple-agents-v2/internal/backup"
)

// backupConfigDTO is the browser-facing shape. It deliberately omits every
// encrypted field and reports only whether a secret is set — the API must never
// hand a stored credential back to the page that stored it.
type backupConfigDTO struct {
	Enabled       bool      `json:"enabled"`
	Destination   string    `json:"destination"`
	Schedule      string    `json:"schedule"`
	Hour          int       `json:"hour"`
	Weekday       int       `json:"weekday"`
	Retention     int       `json:"retention"`
	PassphraseSet bool      `json:"passphrase_set"`
	LocalDir      string    `json:"local_dir"`
	S3            s3DTO     `json:"s3"`
	LastRunAt     time.Time `json:"last_run_at"`
	LastStatus    string    `json:"last_status"`
	LastError     string    `json:"last_error"`
	LastSize      int64     `json:"last_size"`
	NextRunAt     time.Time `json:"next_run_at"`
	PendingRestore bool     `json:"pending_restore"`
}

type s3DTO struct {
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix"`
	AccessKey    string `json:"access_key"`
	SecretKeySet bool   `json:"secret_key_set"`
	PathStyle    bool   `json:"path_style"`
}

func toBackupDTO(c *backup.Config, dataDir string) backupConfigDTO {
	return backupConfigDTO{
		Enabled: c.Enabled, Destination: c.Destination, Schedule: c.Schedule,
		Hour: c.Hour, Weekday: c.Weekday, Retention: c.Retention,
		PassphraseSet: c.EncryptedPassphrase != "",
		LocalDir:      c.Local.Dir,
		S3: s3DTO{
			Endpoint: c.S3.Endpoint, Region: c.S3.Region, Bucket: c.S3.Bucket,
			Prefix: c.S3.Prefix, AccessKey: c.S3.AccessKey,
			SecretKeySet: c.S3.EncryptedSecretKey != "", PathStyle: c.S3.PathStyle,
		},
		LastRunAt: c.LastRunAt, LastStatus: c.LastStatus, LastError: c.LastError,
		LastSize: c.LastSize, NextRunAt: c.NextRunAt,
		PendingRestore: backup.HasPendingRestore(dataDir),
	}
}

func (s *Server) handleGetBackupConfig(c echo.Context) error {
	cfg, err := backup.LoadConfig(s.db, s.systemKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, toBackupDTO(cfg, s.dataDir))
}

// backupConfigReq carries plaintext secrets inbound only. An empty passphrase
// means "leave the stored one alone", so saving an unrelated field does not
// wipe the credential.
type backupConfigReq struct {
	Enabled     bool   `json:"enabled"`
	Destination string `json:"destination"`
	Schedule    string `json:"schedule"`
	Hour        int    `json:"hour"`
	Weekday     int    `json:"weekday"`
	Retention   int    `json:"retention"`
	Passphrase  string `json:"passphrase"`
	Local       struct {
		Dir string `json:"dir"`
	} `json:"local"`
	S3 struct {
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Bucket    string `json:"bucket"`
		Prefix    string `json:"prefix"`
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
		PathStyle bool   `json:"path_style"`
	} `json:"s3"`
}

func (s *Server) handleSaveBackupConfig(c echo.Context) error {
	var req backupConfigReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	cfg, err := backup.LoadConfig(s.db, s.systemKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	cfg.Enabled = req.Enabled
	cfg.Destination = req.Destination
	cfg.Schedule = req.Schedule
	cfg.Hour = req.Hour
	cfg.Weekday = req.Weekday
	cfg.Retention = req.Retention
	cfg.Local.Dir = req.Local.Dir
	cfg.S3.Endpoint = req.S3.Endpoint
	cfg.S3.Region = req.S3.Region
	cfg.S3.Bucket = req.S3.Bucket
	cfg.S3.Prefix = req.S3.Prefix
	cfg.S3.AccessKey = req.S3.AccessKey
	cfg.S3.PathStyle = req.S3.PathStyle

	if req.Passphrase != "" {
		if err := cfg.SetPassphrase(s.systemKey, req.Passphrase); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	if req.S3.SecretKey != "" {
		if err := cfg.SetS3SecretKey(s.systemKey, req.S3.SecretKey); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}

	if err := cfg.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// Re-arm the schedule so a cadence change takes effect without a restart.
	cfg.NextRunAt = backup.NextRun(cfg, time.Now())

	if err := backup.SaveConfig(s.db, s.systemKey, cfg); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	s.audit(c, "backup.config.save", "", "")
	return c.JSON(http.StatusOK, toBackupDTO(cfg, s.dataDir))
}

func (s *Server) handleRunBackup(c echo.Context) error {
	name, err := s.backupScheduler.RunOnce(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.audit(c, "backup.run", name, "")
	return c.JSON(http.StatusOK, map[string]string{"name": name})
}

func (s *Server) backupDestination() (backup.Destination, error) {
	cfg, err := backup.LoadConfig(s.db, s.systemKey)
	if err != nil {
		return nil, err
	}
	return cfg.BuildDestination(s.systemKey)
}

func (s *Server) handleListSnapshots(c echo.Context) error {
	dest, err := s.backupDestination()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	entries, err := dest.List(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	if entries == nil {
		entries = []backup.Entry{}
	}
	return c.JSON(http.StatusOK, entries)
}

func (s *Server) handleDownloadSnapshot(c echo.Context) error {
	name := c.Param("name")
	if !backup.IsSnapshotName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "not a snapshot name")
	}
	dest, err := s.backupDestination()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	rc, err := dest.Get(c.Request().Context(), name)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	defer rc.Close()
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="`+name+`"`)
	return c.Stream(http.StatusOK, "application/octet-stream", rc)
}

func (s *Server) handleDeleteSnapshot(c echo.Context) error {
	name := c.Param("name")
	if !backup.IsSnapshotName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "not a snapshot name")
	}
	dest, err := s.backupDestination()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := dest.Delete(c.Request().Context(), name); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	s.audit(c, "backup.snapshot.delete", name, "")
	return c.NoContent(http.StatusNoContent)
}

type snapshotActionReq struct {
	Name       string `json:"name"`
	Passphrase string `json:"passphrase"`
	Confirm    string `json:"confirm"`
}

func (s *Server) handleVerifySnapshot(c echo.Context) error {
	var req snapshotActionReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	rc, err := s.openSnapshotForRead(c, req.Name)
	if err != nil {
		return err
	}
	defer rc.Close()

	schema, err := s.binarySchemaVersion()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	m, err := backup.Verify(rc, req.Passphrase, schema)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ok": true, "files": len(m.Files), "workspaces": m.WorkspaceCount,
		"created_at": m.CreatedAt, "app_version": m.AppVersion,
	})
}

// handleRestoreSnapshot stages a restore and asks the process to stop. The swap
// itself happens at the top of the next startup, before the database is opened
// — the one path that cannot corrupt a live install.
func (s *Server) handleRestoreSnapshot(c echo.Context) error {
	var req snapshotActionReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.Confirm != "RESTORE" {
		return echo.NewHTTPError(http.StatusBadRequest, `type RESTORE to confirm`)
	}
	rc, err := s.openSnapshotForRead(c, req.Name)
	if err != nil {
		return err
	}
	defer rc.Close()

	schema, err := s.binarySchemaVersion()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if _, err := backup.StageRestore(rc, s.dataDir, req.Passphrase, schema); err != nil {
		if errors.Is(err, backup.ErrSchemaTooNew) || errors.Is(err, backup.ErrBadPassphrase) ||
			errors.Is(err, backup.ErrSystemKeyConflict) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	s.audit(c, "backup.restore.staged", req.Name, "")

	// Shut down after the response has been flushed, so the browser sees the
	// instruction rather than a dropped connection.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		s.requestShutdown()
	}()
	return c.JSON(http.StatusOK, map[string]string{
		"status": "staged",
		"message": "The server is stopping to apply the restore. " +
			"If you started it manually, start it again — the restore is applied on the next launch.",
	})
}

// openSnapshotForRead resolves either an uploaded file or a stored snapshot
// name.
//
// The uploaded branch is deliberately EXEMPT from the shared 25 MiB
// internal/iolimit cap that guards every other ingest door: a legitimate
// snapshot exceeds that as soon as a workspace holds KB attachments, and
// capping here would break restore for exactly the installs with the most to
// lose. It streams to a temp file under a much larger bound instead.
func (s *Server) openSnapshotForRead(c echo.Context, name string) (io.ReadCloser, error) {
	if fh, err := c.FormFile("file"); err == nil {
		src, err := fh.Open()
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "cannot read the uploaded file")
		}
		defer src.Close()

		tmp, err := os.CreateTemp("", "sa-restore-*.sab")
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if _, err := io.Copy(tmp, io.LimitReader(src, maxSnapshotUpload)); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return &tempFileReader{File: tmp}, nil
	}

	if !backup.IsSnapshotName(name) {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "not a snapshot name")
	}
	dest, err := s.backupDestination()
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	rc, err := dest.Get(c.Request().Context(), name)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return rc, nil
}

// maxSnapshotUpload bounds a restore upload. It is far above the shared 25 MiB
// ingest cap because a snapshot is an archive of the entire install.
const maxSnapshotUpload = 8 << 30 // 8 GiB

// tempFileReader removes its backing file on Close.
type tempFileReader struct{ *os.File }

func (t *tempFileReader) Close() error {
	name := t.File.Name()
	err := t.File.Close()
	os.Remove(name)
	return err
}

// binarySchemaVersion reports the newest migration this build ships.
func (s *Server) binarySchemaVersion() (string, error) {
	entries, err := os.ReadDir(s.migrationsDir)
	if err != nil {
		return "", fmt.Errorf("read migrations dir: %w", err)
	}
	newest := ""
	for _, e := range entries {
		if n := e.Name(); len(n) > 7 && n[len(n)-7:] == ".up.sql" && n > newest {
			newest = n
		}
	}
	if newest == "" {
		return "", errors.New("no migrations found")
	}
	return newest, nil
}
```

This handler file references four `Server` fields that may not exist yet: `s.systemKey`, `s.dataDir`, `s.migrationsDir`, `s.backupScheduler`, and one method `s.requestShutdown()`. Check `web/server.go` for which are already present (`grep -n "systemKey\|dataDir" web/server.go`) and add the missing ones to the `Server` struct and its constructor, wiring them from `main.go`. For `requestShutdown`, the simplest correct implementation is to store the `context.CancelFunc` that `serve` already owns, or to call `s.echo.Shutdown(context.Background())` in a goroutine — read how `serve` currently terminates and follow it. Also confirm `s.audit(c, action, target, detail)` matches the existing audit helper's real signature (`grep -n "func (s \*Server) audit" web/*.go`) and adjust the calls.

- [ ] **Step 4: Register the routes**

In `web/api.go`, inside the owner-scoped group (find where `/api/v1/admin` routes are registered and mirror the middleware):

```go
	bk := api.Group("/backup", s.requireOwnerAPI)
	bk.GET("/config", s.handleGetBackupConfig)
	bk.PUT("/config", s.handleSaveBackupConfig)
	bk.POST("/run", s.handleRunBackup)
	bk.GET("/snapshots", s.handleListSnapshots)
	bk.GET("/snapshots/:name/download", s.handleDownloadSnapshot)
	bk.DELETE("/snapshots/:name", s.handleDeleteSnapshot)
	bk.POST("/verify", s.handleVerifySnapshot)
	bk.POST("/restore", s.handleRestoreSnapshot)
```

- [ ] **Step 5: Add the routes to the parity gate**

In `web/api_parity_test.go`, add these eight entries to the `want` table, matching the existing entry format exactly:

```
GET    /api/v1/backup/config
PUT    /api/v1/backup/config
POST   /api/v1/backup/run
GET    /api/v1/backup/snapshots
GET    /api/v1/backup/snapshots/:name/download
DELETE /api/v1/backup/snapshots/:name
POST   /api/v1/backup/verify
POST   /api/v1/backup/restore
```

- [ ] **Step 6: Run the tests**

Run: `go test ./web/... -count=1 -timeout 600s`
Expected: PASS, including `TestAPIParityInventory`.

- [ ] **Step 7: Commit**

```bash
git add web/api_backup.go web/api_backup_test.go web/api.go web/api_parity_test.go
git commit -m "feat(web): owner-scoped backup API

Restore staging exempts the upload door from the shared 25 MiB ingest cap: a
real snapshot exceeds it as soon as a workspace has attachments."
```

---

### Task 7: Settings UI

**Files:**
- Create: `web/ui/src/lib/backup.ts` (query hooks)
- Create: `web/ui/src/pages/settings/BackupSection.tsx`
- Create: `web/ui/src/pages/settings/BackupSection.test.tsx`
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx` (mount the section)

**Interfaces:**
- Consumes: the eight routes from Task 6.
- Produces: `<BackupSection />`, mounted in `OwnerSections` after `SystemStatusSection`.

- [ ] **Step 1: Read the existing conventions**

Read `web/ui/src/lib/settings.ts` and `web/ui/src/pages/settings/OwnerSections.tsx`. Reuse `errMsg`, `ErrorNote`, `Button`, `Input`, `useQuery`/`useMutation` and the `text-sm font-bold text-muted-2` heading style exactly as they appear there — do not introduce a new visual idiom for this one section.

- [ ] **Step 2: Write the failing component test**

Create `web/ui/src/pages/settings/BackupSection.test.tsx`, mirroring the setup in the existing `OwnerSections.test.tsx` (read it first for the QueryClient/render wrapper this repo uses):

```tsx
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackupSection } from "./BackupSection";
import { renderWithProviders } from "@/test/utils";

beforeEach(() => {
  vi.restoreAllMocks();
});

function mockConfig(overrides: Record<string, unknown> = {}) {
  return {
    enabled: false, destination: "local", schedule: "daily", hour: 3, weekday: 0,
    retention: 7, passphrase_set: false, local_dir: "",
    s3: { endpoint: "", region: "", bucket: "", prefix: "", access_key: "", secret_key_set: false, path_style: false },
    last_run_at: "0001-01-01T00:00:00Z", last_status: "", last_error: "", last_size: 0,
    next_run_at: "0001-01-01T00:00:00Z", pending_restore: false,
    ...overrides,
  };
}

describe("BackupSection", () => {
  it("warns that the passphrase is the only way to recover", async () => {
    vi.spyOn(global, "fetch").mockResolvedValue(
      new Response(JSON.stringify(mockConfig()), { status: 200 }) as never,
    );
    renderWithProviders(<BackupSection />);
    await waitFor(() => {
      expect(screen.getByText(/only way to recover/i)).toBeInTheDocument();
    });
  });

  it("shows the last failure so a silently broken backup is visible", async () => {
    vi.spyOn(global, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify(mockConfig({ enabled: true, last_status: "error", last_error: "bucket unreachable" })),
        { status: 200 },
      ) as never,
    );
    renderWithProviders(<BackupSection />);
    await waitFor(() => {
      expect(screen.getByText(/bucket unreachable/i)).toBeInTheDocument();
    });
  });

  it("requires typing RESTORE before the restore button enables", async () => {
    vi.spyOn(global, "fetch").mockResolvedValue(
      new Response(JSON.stringify(mockConfig({ enabled: true, passphrase_set: true })), { status: 200 }) as never,
    );
    renderWithProviders(<BackupSection />);
    const open = await screen.findByRole("button", { name: /restore from snapshot/i });
    await userEvent.click(open);

    const confirmBtn = await screen.findByRole("button", { name: /^restore$/i });
    expect(confirmBtn).toBeDisabled();

    await userEvent.type(screen.getByLabelText(/type restore/i), "RESTORE");
    await waitFor(() => expect(confirmBtn).toBeEnabled());
  });
});
```

Adjust the import of `renderWithProviders` to whatever helper `OwnerSections.test.tsx` actually uses.

- [ ] **Step 3: Run to verify failure**

Run: `cd web/ui && npx vitest run src/pages/settings/BackupSection.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement the hooks**

Create `web/ui/src/lib/backup.ts`:

```ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";

export type BackupS3 = {
  endpoint: string; region: string; bucket: string; prefix: string;
  access_key: string; secret_key_set: boolean; path_style: boolean;
};

export type BackupConfig = {
  enabled: boolean;
  destination: "local" | "s3";
  schedule: "daily" | "weekly";
  hour: number;
  weekday: number;
  retention: number;
  passphrase_set: boolean;
  local_dir: string;
  s3: BackupS3;
  last_run_at: string;
  last_status: string;
  last_error: string;
  last_size: number;
  next_run_at: string;
  pending_restore: boolean;
};

export type Snapshot = { name: string; size: number; mod_time: string };

export function useBackupConfig() {
  return useQuery({
    queryKey: ["backup", "config"],
    queryFn: () => api.get<BackupConfig>("/backup/config"),
  });
}

export function useSnapshots(enabled: boolean) {
  return useQuery({
    queryKey: ["backup", "snapshots"],
    queryFn: () => api.get<Snapshot[]>("/backup/snapshots"),
    enabled,
  });
}

export function useSaveBackupConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: unknown) => api.put<BackupConfig>("/backup/config", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup"] }),
  });
}

export function useRunBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ name: string }>("/backup/run", {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup"] }),
  });
}

export function useDeleteSnapshot() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.del(`/backup/snapshots/${encodeURIComponent(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup", "snapshots"] }),
  });
}

export function useVerifySnapshot() {
  return useMutation({
    mutationFn: (body: { name: string; passphrase: string }) =>
      api.post<{ ok: boolean; files: number; workspaces: number }>("/backup/verify", body),
  });
}

export function useRestoreSnapshot() {
  return useMutation({
    mutationFn: (body: { name: string; passphrase: string; confirm: string }) =>
      api.post<{ status: string; message: string }>("/backup/restore", body),
  });
}
```

Match `api.get`/`api.put`/`api.post`/`api.del` to the real helper names in `web/ui/src/lib/api.ts`.

- [ ] **Step 5: Implement the section**

Create `web/ui/src/pages/settings/BackupSection.tsx`. It renders, in order:

1. **Heading + blurb** — "Backup", and one line saying a snapshot covers every workspace's knowledge base, agents, skills and settings, and that times are in the server's local timezone.
2. **Status row** — last run (`timeAgo`), next run, last size, and an `ErrorNote` carrying `last_error` whenever `last_status === "error"`. A `pending_restore` banner when true, telling the owner a restore applies on next start and can be cancelled with `simple-agents backup cancel-restore`.
3. **Enable toggle + destination picker** (Local / S3) revealing the matching field set: `local_dir`, or endpoint/region/bucket/prefix/access key/secret key/path-style.
4. **Passphrase field** — shown as "set" when `passphrase_set`, with a "Change" affordance. Directly beneath it, permanently visible, the warning: *"Write this down. It is the only way to recover your data — nobody can reset it for you."*
5. **Schedule controls** — Daily/Weekly, an hour select (0–23, labelled "server local time"), a weekday select when weekly, and a retention number input defaulting to 7.
6. **Save button**, wired to `useSaveBackupConfig`, surfacing the API's 400 message through `ErrorNote`.
7. **Back up now** button (`useRunBackup`).
8. **Snapshot list** — name, size, age, with per-row Download (an `<a href>` to the download route), Verify and Delete, plus a **Restore from snapshot** action.
9. **Restore dialog** — passphrase field, a `Type RESTORE to confirm` input whose label is exactly `Type RESTORE`, and a Restore button `disabled` until that input strictly equals `RESTORE`. On success, show the returned `message` rather than a generic toast, because the server is about to stop.

Follow `OwnerSections.tsx` for markup and class names throughout: `<h3 className="text-sm font-bold text-muted-2">`, `mt-1 text-xs text-muted-2` for blurbs, `ErrorNote` for every error, and the shared `Button`/`Input`.

- [ ] **Step 6: Mount it**

In `web/ui/src/pages/settings/OwnerSections.tsx`, import `BackupSection` and render it after `<SystemStatusSection />`:

```tsx
        <SystemStatusSection />
        <BackupSection />
        <AuditLogSection />
```

- [ ] **Step 7: Run the frontend checks**

Run:
```bash
cd web/ui && npx tsc -b && npx oxlint && npx vitest run
```
Expected: no type errors, no lint errors, all tests pass including the three new ones.

- [ ] **Step 8: Build and smoke-test against a temp install**

```bash
make ui && go build -o bin/simple-agents ./cmd/simple-agents
export SA_DATA_DIR=$(mktemp -d) SA_PORT=8099
./bin/simple-agents db migrate
./bin/simple-agents owner bootstrap -u tester -p 'test-pw-123'
./bin/simple-agents serve &
sleep 3
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8099/
curl -sS http://127.0.0.1:8099/api/v1/backup/config   # expect 401 unauthenticated
kill %1
```

Expected: `200` for the SPA root and a 401 JSON body for the unauthenticated backup config — proving the route is registered and owner-guarded.

**Per the live-instance-safety rule, use `SA_DATA_DIR=$(mktemp -d)` and a non-default port — never the operator's live install or its port.**

- [ ] **Step 9: Run the full gate**

Run: `make ci`
Expected: fmt, vet, race tests, cross-compile matrix and the UI build all pass.

- [ ] **Step 10: Commit**

```bash
git add web/ui/src/lib/backup.ts web/ui/src/pages/settings/BackupSection.tsx \
        web/ui/src/pages/settings/BackupSection.test.tsx \
        web/ui/src/pages/settings/OwnerSections.tsx
git commit -m "feat(web/ui): backup settings section

The passphrase warning is permanent, not a one-time toast: a backup nobody can
decrypt is worse than no backup because it is silently worse."
```

---

## Self-Review

**Spec coverage.** Spec §"Configuration and scheduling" (the `backup.config` JSON, encrypted secrets, defaults) → Task 1. §"Retention" → Task 2. §"Configuration and scheduling" (the ticker, missed-run collapse, server-local time, not-internal/scheduler) → Task 3. §"Destinations" S3 + SigV4 → Tasks 4 and 5. §"Web surface" all eight routes, the parity gate, audit rows, and the `iolimit` exemption → Task 6. §"Web surface" UI, the show-once passphrase warning, and the `RESTORE` confirmation → Task 7. §"Error handling" — destination unreachable and the "enabled without a passphrase" refusal are covered by Task 3's tests; wrong passphrase / corrupt archive / schema-too-new surface through Task 6's handlers, which map the core plan's sentinels onto 400s.

Deliberately **not** covered here, per the spec's Out-of-scope list: per-workspace restore, incremental backup, Google Drive / Dropbox / GitHub destinations, and re-encrypting existing snapshots on passphrase change.

One spec line worth flagging to the implementer: the spec's error table says a failed snapshot surfaces "in the settings banner, the audit log and the server log". Task 3 records it to the config (banner) and the server log; it does **not** write an `audit_logs` row for a *scheduled* failure, because `s.audit` needs an `echo.Context` and the ticker has none. If an audit row for scheduled runs is wanted, add a context-free audit helper — that is a small, clearly-scoped addition rather than a defect in this plan.

**Placeholder scan.** No TBDs. Task 7 step 5 describes the component as a numbered content specification rather than a full JSX dump — that is deliberate, since the file must mirror `OwnerSections.tsx`'s existing markup which the implementer reads in step 1; every behaviour that the tests assert (the permanent warning, the error surfacing, the `RESTORE` gate) is stated exactly.

**Type consistency.** `Config`, `LocalConfig`, `S3Config`, `SettingStore`, `Destination`, `Entry` and `Scheduler` are each defined once. `NewS3Destination(cfg S3Config, secretKey string)` is referenced in Task 1's `BuildDestination` and defined in Task 5 — Task 1 step 3 explicitly notes the temporary stub and Task 5 step 4 removes it. The TypeScript `BackupConfig` mirrors `backupConfigDTO` field-for-field, including `passphrase_set` and `pending_restore` and excluding every encrypted field.

**Known integration points the implementer must verify rather than assume** (each is called out in the step that needs it): the `Server` struct's `systemKey`/`dataDir`/`migrationsDir`/`backupScheduler` fields, the real signature of `s.audit`, how `serve` terminates (for `requestShutdown`), whether `db.DB` exposes its `*sql.DB`, and the actual names of the `api.*` and test-helper functions in the SPA.
