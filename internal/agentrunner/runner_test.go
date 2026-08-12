package agentrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
)

// TestAgentStatePersistsAcrossRunsAsMarkdown proves the runner's state-loading
// path (agentdesigner.ReadState/WriteState) round-trips state through
// state.md rather than state.json — state.json must never be created.
func TestAgentStatePersistsAcrossRunsAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.md")
	if err := agentdesigner.WriteState(p, "T", map[string]any{"cursor": "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json must not be created any more")
	}
	got, err := agentdesigner.ReadState(p)
	if err != nil || got["cursor"] != "abc" {
		t.Fatalf("state not readable: %#v %v", got, err)
	}
}

// ─── saveState / mergeState (post-run save path) ──────────────────────────
//
// These pin the highest-risk part of SP7: the merge-and-save path a real run
// hits after every [STATE] update. state.json round-tripping (above) does not
// exercise saveState/mergeState at all, so a regression there (e.g. saveState
// clobbering hand-written "## Notes" prose, or mergeState failing to honor a
// null-delete) would previously go undetected.

// TestSaveStateFirstRunCreatesWellFormedDocument covers scenario 1: saving
// state onto a nonexistent state.md creates a well-formed document (heading +
// fenced json) and the state reads back through the public ReadState API.
func TestSaveStateFirstRunCreatesWellFormedDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	state := map[string]interface{}{"cursor": "abc", "count": float64(3)}
	if err := saveState(dir, "My Agent", state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state.md not created: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "# State — My Agent") {
		t.Fatalf("missing heading in fresh document:\n%s", text)
	}
	if !strings.Contains(text, "```json") {
		t.Fatalf("missing json fence in fresh document:\n%s", text)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got["cursor"] != "abc" || got["count"] != json.Number("3") {
		t.Fatalf("state did not read back: %#v", got)
	}
}

