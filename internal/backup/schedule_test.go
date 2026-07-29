package backup

import (
	"context"
	"path/filepath"
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
	// Weekday 0 = Sunday.
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
	from := time.Date(2026, 7, 29, 10, 0, 0, 0, time.Local)
	c := &Config{Schedule: ScheduleWeekly, Hour: 20, Weekday: int(from.Weekday())}
	got := NextRun(c, from)
	want := time.Date(2026, 7, 29, 20, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A server down for a week must produce ONE snapshot on boot, not seven.
func TestMissedRunsCollapseToOne(t *testing.T) {
	c := &Config{Schedule: ScheduleDaily, Hour: 3}
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.Local)

	next := NextRun(c, now)
	if !next.After(now) {
		t.Fatalf("next run %v must be in the future", next)
	}
	if next.Sub(now) > 25*time.Hour {
		t.Fatalf("next run %v is too far out; missed slots must not accumulate", next)
	}
}

func TestSchedulerRunOnceWritesSnapshotAndPrunes(t *testing.T) {
	dataDir := t.TempDir()
	database, _ := newTestDB(t, dataDir)
	writeFile(t, filepath.Join(dataDir, "vaults", "ws1", "notes", "a.md"), "note")

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

	// Retention of 1 must leave exactly one snapshot after a second run.
	if _, err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	entries, _ := NewLocalDestination(destDir).List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("retention=1 left %d snapshots, want 1", len(entries))
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
