package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMissingGrantedScopesFailsOpen(t *testing.T) {
	cases := []struct {
		name     string
		declared []string
		extra    map[string]string
		want     int
	}{
		{
			// The upgrade case, and the reason this fails open at all: every
			// connection made before scope capture has no recorded grant. Reading
			// that as "granted nothing" would break every working install the
			// moment the first action declared a scope.
			name:     "no recorded grant is not a denial",
			declared: []string{"https://www.googleapis.com/auth/gmail.settings.basic"},
			extra:    map[string]string{},
			want:     0,
		},
		{
			name:     "action declaring nothing is unconstrained",
			declared: nil,
			extra:    map[string]string{"scope": "openid"},
			want:     0,
		},
		{
			name:     "granted scope passes",
			declared: []string{"https://www.googleapis.com/auth/gmail.readonly"},
			extra:    map[string]string{"scope": "openid https://www.googleapis.com/auth/gmail.readonly"},
			want:     0,
		},
		{
			name:     "ungranted scope is reported",
			declared: []string{"https://www.googleapis.com/auth/gmail.settings.basic"},
			extra:    map[string]string{"scope": "https://www.googleapis.com/auth/gmail.readonly"},
			want:     1,
		},
		{
			// Microsoft returns the short form on some endpoints and the qualified
			// form on others. Reporting a scope the user demonstrably granted would
			// send them to reconnect an account that is already correct.
			name:     "short and qualified spellings are the same grant",
			declared: []string{"https://graph.microsoft.com/Mail.Read"},
			extra:    map[string]string{"scope": "Mail.Read User.Read"},
			want:     0,
		},
		{
			name:     "comma-delimited grant strings parse",
			declared: []string{"tweet.write"},
			extra:    map[string]string{"scope": "tweet.read,tweet.write,users.read"},
			want:     0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := missingGrantedScopes(tc.declared, tc.extra); len(got) != tc.want {
				t.Fatalf("missingGrantedScopes = %v, want %d entries", got, tc.want)
			}
		})
	}
}

func TestCursorValueTreatsEveryAbsentSpellingAsNoNextPage(t *testing.T) {
	// A terminal page has at least four spellings across providers. Any of them read
	// as a live cursor would hand the model an envelope inviting it to fetch a page
	// that does not exist — an infinite loop against the turn budget.
	for _, body := range []string{
		`{"files":[{"id":"1"}]}`,
		`{"files":[],"nextPageToken":null}`,
		`{"files":[],"nextPageToken":""}`,
	} {
		if got := cursorValue("$.nextPageToken", []byte(body)); got != "" {
			t.Errorf("body %s: cursorValue = %q, want empty", body, got)
		}
	}
	if got := cursorValue("$.nextPageToken", []byte(`{"nextPageToken":"ABC"}`)); got != "ABC" {
		t.Errorf("cursorValue = %q, want ABC", got)
	}
	// An offset-based API answers with a number; it goes straight back out as a query
	// parameter, so its JSON text is the right thing to carry.
	if got := cursorValue("$.paging.next", []byte(`{"paging":{"next":100}}`)); got != "100" {
		t.Errorf("numeric cursor = %q, want 100", got)
	}
	// No declared cursor must never wrap.
	if got := cursorValue("", []byte(`{"nextPageToken":"ABC"}`)); got != "" {
		t.Errorf("undeclared cursor = %q, want empty", got)
	}
}

func TestExtractOKReportsAnUnresolvedPath(t *testing.T) {
	body := []byte(`{"data":{"children":[{"id":"a"}]}}`)
	if _, ok := extractOK("$.data.children", body); !ok {
		t.Error("a correct nested path should resolve")
	}
	// The failure this whole harness exists for: a path that looks right, returns the
	// entire document, and reports success everywhere else.
	got, ok := extractOK("$.data.items", body)
	if ok {
		t.Error("a wrong path must report unresolved")
	}
	if string(got) != string(body) {
		t.Error("an unresolved path must still degrade to the whole body at run time")
	}
}

