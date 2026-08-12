package coder

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/rookery-ai/rookery/internal/vault"
	"github.com/rookery-ai/rookery/internal/websearch"
)

// newWiringTestDB gives the test a real (temp-file) SQLite DB carrying the
// project's actual schema, so the secret really round-trips through
// storage+decryption rather than a mock.
func newWiringTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

const wiringTestSalt = "aabbccddeeff00112233445566778899" // 32 hex chars = 16 bytes

// TestChatCoderPicksUpStoredSearchKey proves the FULL wiring chain a chat
// surface exercises for Task 5: a SEARCH_KEY_BRAVE secret stored for a real
// workspace → a websearch.SecretLookup reading it back through the actual
// secrets service (the same shape as web's s.secretsLookup and the gateway's
// secretsLookup closure) → websearch.ResolveKeyEnv → coder.WithExtraEnv →
// buildHostTools's subprocessEnv → searchProviders(). This is what makes
// web_search in chat actually use the keyed provider instead of the keyless
// cascade; asserting only the /search-keys endpoint would leave this last
// mile unverified.
func TestChatCoderPicksUpStoredSearchKey(t *testing.T) {
	database := newWiringTestDB(t)
	const workspaceID = "ws-search-wiring"
	if err := database.CreateWorkspace(&db.Workspace{
		ID:          workspaceID,
		Name:        "wiring-test",
		SecretsSalt: wiringTestSalt,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	const masterPw = "correct horse battery staple"
	svc := secrets.New(database, workspaceID, masterPw, wiringTestSalt)
	const braveKey = "real-brave-api-key-value"
	if err := svc.Set(context.Background(), "SEARCH_KEY_BRAVE", braveKey); err != nil {
		t.Fatalf("store SEARCH_KEY_BRAVE: %v", err)
	}
	// Tavily deliberately left unset — ResolveKeyEnv must include only
	// configured keys, not pad the map with empties.

	// lookup has the exact signature both chat surfaces already use to resolve
	// their own provider's API key (web's s.secretsLookup / the gateway's
	// secretsLookup closure in cmd/rookery/main.go).
	lookup := func(ctx context.Context, workspaceID, name string) (string, error) {
		return secrets.New(database, workspaceID, masterPw, wiringTestSalt).Get(ctx, name)
	}

	searchEnv := websearch.ResolveKeyEnv(context.Background(), workspaceID, lookup)
	if len(searchEnv) != 1 {
		t.Fatalf("expected exactly one resolved search key (brave only), got %v", searchEnv)
	}
	if searchEnv["SEARCH_KEY_BRAVE"] != braveKey {
		t.Fatalf("resolved brave key = %q, want %q", searchEnv["SEARCH_KEY_BRAVE"], braveKey)
	}
	if _, ok := searchEnv["SEARCH_KEY_TAVILY"]; ok {
		t.Fatalf("tavily was never set; must not appear in the resolved env: %v", searchEnv)
	}

	// Now drive it through the exact coder modifier chat wires it with
	// (coder.WithExtraEnv(searchEnv)), and build the host tool set the same
	// way a chat turn does, to confirm subprocessEnv — which searchProviders()
	// reads — actually carries the key.
	vlt := vault.New(t.TempDir())
	root := vlt.Root(workspaceID)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("scaffold vault root: %v", err)
	}
	// WithAPIConfig mirrors coder.ForWorkspace building an api_kind coder — the
	// case this task actually fixes (the API-engine chat branch previously
	// called no WithExtraEnv at all).
	c := (&Coder{vlt: vlt}).
		WithAPIConfig("openai", "gpt-4o", "", "OPENAI_API_KEY").
		WithDir(root).
		WithExtraEnv(searchEnv)

	h := c.buildHostTools(workspaceID)
	if h.subprocessEnv["SEARCH_KEY_BRAVE"] != braveKey {
		t.Fatalf("hostToolSet.subprocessEnv[SEARCH_KEY_BRAVE] = %q, want %q (searchProviders() reads this map)",
			h.subprocessEnv["SEARCH_KEY_BRAVE"], braveKey)
	}

	// Chat's workDir == vault root, so includeExecTools is false here — confirm
	// web_search is still offered (it is a read-only tool, unlike run_script/
	// bash, which ARE gated by includeExecTools; see execute()'s dispatch).
	// Without this, the wiring below would be moot: the model could never
	// reach searchProviders() from chat in the first place.
	if h.includeExecTools {
		t.Fatal("test setup sanity check: expected includeExecTools=false to mirror chat (workDir==vaultRoot)")
	}
	if _, ok := findTool(h.tools(), "web_search"); !ok {
		t.Fatal("web_search must be offered even with includeExecTools=false (chat) — it is read-only, not exec-gated")
	}

	// And finally: the keyed provider is actually selected FIRST, ahead of the
	// keyless cascade — the observable behavior the whole feature exists for.
	providers := h.searchProviders()
	if len(providers) == 0 {
		t.Fatal("searchProviders() returned no providers")
	}
	if providers[0].Name() != "brave" {
		t.Fatalf("first search provider = %q, want %q (keyed provider must be tried before the keyless cascade)",
			providers[0].Name(), "brave")
	}
}

// TestChatCoderNoStoredKeyKeepsKeylessCascade is the control case: with no
// search key configured, ResolveKeyEnv yields an empty map and
// searchProviders() falls straight to the keyless cascade (first provider is
// NOT "brave"/"tavily") — proving the wiring is additive, never required.
func TestChatCoderNoStoredKeyKeepsKeylessCascade(t *testing.T) {
	database := newWiringTestDB(t)
	const workspaceID = "ws-search-wiring-empty"
	if err := database.CreateWorkspace(&db.Workspace{
		ID:          workspaceID,
		Name:        "wiring-test-empty",
		SecretsSalt: wiringTestSalt,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	lookup := func(ctx context.Context, workspaceID, name string) (string, error) {
		return secrets.New(database, workspaceID, "unused-pw", wiringTestSalt).Get(ctx, name)
	}
	searchEnv := websearch.ResolveKeyEnv(context.Background(), workspaceID, lookup)
	if len(searchEnv) != 0 {
		t.Fatalf("expected no resolved search keys, got %v", searchEnv)
	}

	vlt := vault.New(t.TempDir())
	root := vlt.Root(workspaceID)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("scaffold vault root: %v", err)
	}
	c := (&Coder{vlt: vlt}).WithAPIConfig("openai", "gpt-4o", "", "OPENAI_API_KEY").WithDir(root)
	h := c.buildHostTools(workspaceID)
	providers := h.searchProviders()
	if len(providers) == 0 {
		t.Fatal("searchProviders() returned no providers")
	}
	if providers[0].Name() == "brave" || providers[0].Name() == "tavily" {
		t.Fatalf("with no stored key, the keyed provider must not win; got first provider %q", providers[0].Name())
	}
}
