package agentstate

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func post(t *testing.T, url, token, body string) string {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestBridgeRejectsAnUnknownToken(t *testing.T) {
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	req, _ := http.NewRequest("POST", addr+"/state/get", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer nope")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBridgeRoundTripsState(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	post(t, addr+"/state/set", tok, `{"patch":{"seen":1}}`)
	if got := post(t, addr+"/state/get", tok, `{}`); !strings.Contains(got, "seen") {
		t.Fatalf("round trip lost state: %s", got)
	}
}

// A fresh agent — no state.md written yet — must read as empty-and-understood,
// not as an error. The bridge is a CLI coder's first door onto state for an
// agent that has never emitted [STATE], so this is the common case, not an
// edge one.
func TestBridgeGetOnFreshAgentIsEmptyAndUnderstood(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	got := post(t, addr+"/state/get", tok, `{}`)
	if !strings.Contains(got, `"understood":true`) {
		t.Fatalf("fresh agent must report understood=true: %s", got)
	}
	if strings.Contains(got, "seen") {
		t.Fatalf("fresh agent should have no state yet: %s", got)
	}
}

// Once a token is unregistered (the run ended) it must not keep working —
// otherwise a finished run's subprocess (or anything that captured the token)
// could keep reading/writing an agent's memory indefinitely.
func TestBridgeUnregisterRevokesTheToken(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	b.Unregister(tok)

	req, _ := http.NewRequest("POST", addr+"/state/get", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after Unregister", resp.StatusCode)
	}
}

// A second /state/set must MERGE onto the first, not replace it — the same
// contract agentstate.Apply gives the [STATE] marker and the API engine's
// set_state tool. Two doors that disagree here is exactly the bug this whole
// package exists to remove.
func TestBridgeSetMergesAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	post(t, addr+"/state/set", tok, `{"patch":{"seen":1}}`)
	post(t, addr+"/state/set", tok, `{"patch":{"other":2}}`)
	got := post(t, addr+"/state/get", tok, `{}`)
	if !strings.Contains(got, "seen") || !strings.Contains(got, "other") {
		t.Fatalf("second set replaced instead of merging: %s", got)
	}
}

// A null value in a patch deletes the key — the [STATE] marker's long-standing
// semantic, which agentstate.Merge implements and every door must honour
// identically.
func TestBridgeSetPatchDeletesOnNull(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	post(t, addr+"/state/set", tok, `{"patch":{"seen":1}}`)
	got := post(t, addr+"/state/set", tok, `{"patch":{"seen":null}}`)
	if strings.Contains(got, "seen") {
		t.Fatalf("nil patch value did not delete the key: %s", got)
	}
}

// A patch that would push state.md over agentstate.MaxStateSize is a request
// problem the CLI client can act on (shrink the payload), not a server fault —
// it must come back as a 400 with the underlying reason, never a 500.
func TestBridgeSetRejectsOversizedState(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	big := strings.Repeat("x", MaxStateSize+1)
	req, _ := http.NewRequest("POST", addr+"/state/set", strings.NewReader(`{"patch":{"blob":"`+big+`"}}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// 200 with an "error" field, matching the connector/MCP/KB bridges: a
	// refused call is not a broken bridge, so a CLI client should not have to
	// special-case the status code to see it — it prints the body either way.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 even for a refused patch", resp.StatusCode)
	}
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), `"error"`) {
		t.Fatalf("oversized patch was not reported as an error: %s", out)
	}
}

// A reply over the bridge's 8 KiB cap must be truncated rather than handed to
// the model whole — the same rule every other loopback bridge in the codebase
// enforces, and the state.md 64 KiB write limit does not save the reply from
// it (the bridge cap is tighter and independent).
func TestBridgeGetTruncatesAnOversizedReply(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	blob := strings.Repeat("y", maxBridgeResult+500)
	post(t, addr+"/state/set", tok, `{"patch":{"blob":"`+blob+`"}}`)
	got := post(t, addr+"/state/get", tok, `{}`)
	if !strings.Contains(got, `"truncated":true`) {
		t.Fatalf("oversized reply was not marked truncated: %.200s...", got)
	}
}

// Register scopes strictly by directory: two agents never see each other's
// state.md through the bridge, even from the same process.
func TestBridgeRegisterIsolatesAgentsByDirectory(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	// Both must actually exist, or the /state/set write fails before isolation
	// is even exercised — a mutation that ignores agentDir entirely would then
	// still pass, since neither write would ever land.
	if err := os.MkdirAll(dirA, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o750); err != nil {
		t.Fatal(err)
	}
	b := NewBridge()
	addr, _ := b.Start(t.Context())

	tokA := b.Register(dirA, "a")
	tokB := b.Register(dirB, "b")
	defer b.Unregister(tokA)
	defer b.Unregister(tokB)

	post(t, addr+"/state/set", tokA, `{"patch":{"only_in_a":1}}`)
	gotB := post(t, addr+"/state/get", tokB, `{}`)
	if strings.Contains(gotB, "only_in_a") {
		t.Fatalf("agent b's token read agent a's state: %s", gotB)
	}
}