// TestSaveStatePreservesHandWrittenNotes covers scenario 2 — the one that
// matters most: a pre-existing state.md with a "## Notes" section containing
// hand-written prose must survive a save byte-for-byte, while only the fenced
// machine-state block changes.
func TestSaveStatePreservesHandWrittenNotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	notesBlock := "## Notes\n\nDo not touch the cursor manually — it tracks the last\nprocessed message ID from the source inbox.\n"
	seed := agentdesigner.RenderStateTemplate("My Agent", `{
  "cursor": "old-value"
}`) + "\n" + notesBlock
	if err := os.WriteFile(path, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := saveState(dir, "My Agent", map[string]interface{}{"cursor": "new-value"}); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	if !strings.Contains(text, notesBlock) {
		t.Fatalf("Notes section (heading + prose) was not preserved byte-for-byte.\ngot:\n%s", text)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got["cursor"] != "new-value" {
		t.Fatalf("fenced state was not updated: %#v", got)
	}
}

// TestMergeStateNullDeletesKey covers scenario 3: a [STATE] update whose value
// is nil removes that key from the merged state, leaving the surviving keys
// intact, and that merged result persists correctly through saveState.
func TestMergeStateNullDeletesKey(t *testing.T) {
	existing := map[string]interface{}{
		"cursor": "abc",
		"count":  float64(3),
		"stale":  "drop-me",
	}
	update := map[string]interface{}{
		"cursor": "def",
		"stale":  nil,
	}

	mergeState(existing, update)

	if _, ok := existing["stale"]; ok {
		t.Fatalf("stale key was not deleted: %#v", existing)
	}
	if existing["cursor"] != "def" {
		t.Fatalf("cursor not updated: %#v", existing)
	}
	if existing["count"] != float64(3) {
		t.Fatalf("unrelated key count was not preserved: %#v", existing)
	}

	// Persist the merged result and confirm it round-trips through the real
	// save path too — merge correctness alone doesn't guarantee the deletion
	// survives the write.
	dir := t.TempDir()
	if err := saveState(dir, "My Agent", existing); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got, err := agentdesigner.ReadState(filepath.Join(dir, "state.md"))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if _, ok := got["stale"]; ok {
		t.Fatalf("deleted key resurfaced after save/read round-trip: %#v", got)
	}
	if got["cursor"] != "def" {
		t.Fatalf("cursor not persisted: %#v", got)
	}
}

// TestStateMergePathPreservesLargeIntAndNullDelete drives the FULL live-run
// path a real [STATE] update actually takes: parseCoderOutput (the decode
// site closest to the coder's raw text) -> mergeState -> saveState ->
// ReadState. This is deliberately NOT the same as
// TestMergeStateNullDeletesKey, which hands mergeState a pre-built
// map[string]interface{} literal (still float64 in Go source) and so can
// never catch a decode-site regression. A coder emitting
// [STATE]{"last_id": 9007199254740993}[/STATE] — a 64-bit Discord snowflake,
// the single most common thing an agent stashes in state — must survive
// intact through every hop, and the merge's null-delete semantics (a key
// removed by a null value) must be unaffected by the json.Number change,
// since only numbers change decoded type; null still decodes to a plain nil
// interface.
func TestStateMergePathPreservesLargeIntAndNullDelete(t *testing.T) {
	dir := t.TempDir()
	const bigID = "9007199254740993" // 2^53 + 1

	// Seed existing state (as if from a prior run) with the large ID already
	// present, plus a key this turn's update will delete.
	existing := map[string]interface{}{}
	if err := saveState(dir, "My Agent", map[string]interface{}{
		"last_id": json.Number(bigID),
		"stale":   "drop-me",
	}); err != nil {
		t.Fatalf("seed saveState: %v", err)
	}
	seeded, err := agentdesigner.ReadState(filepath.Join(dir, "state.md"))
	if err != nil {
		t.Fatalf("seed ReadState: %v", err)
	}
	for k, v := range seeded {
		existing[k] = v
	}

	// Coder emits a [STATE] update: re-affirms the same large ID (as a live
	// coder would when re-emitting unchanged state) and nulls out "stale".
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT] processed",
		"[STATE]",
		`{"last_id": ` + bigID + `, "stale": null}`,
		"[/STATE]",
	}, "\n"))
	if len(out.stateUpdates) != 1 {
		t.Fatalf("expected exactly one state update: %+v", out.stateUpdates)
	}

	for _, update := range out.stateUpdates {
		mergeState(existing, update)
	}
	if _, ok := existing["stale"]; ok {
		t.Fatalf("null-value delete did not remove stale key: %#v", existing)
	}

	if err := saveState(dir, "My Agent", existing); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got, err := agentdesigner.ReadState(filepath.Join(dir, "state.md"))
	if err != nil {
		t.Fatalf("final ReadState: %v", err)
	}
	if _, ok := got["stale"]; ok {
		t.Fatalf("deleted key resurfaced after full merge/save/read round-trip: %#v", got)
	}
	num, ok := got["last_id"].(json.Number)
	if !ok {
		t.Fatalf("last_id decoded as %T, not json.Number: %#v", got["last_id"], got["last_id"])
	}
	if num.String() != bigID {
		t.Fatalf("large integer lost precision through the merge path: got %s, want %s", num.String(), bigID)
	}
}

// ─── applyAndSaveState (end-of-turn self-heal) ─────────────────────────────
//
// A prior version of the runner only called saveState when a turn's
// [STATE]updates were non-empty (`if len(parsed.stateUpdates) > 0 { ...
// saveState(...) }`). A turn where the coder overwrote state.md via a
// full-file write_file (e.g. editing "## Notes") and mangled or dropped the
// json fence WITHOUT emitting [STATE] that same turn hit no branch at all —
// the damage stood, and the next run's ReadState silently returned {}, total
// silent memory loss. applyAndSaveState is the fix: the runner now calls it
// on every turn regardless of whether that turn had updates, so a mangled
// fence self-heals from the state read at the top of the run. The one carve
// -out (stateReadOK) is for when that initial read itself failed — see the
// function's own doc comment.
//
// runCoderTurns's ACTUAL call to applyAndSaveState (runner.go, inside the
// turn loop) is exercised by driving a real coder.Coder.Generate call, which
// is out of scope here — these tests instead call applyAndSaveState directly,
// which is the same code path since the loop's own guard around it was
// removed entirely (the call is now an unconditional one-liner, verifiable by
// inspection of the diff rather than requiring a live coder harness).

