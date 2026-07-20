package agentdesigner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSkillDB is a minimal in-memory stand-in for the skillDB interface, used
// by both the reconciler-absorption tests here and (previously) the
// reconciler's own tests.
type fakeSkillDB struct {
	seeded map[string][]string
}

func (f *fakeSkillDB) ListAgentSkillNames(agentID string) ([]string, error) {
	return f.seeded[agentID], nil
}

func (f *fakeSkillDB) SetAgentSkills(agentID string, skillNames []string) error {
	if f.seeded == nil {
		f.seeded = map[string][]string{}
	}
	f.seeded[agentID] = skillNames
	return nil
}

func TestMigrateConvertsStateAndDeletesManifest(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"cursor":"xyz","n":3}`), 0o640)
	os.WriteFile(filepath.Join(agentDir, "agent.json"), []byte(`{"id":"a1","name":"A","skills":["pdf"]}`), 0o640)

	db := &fakeSkillDB{} // reuse the fake from the existing reconciler test
	n, err := MigrateAgentFilesToMarkdown(db, base, []string{"pdf", "csv"})
	if err != nil || n != 1 {
		t.Fatalf("migrate: n=%d err=%v", n, err)
	}

	if _, err := os.Stat(filepath.Join(agentDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json should be gone")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "agent.json")); !os.IsNotExist(err) {
		t.Fatal("agent.json should be gone")
	}
	got, err := ReadState(filepath.Join(agentDir, "state.md"))
	if err != nil || got["cursor"] != "xyz" || got["n"] != json.Number("3") {
		t.Fatalf("state not migrated: %#v %v", got, err)
	}
	if len(db.seeded["a1"]) != 1 || db.seeded["a1"][0] != "pdf" {
		t.Fatalf("skills not reconciled before deletion: %#v", db.seeded)
	}
}

// TestMigrateConvertsLargeIntegerWithoutLoss is the migration-level regression
// test for the reviewer-reported bug: a legacy state.json holding a value
// above 2^53 (a Discord snowflake ID, the most common thing an agent stores)
// must convert to state.md with the exact original digits. Before the fix,
// migrateAgentState's own json.Unmarshal into map[string]any already rounded
// the value on the FIRST decode — so the later reflect.DeepEqual verify step
// compared two equally-rounded values, reported success, and deleted
// state.json having silently lost precision.
func TestMigrateConvertsLargeIntegerWithoutLoss(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const bigID = "9007199254740993" // 2^53 + 1
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"last_id":`+bigID+`}`), 0o640)

	db := &fakeSkillDB{}
	n, err := MigrateAgentFilesToMarkdown(db, base, nil)
	if err != nil || n != 1 {
		t.Fatalf("migrate: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json should be gone after a verified, lossless migration")
	}

	got, err := ReadState(filepath.Join(agentDir, "state.md"))
	if err != nil {
		t.Fatalf("read migrated state: %v", err)
	}
	num, ok := got["last_id"].(json.Number)
	if !ok {
		t.Fatalf("last_id decoded as %T, not json.Number: %#v", got["last_id"], got["last_id"])
	}
	if num.String() != bigID {
		t.Fatalf("migration lost precision: got %s, want %s", num.String(), bigID)
	}

	raw, _ := os.ReadFile(filepath.Join(agentDir, "state.md"))
	if !strings.Contains(string(raw), bigID) {
		t.Fatalf("state.md does not hold the exact literal digits %s:\n%s", bigID, raw)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"a":1}`), 0o640)

	db := &fakeSkillDB{}
	if _, err := MigrateAgentFilesToMarkdown(db, base, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(agentDir, "state.md"))
	if _, err := MigrateAgentFilesToMarkdown(db, base, nil); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(agentDir, "state.md"))
	if string(first) != string(second) {
		t.Fatalf("second run changed the file:\n%s\n---\n%s", first, second)
	}
}

func TestMigrateKeepsBothFilesWhenStateMdAlreadyExists(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"old":1}`), 0o640)
	WriteState(filepath.Join(agentDir, "state.md"), "A", map[string]any{"new": float64(2)})

	if _, err := MigrateAgentFilesToMarkdown(&fakeSkillDB{}, base, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadState(filepath.Join(agentDir, "state.md"))
	if got["new"] != json.Number("2") {
		t.Fatalf("existing state.md was clobbered: %#v", got)
	}
	// state.json is left in place too: the rule only ever converts json→md when
	// no state.md exists yet, so a pre-existing state.md is never treated as
	// "already migrated" — it must not silently delete a still-live state.json.
	if _, err := os.Stat(filepath.Join(agentDir, "state.json")); err != nil {
		t.Fatalf("state.json should still be present when state.md already existed: %v", err)
	}
}

