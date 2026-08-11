package backup

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

var errNoSetting = errors.New("setting not found")

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
		"enabled with no passphrase": {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 7},
		"s3 with no bucket":          {Enabled: true, Destination: DestS3, Schedule: ScheduleDaily, Retention: 7, EncryptedPassphrase: "x"},
		"hour out of range":          {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Hour: 24, Retention: 7, EncryptedPassphrase: "x"},
		"retention below one":        {Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily, Retention: 0, EncryptedPassphrase: "x"},
		"unknown schedule":           {Enabled: true, Destination: DestLocal, Schedule: "hourly", Retention: 7, EncryptedPassphrase: "x"},
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

func TestBuildDestinationLocalUsesTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	c := &Config{Destination: DestLocal}
	d, err := c.BuildDestination(dataDir, testKey())
	if err != nil {
		t.Fatalf("BuildDestination: %v", err)
	}
	want := "local:" + filepath.Join(dataDir, "backups")
	if d.Name() != want {
		t.Fatalf("got %q, want %q", d.Name(), want)
	}
}

// A local destination needs no configuration at all now, so an otherwise
// complete config must validate without one.
func TestConfigValidateAcceptsLocalWithNoDirectory(t *testing.T) {
	c := &Config{
		Enabled: true, Destination: DestLocal, Schedule: ScheduleDaily,
		Hour: 3, Retention: 7, EncryptedPassphrase: "x",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("a local destination needs no directory: %v", err)
	}
}

// An install configured before the folder field was removed still has
// {"local":{"dir":"/mnt/backups"}} in its stored config. It must parse, and the
// value must not come back to life. encoding/json ignores unknown keys, so this
// costs no migration code — but it is exactly the kind of thing someone
// "restores" by re-adding the field, so it is pinned.
func TestLoadConfigIgnoresALegacyLocalDirectory(t *testing.T) {
	store := newMemStore()
	raw := `{"enabled":true,"destination":"local","schedule":"daily","hour":3,` +
		`"retention":7,"encrypted_passphrase":"x","local":{"dir":"/mnt/backups"}}`
	if err := store.SetSystemSetting(SettingsKey, raw); err != nil {
		t.Fatal(err)
	}

	c, err := LoadConfig(store, testKey())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Retention != 7 || c.Destination != DestLocal {
		t.Fatalf("the rest of the config must survive: %+v", c)
	}

	dataDir := t.TempDir()
	d, err := c.BuildDestination(dataDir, testKey())
	if err != nil {
		t.Fatalf("BuildDestination: %v", err)
	}
	if strings.Contains(d.Name(), "/mnt/backups") {
		t.Fatalf("the legacy directory must not be honoured: %q", d.Name())
	}
}

func TestBuildDestinationS3NeedsSecret(t *testing.T) {
	c := &Config{Destination: DestS3, S3: S3Config{Bucket: "b", Region: "r", AccessKey: "AK"}}
	if _, err := c.BuildDestination(t.TempDir(), testKey()); err == nil {
		t.Fatal("an S3 destination without a stored secret key must fail loudly")
	}
}
