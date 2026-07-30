package backup

import (
	"testing"
	"time"
)

// TestSnapshotNameUsesRookeryPrefixAndRkbExtension pins the snapshot naming
// convention. Prune and both destinations filter on IsSnapshotName, so the
// prefix and extension are not cosmetic: a mismatch means real snapshots stop
// being listed, pruned or offered for restore.
//
// The predicate is deliberately strict on all three parts — prefix, timestamp
// layout and extension — because a destination may be a bucket or folder shared
// with other data, and a foreign file must never be listed, downloaded or
// deleted. The legacy naming is not accepted: the install is greenfield, so
// there is no old snapshot to stay compatible with, and a dual-form predicate
// would be a permanent compat wart in a codebase with zero external users.
func TestSnapshotNameUsesRookeryPrefixAndRkbExtension(t *testing.T) {
	ts := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)

	got := SnapshotName(ts)
	want := "rookery-20260729-030000.rkb"
	if got != want {
		t.Fatalf("SnapshotName = %q, want %q", got, want)
	}
	if !IsSnapshotName(got) {
		t.Fatalf("IsSnapshotName(%q) = false, want true", got)
	}

	for _, name := range []string{
		"backup-20260729-030000.rkb",      // right layout, foreign prefix
		"rookery-20260729-030000.tar",     // right prefix, wrong extension
		"rookery-2026-07-29.rkb",          // right prefix, wrong timestamp layout
		"rookery-20260729-030000.rkb.tmp", // a partial upload in progress
		"notes.rkb",                       // unrelated file that merely shares the extension
	} {
		if IsSnapshotName(name) {
			t.Errorf("IsSnapshotName(%q) = true, want false", name)
		}
	}
}