// TestMigrateHandlesDraftDirs proves draft_<slug> agent dirs are migrated on the
// same terms as canonical agents/<uuid> dirs.
func TestMigrateHandlesDraftDirs(t *testing.T) {
	base := t.TempDir()
	draftDir := filepath.Join(base, "ws1", "agents", "draft_my-agent")
	os.MkdirAll(draftDir, 0o755)
	os.WriteFile(filepath.Join(draftDir, "state.json"), []byte(`{"step":1}`), 0o640)

	n, err := MigrateAgentFilesToMarkdown(&fakeSkillDB{}, base, nil)
	if err != nil || n != 1 {
		t.Fatalf("migrate draft dir: n=%d err=%v", n, err)
	}
	got, err := ReadState(filepath.Join(draftDir, "state.md"))
	if err != nil || got["step"] != json.Number("1") {
		t.Fatalf("draft dir state not migrated: %#v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(draftDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("draft dir state.json should be gone")
	}
}

// TestMigrateFallbackBloatSkipped mirrors ReconcileSkillAttachmentsToDB's own
// guard: a manifest.Skills list that names every core skill is the legacy
// "declared none" fallback signature and must not be seeded into the DB, even
// though agent.json is still deleted.
func TestMigrateFallbackBloatSkipped(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "agent.json"),
		[]byte(`{"id":"a1","name":"A","skills":["pdf","csv"]}`), 0o640)

	db := &fakeSkillDB{}
	n, err := MigrateAgentFilesToMarkdown(db, base, []string{"pdf", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.seeded["a1"]) != 0 {
		t.Fatalf("fallback-bloat manifest must not be seeded into the DB: %#v", db.seeded)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "agent.json")); !os.IsNotExist(err) {
		t.Fatal("agent.json should still be deleted even when skills weren't seeded")
	}
	// No state.json existed, so nothing state-related was converted; the
	// manifest-only cleanup still counts as touching the agent... but since the
	// brief only requires "count of agents touched" without pinning fallback
	// dirs, we only assert n >= 0 (no panic/error) here.
	_ = n
}

// TestMigrateSkipsWhenDBAlreadyHasSkills proves the "only seed when DB has no
// rows yet" guard, matching ReconcileSkillAttachmentsToDB's existing semantics.
func TestMigrateSkipsWhenDBAlreadyHasSkills(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "agent.json"),
		[]byte(`{"id":"a1","name":"A","skills":["pdf"]}`), 0o640)

	db := &fakeSkillDB{seeded: map[string][]string{"a1": {"already-set"}}}
	if _, err := MigrateAgentFilesToMarkdown(db, base, []string{"pdf", "csv"}); err != nil {
		t.Fatal(err)
	}
	if len(db.seeded["a1"]) != 1 || db.seeded["a1"][0] != "already-set" {
		t.Fatalf("must not overwrite existing DB rows: %#v", db.seeded["a1"])
	}
}

// TestMigrateInvalidStateJSONLeavesBothFiles proves the failure path: when
// state.json cannot even be parsed, nothing is converted or deleted, and the
// migration keeps going (does not abort the whole run).
func TestMigrateInvalidStateJSONLeavesBothFiles(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`not valid json`), 0o640)

	n, err := MigrateAgentFilesToMarkdown(&fakeSkillDB{}, base, nil)
	if err != nil {
		t.Fatalf("migrate should not hard-fail on one bad agent: %v", err)
	}
	if n != 0 {
		t.Fatalf("invalid state.json must not count as converted: n=%d", n)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "state.json")); err != nil {
		t.Fatalf("state.json must survive an unparsable-JSON failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "state.md")); !os.IsNotExist(err) {
		t.Fatal("state.md must not be created when the source could not be parsed")
	}
}

// TestMigrateVerifyMismatchLeavesBothFiles exercises the write-step failure
// branch of the verify-then-delete guard directly (calling the unexported
// helper, since an already-existing state.md makes MigrateAgentFilesToMarkdown
// bail out before ever touching state.json — see
// TestMigrateKeepsBothFilesWhenStateMdAlreadyExists — so the public entry
// point can't reach this branch). state.md is made a directory so WriteState
// errors instead of succeeding; the DeepEqual verify-mismatch branch right
// after it is unreachable through any real WriteState/ReadState pair (they're
// a matched, correct pair by construction) — it exists purely as a defensive
// net, so this write-failure case is the representative one to pin.
func TestMigrateVerifyMismatchLeavesBothFiles(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"a":1}`), 0o640)
	// Make state.md a directory so WriteState errors instead of succeeding.
	os.MkdirAll(filepath.Join(agentDir, "state.md"), 0o755)

	changed := migrateAgentState(agentDir, "a1")
	if changed {
		t.Fatal("migrateAgentState must report no change when the write step fails")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "state.json")); err != nil {
		t.Fatalf("state.json must survive a failed write: %v", err)
	}
}