// The envelope must appear ONLY when there is a next page, or every existing action's
// output shape changes and every consumer of a bare array breaks.
func TestPaginatedResultShapeIsStable(t *testing.T) {
	b, err := json.Marshal(paginatedResult{Items: json.RawMessage(`[1,2]`), NextCursor: "X"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"items":[1,2],"next_cursor":"X"}` {
		t.Fatalf("envelope shape changed: %s", b)
	}
}

// Every delete action in the catalog answers 204 with no body. The API engine
// special-cased that and the bridge did not — and marshaling an empty
// json.RawMessage fails, so a CLI coder received a broken body for a call that
// succeeded. The normalization has to happen where both kinds share it.
func TestExecuteNormalizesAnEmptySuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google_calendar", "calendar_delete_event")
	a.Request.URL = srv.URL + "/events/e1"
	reg.actions["google_calendar"] = []Action{a}

	res, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google_calendar"}, "calendar_delete_event",
		map[string]any{"calendar_id": "primary", "event_id": "e1"}, Policy{})
	if err != nil {
		t.Fatalf("a 204 is a success, got: %v", err)
	}
	if string(res.Data) != `{"ok":true}` {
		t.Fatalf("Data = %q, want {\"ok\":true}", res.Data)
	}
	// The actual regression: this is what the bridge does with the result.
	if _, err := json.Marshal(map[string]any{"data": res.Data}); err != nil {
		t.Fatalf("bridge cannot marshal the result: %v", err)
	}
}

func TestExecuteRejectsANonJSONBodyWithAnActionableMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`))
	}))
	defer srv.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/messages"
	reg.actions["google"] = []Action{a}

	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google"}, "gmail_search", map[string]any{"query": "hi"}, Policy{})
	ce, ok := err.(*ConnectorError)
	if !ok {
		t.Fatalf("expected a ConnectorError, got %v", err)
	}
	if !strings.Contains(ce.Msg, "non-JSON") {
		t.Fatalf("message should name the cause, got %q", ce.Msg)
	}
}

// The helper tests above prove the logic; these prove it is WIRED. A correct
// missingGrantedScopes that Execute never calls is exactly as useless as no check.

func TestExecuteGatesAnUngrantedScopeBeforeSpendingATokenRefresh(t *testing.T) {
	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Scopes = []string{"https://www.googleapis.com/auth/gmail.readonly"}
	reg.actions["google"] = []Action{a}

	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, http.DefaultClient,
		ConnRef{ID: "c1", Provider: "google", Extra: map[string]string{
			"scope": "https://www.googleapis.com/auth/userinfo.email",
		}}, "gmail_search", map[string]any{"query": "hi"}, Policy{})

	ce, ok := err.(*ConnectorError)
	if !ok || ce.Kind != KindNeedsReauth {
		t.Fatalf("expected KindNeedsReauth, got %v", err)
	}
	// The message is the entire value of the check: an opaque 403 is what we already
	// had. It must name the action, the scope and the fix.
	for _, want := range []string{"gmail_search", "gmail.readonly", "Reconnect"} {
		if !strings.Contains(ce.Msg, want) {
			t.Errorf("message %q does not mention %q", ce.Msg, want)
		}
	}
}

func TestExecuteWrapsAPageOnlyWhenACursorIsLive(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/messages"
	a.ResponseCursor = "$.nextPageToken"
	reg.actions["google"] = []Action{a}

	run := func() string {
		res, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
			ConnRef{ID: "c1", Provider: "google"}, "gmail_search", map[string]any{"query": "hi"}, Policy{})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return string(res.Data)
	}

	body = `{"messages":[{"id":"m1"}],"nextPageToken":"PAGE2"}`
	if got := run(); got != `{"items":[{"id":"m1"}],"next_cursor":"PAGE2"}` {
		t.Fatalf("paginated response not wrapped: %s", got)
	}

	// A terminal page must stay byte-identical to what it returns today, or every
	// pre-existing action's output shape changes the day it gains a cursor path.
	body = `{"messages":[{"id":"m1"}]}`
	if got := run(); got != `[{"id":"m1"}]` {
		t.Fatalf("terminal page should be bare, got: %s", got)
	}
}