// TestApplyAndSaveStateHealsManagedFenceWithNoUpdates covers Test 1: a run
// that emits NO [STATE] but whose state.md fence was mangled/removed mid-run
// (e.g. a full-file write_file editing "## Notes") leaves the file with a
// valid fence containing the PRE-RUN state — i.e. it self-heals instead of
// losing memory.
func TestApplyAndSaveStateHealsManagedFenceWithNoUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	// Seed a well-formed state.md as a prior run would have left it.
	if err := saveState(dir, "My Agent", map[string]interface{}{"cursor": "abc"}); err != nil {
		t.Fatalf("seed saveState: %v", err)
	}

	// The runner reads state at the START of the run, before the agent's
	// turns can touch the file.
	currentState, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("seed ReadState: %v", err)
	}
	if currentState["cursor"] != "abc" {
		t.Fatalf("seed state not as expected: %#v", currentState)
	}

	// Mid-run: the coder's write_file overwrites the whole document — e.g.
	// while editing "## Notes" — and in doing so drops the json fence
	// entirely. It does NOT emit [STATE] this turn.
	mangled := "# State — My Agent\n\n## Notes\n\nadded a note, oops no fence anymore\n"
	if err := os.WriteFile(path, []byte(mangled), 0o640); err != nil {
		t.Fatal(err)
	}

	// The runner calls this unconditionally at the end of the turn, even
	// though parsed.stateUpdates is empty this turn.
	if err := applyAndSaveState(dir, "My Agent", currentState, nil, true); err != nil {
		t.Fatalf("applyAndSaveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "```json") {
		t.Fatalf("fence was not restored:\n%s", string(raw))
	}
	// The Notes edit the agent made this turn must survive the heal.
	if !strings.Contains(string(raw), "added a note, oops no fence anymore") {
		t.Fatalf("agent's Notes edit was lost during the heal:\n%s", string(raw))
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState after heal: %v", err)
	}
	if got["cursor"] != "abc" {
		t.Fatalf("pre-run state not recovered after heal: %#v", got)
	}
}

// TestApplyAndSaveStateNoUpdatesIsByteIdenticalOnCanonicalFile covers half of
// Test 2 and confirms the "rewrite is content-identical on a well-formed
// file" claim: a no-update turn against a state.md already in the exact form
// WriteState itself produces (heading, italic intro, canonical fenced JSON)
// must leave the file byte-for-byte unchanged.
func TestApplyAndSaveStateNoUpdatesIsByteIdenticalOnCanonicalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	if err := saveState(dir, "My Agent", map[string]interface{}{"cursor": "abc", "count": float64(3)}); err != nil {
		t.Fatalf("seed saveState: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	currentState, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	if err := applyAndSaveState(dir, "My Agent", currentState, nil, true); err != nil {
		t.Fatalf("applyAndSaveState: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("no-op save on a canonical file was not byte-identical.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestApplyAndSaveStateNoUpdatesPreservesOutsideFenceContentByteForByte
// covers the other half of Test 2: a no-update turn against an untouched,
// well-formed state.md must not corrupt it, and everything OUTSIDE the fence
// (heading, italic intro, "## Notes" prose) survives byte-for-byte — even
// though the fenced JSON itself is hand-formatted with different key order
// and spacing than WriteState would produce, so it's expected to be
// re-serialized (sorted keys, canonical indent — assessed elsewhere in this
// work as functionally inert, since it doesn't change what the state means).
func TestApplyAndSaveStateNoUpdatesPreservesOutsideFenceContentByteForByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	notesBlock := "## Notes\n\nDo not touch the cursor manually — it tracks the last\nprocessed message ID from the source inbox.\n"
	intro := "*Managed by Rookery. The block below is this agent's memory between runs — edit it if you need to fix something by hand.*\n"
	heading := "# State — My Agent\n"
	// Hand-formatted fence: single line, keys in non-alphabetical order —
	// deliberately NOT what MarshalIndent would produce, so a byte-for-byte
	// check on the WHOLE file would be the wrong assertion here.
	seed := heading + "\n" + intro + "\n```json\n{\"stale\": \"drop-me\", \"cursor\": \"old-value\"}\n```\n" + notesBlock
	if err := os.WriteFile(path, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	currentState, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	if err := applyAndSaveState(dir, "My Agent", currentState, nil, true); err != nil {
		t.Fatalf("applyAndSaveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	if !strings.HasPrefix(text, heading+"\n"+intro+"\n") {
		t.Fatalf("heading/intro were not preserved byte-for-byte:\n%s", text)
	}
	if !strings.Contains(text, notesBlock) {
		t.Fatalf("Notes section was not preserved byte-for-byte:\n%s", text)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState after no-op save: %v", err)
	}
	if got["cursor"] != "old-value" || got["stale"] != "drop-me" {
		t.Fatalf("state content changed on a no-update save: %#v", got)
	}
}

// TestApplyAndSaveStateSkipsWriteWhenInitialReadFailed is the abort/failure-
// path guard: if the run's initial ReadState failed (a well-formed fence
// containing malformed JSON, or a genuine I/O error), currentState is a
// synthetic {} standing in for content we could never parse. A no-update turn
// must NOT write that {} back — doing so would silently replace
// hand-recoverable bad state with nothing, exactly the failure mode this fix
// exists to prevent, just moved up one level. The file must be left exactly
// as it was.
func TestApplyAndSaveStateSkipsWriteWhenInitialReadFailed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	// A well-formed fence (clean delimiters) whose JSON body does not parse —
	// findStateFence reports loc.OK (it only checks delimiters), but
	// ReadState's json.Decode fails, so ReadState returns a non-nil error.
	seed := "# State — My Agent\n\n```json\n{\"cursor\": \n```\n"
	if err := os.WriteFile(path, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := agentdesigner.ReadState(path); err == nil {
		t.Fatal("test setup invalid: expected ReadState to fail on malformed JSON in a well-formed fence")
	}

	// The runner's fallback for a failed read (runCoderAgent): stateMap
	// becomes {}, stateReadOK becomes false.
	currentState := map[string]interface{}{}

	if err := applyAndSaveState(dir, "My Agent", currentState, nil, false); err != nil {
		t.Fatalf("applyAndSaveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != seed {
		t.Fatalf("malformed state.md was overwritten despite a failed initial read.\nwant (unchanged):\n%s\ngot:\n%s", seed, raw)
	}
}

// TestApplyAndSaveStateExplicitUpdateAlwaysWritesEvenAfterFailedRead pins the
// other half of the stateReadOK contract: when the coder DOES emit an
// explicit [STATE] update this turn, the write always happens — even if the
// initial read had failed — because an explicit update is the agent
// deliberately establishing a new baseline, not an incidental no-op turn.
// This is the pre-existing behavior and must be unchanged by the fix.
func TestApplyAndSaveStateExplicitUpdateAlwaysWritesEvenAfterFailedRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	seed := "# State — My Agent\n\n```json\n{\"cursor\": \n```\n"
	if err := os.WriteFile(path, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	currentState := map[string]interface{}{} // the runner's failed-read fallback
	updates := []map[string]interface{}{{"cursor": "fresh-value"}}

	if err := applyAndSaveState(dir, "My Agent", currentState, updates, false); err != nil {
		t.Fatalf("applyAndSaveState: %v", err)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState after explicit update: %v", err)
	}
	if got["cursor"] != "fresh-value" {
		t.Fatalf("explicit [STATE] update was not written: %#v", got)
	}
}

// TestApplyAndSaveStateMergeAndNullDeleteUnchanged covers Test 3 through the
// NEW code path: the existing [STATE]-emitted merge behavior — including
// null-key deletion — is unchanged now that applyAndSaveState (not an inline
// block in runCoderTurns) is what the runner calls for every turn.
func TestApplyAndSaveStateMergeAndNullDeleteUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	if err := saveState(dir, "My Agent", map[string]interface{}{
		"cursor": "old-value",
		"stale":  "drop-me",
	}); err != nil {
		t.Fatalf("seed saveState: %v", err)
	}
	currentState, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("seed ReadState: %v", err)
	}

	updates := []map[string]interface{}{{"cursor": "new-value", "stale": nil}}
	if err := applyAndSaveState(dir, "My Agent", currentState, updates, true); err != nil {
		t.Fatalf("applyAndSaveState: %v", err)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState after update: %v", err)
	}
	if got["cursor"] != "new-value" {
		t.Fatalf("cursor not updated: %#v", got)
	}
	if _, ok := got["stale"]; ok {
		t.Fatalf("null-deleted key resurfaced: %#v", got)
	}
}

func TestParseCoderOutputChatBlankLineDoesNotDropContent(t *testing.T) {
	// Reproduces the real failure: the runtime emitted a header, a blank line,
	// then the actual content. The old parser ended the [CHAT] block at the
	// blank line and silently discarded the content. The whole message must
	// now be captured.
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT] 📝 Added a new test to your notes:",
		"",
		"**Soil Percolation Test** (Soil / site suitability test)",
		"A soil percolation test measures how quickly water drains through soil.",
		`[STATE]{"last_added_test": "Soil Percolation Test", "run_count": 1}[/STATE]`,
	}, "\n"))

	if len(out.chatLines) != 1 {
		t.Fatalf("expected 1 chat line, got %d: %q", len(out.chatLines), out.chatLines)
	}
	msg := out.chatLines[0]
	if !strings.Contains(msg, "Soil Percolation Test") {
		t.Errorf("content after blank line was dropped: %q", msg)
	}
	if !strings.Contains(msg, "percolation test measures") {
		t.Errorf("description was dropped: %q", msg)
	}
	if !strings.HasPrefix(msg, "📝 Added a new test to your notes:") {
		t.Errorf("header lost: %q", msg)
	}
	if len(out.stateUpdates) != 1 || out.stateUpdates[0]["last_added_test"] != "Soil Percolation Test" {
		t.Errorf("state not parsed: %+v", out.stateUpdates)
	}
}

