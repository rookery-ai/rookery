package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// A placeholder in a URL PATH cannot be optional. An absent argument substitutes to
// the empty string, so ".../mailFolders/{{parent_id}}/childFolders" renders as
// ".../mailFolders//childFolders" and the provider 404s.
//
// None of the existing hygiene tests can see this: the parameter IS referenced by
// the request, which is all TestOptionalParamsAreActuallyUsed checks. It shipped
// twice in one sitting — outlook_create_folder ("omit for a top-level folder") and
// onenote_list_pages ("omit for every page") — and in both cases the description
// promised behaviour the URL could never deliver.
//
// The fix is always to split into two actions, because the difference is the PATH
// rather than a value in it. That is the same reason calendar_create_event and
// calendar_create_all_day_event are separate.
func TestPathPlaceholdersAreRequiredArguments(t *testing.T) {
	ph := regexp.MustCompile(`\{\{([\w.]+)(\|escape)?\}\}`)
	r := loadRegistry(t)
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			var schema struct {
				Required []string `json:"required"`
			}
			if json.Unmarshal(a.Params, &schema) != nil {
				continue
			}
			required := map[string]bool{}
			for _, p := range schema.Required {
				required[p] = true
			}
			for _, m := range ph.FindAllStringSubmatch(a.Request.URL, -1) {
				name := m[1]
				// {{conn.x}} comes from the connection, not the model, and a
				// provider that declares it has already made it mandatory at
				// connect time.
				if strings.HasPrefix(name, "conn.") || required[name] {
					continue
				}
				t.Errorf("%s/%s: %q is optional but appears in the URL PATH — when omitted "+
					"the URL collapses to a double slash and the provider 404s. Split the "+
					"action in two instead.\n  url: %s", prov, a.Name, name, a.Request.URL)
			}
		}
	}
}

// A query value that is itself JSON is easy to get wrong and impossible for a
// response fixture to catch, because fixtures test what comes back rather than what
// goes out. Meta's insights time_range is the only one in the catalog.
func TestMetaTimeRangeRendersAsRealJSON(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotQuery = req.URL.Query().Get("time_range")
		w.Write([]byte(`{"data":[{"spend":"1.00"}]}`))
	}))
	defer srv.Close()

	reg := testRegistry(t)
	a, ok := reg.Action("meta_ads", "meta_ads_daily_insights")
	if !ok {
		t.Fatal("meta_ads_daily_insights missing")
	}
	a.Request.URL = srv.URL + "/insights"
	reg.actions["meta_ads"] = []Action{a}

	if _, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "meta_ads"}, "meta_ads_daily_insights",
		map[string]any{"account_id": "act_1", "since": "2026-08-01", "until": "2026-08-10"},
		Policy{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := `{"since":"2026-08-01","until":"2026-08-10"}`
	if gotQuery != want {
		t.Fatalf("time_range reached the provider as %q, want %q", gotQuery, want)
	}
	// And it must be JSON Meta can parse, not merely a string that looks like it.
	var probe map[string]string
	if err := json.Unmarshal([]byte(gotQuery), &probe); err != nil {
		t.Fatalf("time_range is not valid JSON: %v", err)
	}
}
