package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These tests are the offline half of "the tool actually works". The catalog-hygiene
// tests already prove an action is well-FORMED (valid name, real schema, every declared
// parameter referenced by the request). What they cannot see is whether the action can
// succeed against the live provider — whether the connection's grant covers it, whether
// the response path narrows anything, whether page two is reachable. Those three are
// the failure modes that reach a user as "the tool is there and does nothing".

// fixtureDir holds one documented example response per action, captured from the
// provider's own API reference when the action was authored.
const fixtureDir = "testdata/responses"

func loadRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	return r
}

// An action's declared scopes must be a subset of what its provider actually asks for
// at consent, or the action is unreachable for every connection ever made.
//
// The comparison is against the CHILD's own default_scopes, not the auth parent's:
// buildConsentURL sends the child's scope list to the parent's authorize endpoint, so
// google_drive's grant is exactly google_drive's scopes. Reading the parent here would
// pass an action no google_drive connection can call.
func TestActionScopesAreDeclaredByTheProvider(t *testing.T) {
	r := loadRegistry(t)
	for _, name := range r.ProviderNames() {
		p, _ := r.ProviderByName(name)
		declared := map[string]bool{}
		for _, s := range p.DefaultScopes {
			declared[s] = true
			declared[scopeTail(s)] = true
		}
		for _, a := range r.Actions(name) {
			for _, s := range a.Scopes {
				if declared[s] || declared[scopeTail(s)] {
					continue
				}
				t.Errorf("%s/%s declares scope %q but the provider never requests it — "+
					"no connection could ever call this action", name, a.Name, s)
			}
		}
	}
}

// An action declaring a next-page cursor must give the model a way to USE it. Without a
// page-token parameter feeding the provider's own cursor query parameter, the envelope
// advertises a second page that cannot be fetched — worse than no pagination at all,
// because it reads as a capability.
func TestPaginatedActionsExposeAPageToken(t *testing.T) {
	r := loadRegistry(t)
	for _, name := range r.ProviderNames() {
		for _, a := range r.Actions(name) {
			if a.ResponseCursor == "" {
				continue
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if json.Unmarshal(a.Params, &schema) != nil {
				t.Errorf("%s/%s: params did not parse", name, a.Name)
				continue
			}
			found := false
			for prop := range schema.Properties {
				if strings.Contains(prop, "page") || strings.Contains(prop, "cursor") ||
					strings.Contains(prop, "offset") || strings.Contains(prop, "after") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s/%s returns a next_cursor but offers no page-token parameter — "+
					"the model is shown a page it cannot fetch", name, a.Name)
			}
		}
	}
}

// Tool-list size is a shared budget: one provider advertising eighty tools degrades the
// model's selection across every OTHER tool an agent holds, connector actions included.
// internal/mcp caps a server at MaxEnabledToolsPerServer (48) for exactly this reason,
// and the same physics applies here. A provider that wants more has actions too granular
// to be distinguishable, or wants to be two auth_parent children.
const maxActionsPerProvider = 48

func TestProviderActionCountStaysWithinBudget(t *testing.T) {
	r := loadRegistry(t)
	for _, name := range r.ProviderNames() {
		if n := len(r.Actions(name)); n > maxActionsPerProvider {
			t.Errorf("%s exposes %d actions, over the %d budget — split it into auth_parent "+
				"children or merge granular actions", name, n, maxActionsPerProvider)
		}
	}
}

// The one that catches a silently-dead action: run the declared response_extract against
// a real documented response and prove it NARROWS.
//
// extract returns the whole body unchanged when its path does not resolve, which is the
// right run-time behaviour and a terrible authoring experience — the YAML reads fine,
// every other test passes, and the only symptom is a truncated blob against the bridge's
// 8 KiB cap. CLAUDE.md records this shipping twice ($.data.children, $.data.user),
// caught by accident both times. A fixture plus extractOK catches the next one here.
func TestResponseExtractResolvesAgainstItsFixture(t *testing.T) {
	r := loadRegistry(t)
	for _, name := range r.ProviderNames() {
		for _, a := range r.Actions(name) {
			raw, ok := readFixture(t, name, a.Name)
			if !ok {
				continue // fixtures are required for new actions only; see the coverage test
			}
			if !json.Valid(raw) {
				t.Errorf("%s/%s: fixture is not valid JSON", name, a.Name)
				continue
			}
			got, resolved := extractOK(a.ResponseExtract, raw)
			if !resolved {
				t.Errorf("%s/%s: response_extract %q does not resolve against the recorded "+
					"response — at run time this silently returns the WHOLE body",
					name, a.Name, a.ResponseExtract)
				continue
			}
			if s := strings.TrimSpace(string(got)); s == "" || s == "null" || s == "{}" || s == "[]" {
				t.Errorf("%s/%s: response_extract %q resolves but yields %s — the model gets nothing",
					name, a.Name, a.ResponseExtract, s)
			}
		}
	}
}