func TestParseCoderOutputBareChatMarkerOwnLine(t *testing.T) {
	// Reproduces the real notion-porter-1 delivery failure: a weak model (qwen) put the
	// [CHAT] marker ALONE on its own line with the message on the FOLLOWING lines, then
	// closed with [STATE] and a trailing [SILENT]. The old parser required "[CHAT] " with an
	// inline space, so it never opened the block — chatLines stayed empty and [SILENT]
	// suppressed both the prose fallback and the empty-run warning: a silent non-delivery.
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT]",
		"✅ Ported 47 pages from Notion Personal to your knowledge base",
		"",
		"• notes/Personal/Personal.md",
		"• notes/Personal/Finance.md",
		`[STATE]{"last_ported": "2026-07-10T10:50:12Z"}[/STATE]`,
		"[SILENT]",
	}, "\n"))

	if len(out.chatLines) != 1 {
		t.Fatalf("expected 1 chat line from a bare [CHAT] marker, got %d: %q", len(out.chatLines), out.chatLines)
	}
	msg := out.chatLines[0]
	if !strings.HasPrefix(msg, "✅ Ported 47 pages") {
		t.Errorf("message on the line(s) after a bare [CHAT] marker was dropped: %q", msg)
	}
	if !strings.Contains(msg, "notes/Personal/Finance.md") {
		t.Errorf("file list was dropped: %q", msg)
	}
	if len(out.stateUpdates) != 1 {
		t.Errorf("state not parsed: %+v", out.stateUpdates)
	}
	// A real [CHAT] was captured, so delivery must NOT be suppressed by the trailing [SILENT]
	// (the runner only honors silent when chatLines is empty).
	if !out.silent {
		t.Errorf("the trailing [SILENT] should still be recorded (informational)")
	}
}

