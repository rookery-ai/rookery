package agentrunner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentstate"
)

// The sequence a re-review reproduced, and the reason the guard consults
// `recovered` rather than only "did the file go empty".
//
// The agent rewrites state.md wholesale mid-turn — the mangle the self-heal
// exists for — and its new prose happens to quote a JSON payload. That is not
// contrived: a quoted API error is one of agentstate's own recovery fixtures.
// The recovery scan adopts the snippet, so the file is NOT empty, so a guard
// keyed only on emptiness lets it stand and the agent's real memory is
// destroyed — this branch's own thesis defect, arriving through its own fix.
func TestAProseSnippetCannotReplaceRealStateAfterAMangle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	if err := saveState(dir, "My Agent", map[string]interface{}{"cursor": "abc"}); err != nil {
		t.Fatal(err)
	}
	runStart := map[string]interface{}{"cursor": "abc"}

	mangled := "# State — My Agent\n\n## Notes\n\nLast API error: {\"error\":\"rate limited\"}\n"
	if err := os.WriteFile(path, []byte(mangled), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := applyAndSaveState(dir, "My Agent", runStart, nil, true); err != nil {
		t.Fatal(err)
	}

	got, _, _ := agentstate.Get(path)
	if got["cursor"] != "abc" {
		t.Fatalf("real state destroyed by an adopted prose snippet: %#v", got)
	}
	if got["error"] != nil {
		t.Errorf("a quoted API error became the agent's memory: %#v", got)
	}
}

// The complement, so the guard cannot be widened into blocking the thing it was
// built for: a run that STARTED with no state must still adopt memory stranded
// outside the fence. That is the hn-watch shape — the original bug.
func TestStrandedStateIsStillAdoptedWhenTheRunStartedEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")
	seed := "```json\n{}\n```\n{\"reported_ids\": [1, 2, 3]}\n"
	if err := os.WriteFile(path, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := applyAndSaveState(dir, "My Agent", map[string]interface{}{}, nil, true); err != nil {
		t.Fatal(err)
	}

	got, _, _ := agentstate.Get(path)
	if got["reported_ids"] == nil {
		t.Fatalf("stranded state was not adopted for a run that started empty: %#v", got)
	}
}