// A cursor path is exactly as silently-wrong as an extract path, and its fixture is
// already sitting there. A fixture recording a paginated response must resolve its
// cursor; one recording a terminal page legitimately has none, which is why an absent
// cursor key is not an error — only a path that cannot resolve against a body that
// clearly contains a cursor.
func TestResponseCursorResolvesWhenTheFixtureHasOne(t *testing.T) {
	r := loadRegistry(t)
	for _, name := range r.ProviderNames() {
		for _, a := range r.Actions(name) {
			if a.ResponseCursor == "" {
				continue
			}
			raw, ok := readFixture(t, name, a.Name)
			if !ok || !json.Valid(raw) {
				continue
			}
			// Only assert when the recorded response is a paginated one. The cursor key
			// is the last path segment; if the body does not mention it at all this is a
			// terminal page and there is nothing to check.
			key := a.ResponseCursor
			if i := strings.LastIndex(key, "."); i >= 0 {
				key = key[i+1:]
			}
			if !strings.Contains(string(raw), `"`+key+`"`) {
				continue
			}
			if cursorValue(a.ResponseCursor, raw) == "" {
				t.Errorf("%s/%s: fixture carries %q but response_cursor %q extracts nothing — "+
					"page two is unreachable", name, a.Name, key, a.ResponseCursor)
			}
		}
	}
}

// Fixture coverage is tracked rather than mandated. Backfilling all ~598 pre-existing
// actions is out of scope, but a provider this work TOUCHES must carry fixtures, or the
// expansion would add hundreds of actions with the extract path unverified — precisely
// the gap this harness exists to close.
//
// A provider joins the list when its actions are authored or revised. Removing a name to
// make the test pass re-opens the gap it was added to close.
var providersRequiringFixtures = []string{
	"google", "google_drive", "google_calendar", "google_sheets", "google_docs",
	"google_tasks", "youtube", "google_contacts", "google_chat", "google_slides",
	"google_forms",
	"outlook", "teams", "outlook_calendar", "outlook_contacts", "onedrive",
	"excel", "onenote", "microsoft_todo",
	"facebook", "instagram", "threads", "x", "pinterest", "reddit", "mastodon",
	"bluesky", "linkedin", "tiktok",
	"google_ads", "meta_ads", "google_analytics", "google_searchconsole",
	"google_adsense", "linkedin_ads",
	"github", "notion", "todoist", "trello", "slack",
}

func TestFixtureCoverageOfExpandedProviders(t *testing.T) {
	r := loadRegistry(t)
	for _, name := range providersRequiringFixtures {
		actions := r.Actions(name)
		if len(actions) == 0 {
			t.Errorf("%s is listed as requiring fixtures but has no actions", name)
			continue
		}
		var missing []string
		for _, a := range actions {
			// An action extracting "$" returns the whole body and cannot silently
			// fail to narrow, so a fixture would prove nothing. The requirement
			// tracks the actual risk: a dotted path, or a pagination cursor.
			if narrowing := strings.TrimSpace(a.ResponseExtract); narrowing == "" || narrowing == "$" {
				if a.ResponseCursor == "" {
					continue
				}
			}
			if _, ok := readFixture(t, name, a.Name); !ok {
				missing = append(missing, a.Name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s has %d/%d actions without a response fixture: %s",
				name, len(missing), len(actions), strings.Join(missing, ", "))
		}
	}
}

// readFixture returns the recorded response for an action, if one was captured.
func readFixture(t *testing.T, provider, action string) ([]byte, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, provider, action+".json"))
	if err != nil {
		return nil, false
	}
	return b, true
}

// A fixture that names no action is a fixture nobody checks — usually a rename that left
// the old file behind, which then hides the fact that the new action has none.
func TestNoOrphanedFixtures(t *testing.T) {
	r := loadRegistry(t)
	entries, err := os.ReadDir(fixtureDir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read %s: %v", fixtureDir, err)
	}
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		known := map[string]bool{}
		for _, a := range r.Actions(dir.Name()) {
			known[a.Name] = true
		}
		files, err := os.ReadDir(filepath.Join(fixtureDir, dir.Name()))
		if err != nil {
			t.Errorf("read %s: %v", dir.Name(), err)
			continue
		}
		for _, f := range files {
			name := strings.TrimSuffix(f.Name(), ".json")
			if !known[name] {
				t.Errorf("%s/%s.json has no matching action — a rename probably left it behind, "+
					"which hides the new action having no fixture", dir.Name(), f.Name())
			}
		}
	}
}