func TestParseCoderOutputChatSingleLine(t *testing.T) {
	out := parseCoderOutput("[CHAT] 💭 Stay curious — momentum favors the prepared.\n")
	if len(out.chatLines) != 1 || out.chatLines[0] != "💭 Stay curious — momentum favors the prepared." {
		t.Fatalf("expected single-line chat, got %q", out.chatLines)
	}
}

func TestParseCoderOutputMultipleChatBlocks(t *testing.T) {
	// A new [CHAT] marker starts a new block; the previous block is flushed.
	out := parseCoderOutput("[CHAT] first message\n[CHAT] second message\n")
	if len(out.chatLines) != 2 || out.chatLines[0] != "first message" || out.chatLines[1] != "second message" {
		t.Fatalf("expected two separate chat lines, got %q", out.chatLines)
	}
}

func TestParseCoderOutputStrayCloseTagStripped(t *testing.T) {
	// The [CHAT] protocol has no close tag, but weak models emit "[/CHAT]" anyway.
	// It must NEVER leak into the delivered message — in any of the common forms.
	t.Run("inline trailing close tag", func(t *testing.T) {
		out := parseCoderOutput("[CHAT] Here is your quote. [/CHAT]\n")
		if len(out.chatLines) != 1 || out.chatLines[0] != "Here is your quote." {
			t.Fatalf("inline [/CHAT] must be stripped; got %q", out.chatLines)
		}
	})
	t.Run("standalone close tag line", func(t *testing.T) {
		out := parseCoderOutput("[CHAT] Here is your quote.\n[/CHAT]\n")
		if len(out.chatLines) != 1 || out.chatLines[0] != "Here is your quote." {
			t.Fatalf("standalone [/CHAT] line must be dropped; got %q", out.chatLines)
		}
	})
	t.Run("multi-line block then close tag", func(t *testing.T) {
		out := parseCoderOutput(strings.Join([]string{
			"[CHAT] Line one of the message.",
			"Line two of the message.",
			"[/CHAT]",
		}, "\n"))
		if len(out.chatLines) != 1 {
			t.Fatalf("expected one chat block, got %q", out.chatLines)
		}
		if strings.Contains(out.chatLines[0], "[/CHAT]") {
			t.Fatalf("[/CHAT] leaked into message: %q", out.chatLines[0])
		}
		if !strings.Contains(out.chatLines[0], "Line two") {
			t.Fatalf("continuation line lost: %q", out.chatLines[0])
		}
	})
}

func TestParseCoderOutputStateEndsChatBlock(t *testing.T) {
	// A [STATE] block marker terminates an open [CHAT] block.
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT] done",
		"[STATE]",
		`{"k": 1}`,
		"[/STATE]",
	}, "\n"))
	if len(out.chatLines) != 1 || out.chatLines[0] != "done" {
		t.Fatalf("chat not flushed at [STATE]: %q", out.chatLines)
	}
	if len(out.stateUpdates) != 1 || out.stateUpdates[0]["k"] != json.Number("1") {
		t.Fatalf("state not parsed: %+v", out.stateUpdates)
	}
}

func TestParseCoderOutputSilentAgent(t *testing.T) {
	// No [CHAT] at all — a silent run. chatLines stays empty (valid).
	out := parseCoderOutput("[STATE]{\"ran\": true}[/STATE]\n")
	if len(out.chatLines) != 0 {
		t.Fatalf("silent run produced chat output: %q", out.chatLines)
	}
}

func TestParseCoderOutputCallAgent(t *testing.T) {
	out := parseCoderOutput("[CHAT] delegating\n[CALL: daily-digest]\n")
	if len(out.callAgents) != 1 || out.callAgents[0] != "daily-digest" {
		t.Fatalf("call agent not parsed: %+v", out.callAgents)
	}
	if len(out.chatLines) != 1 || out.chatLines[0] != "delegating" {
		t.Fatalf("chat before [CALL] lost: %q", out.chatLines)
	}
}

func TestParseCoderOutputSilentMarker(t *testing.T) {
	out := parseCoderOutput("[STATE]{\"ran\": true}[/STATE]\n[SILENT]\n")
	if !out.silent {
		t.Fatal("[SILENT] not detected")
	}
	if len(out.chatLines) != 0 {
		t.Fatalf("silent run produced chat: %q", out.chatLines)
	}
}

func TestParseCoderOutputEmptyChatDropped(t *testing.T) {
	// [CHAT] with only whitespace must not produce a blank delivered message.
	out := parseCoderOutput("[CHAT]   \n\n   \n")
	if len(out.chatLines) != 0 {
		t.Fatalf("empty [CHAT] delivered a blank message: %q", out.chatLines)
	}
}

func TestExtractProseMessageStripsMarkers(t *testing.T) {
	raw := strings.Join([]string{
		"Let me check the notes.",
		"[STATE]",
		`{"last_added": "A1C"}`,
		"[/STATE]",
		"Added Hemoglobin A1C to your notes.",
		"[CALL: digest]",
		"[SILENT]",
	}, "\n")
	prose := extractProseMessage(raw)
	if strings.Contains(prose, "[STATE]") || strings.Contains(prose, "last_added") {
		t.Errorf("state block leaked into prose: %q", prose)
	}
	if strings.Contains(prose, "[CALL:") || strings.Contains(prose, "[SILENT]") {
		t.Errorf("markers leaked into prose: %q", prose)
	}
	if !strings.Contains(prose, "Added Hemoglobin A1C to your notes.") {
		t.Errorf("real prose dropped: %q", prose)
	}
}

func TestExtractProseMessageEmptyWhenOnlyMarkers(t *testing.T) {
	raw := "[STATE]{\"k\":1}[/STATE]\n[SILENT]\n[CALL: x]\n"
	if extractProseMessage(raw) != "" {
		t.Fatalf("expected empty prose, got %q", extractProseMessage(raw))
	}
}

func TestExtractProseMessageStripsBlockedBlock(t *testing.T) {
	raw := "[BLOCKED]\nroot cause: bad\n[/BLOCKED]\nDone — fixed it."
	prose := extractProseMessage(raw)
	if strings.Contains(prose, "[BLOCKED]") || strings.Contains(prose, "root cause") {
		t.Errorf("blocked block leaked: %q", prose)
	}
	if !strings.Contains(prose, "Done — fixed it.") {
		t.Errorf("prose after blocked block dropped: %q", prose)
	}
}
