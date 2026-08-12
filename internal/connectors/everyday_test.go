package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// actionsOf returns a provider's actions keyed by name, failing the test if the
// provider did not load at all.
func actionsOf(t *testing.T, provider string) map[string]Action {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	acts := r.Actions(provider)
	if len(acts) == 0 {
		t.Fatalf("provider %q has no actions — did the manifest load?", provider)
	}
	out := map[string]Action{}
	for _, a := range acts {
		out[a.Name] = a
	}
	return out
}

func hasString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// requiredParams decodes an action's compiled schema and returns its required list.
func requiredParams(t *testing.T, a Action) []string {
	t.Helper()
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(a.Params, &schema); err != nil {
		t.Fatalf("%s params: %v", a.Name, err)
	}
	return schema.Required
}

// paramNames decodes an action's compiled schema and returns its property names.
func paramNames(t *testing.T, a Action) map[string]bool {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(a.Params, &schema); err != nil {
		t.Fatalf("%s params: %v", a.Name, err)
	}
	out := map[string]bool{}
	for k := range schema.Properties {
		out[k] = true
	}
	return out
}

func TestGoogleCalendarProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("google_calendar")
	if !ok {
		t.Fatal("google_calendar provider not loaded")
	}
	if p.AuthParent != "google" {
		t.Errorf("auth_parent = %q, want google — Calendar must reuse the Google OAuth app", p.AuthParent)
	}
	if p.Category != "Google" {
		t.Errorf("category = %q, want Google", p.Category)
	}
	// OAuthProvider must resolve to the parent, or consent has no endpoint.
	op, ok := r.OAuthProvider("google_calendar")
	if !ok || op.Name != "google" {
		t.Errorf("OAuthProvider = %v/%q, want google", ok, op.Name)
	}
	if len(p.DefaultScopes) == 0 {
		t.Error("no default_scopes — consent would request nothing")
	}

	acts := actionsOf(t, "google_calendar")
	for _, want := range []string{
		"calendar_list_calendars", "calendar_list_events", "calendar_get_event",
		"calendar_create_event", "calendar_update_event", "calendar_delete_event",
		"calendar_freebusy",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	// Writes must be marked, or the build-phase guard lets them fire during a build.
	for _, name := range []string{"calendar_create_event", "calendar_update_event", "calendar_delete_event"} {
		if !acts[name].Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}

	// list_events is the action most likely to blow the 8 KiB bridge cap, so it must
	// accept a bounded window rather than returning everything on the calendar.
	le := acts["calendar_list_events"]
	props := paramNames(t, le)
	for _, p := range []string{"time_min", "time_max", "max_results"} {
		if !props[p] {
			t.Errorf("calendar_list_events must accept %q to bound its result", p)
		}
	}
	for _, p := range []string{"time_min", "time_max"} {
		if !hasString(requiredParams(t, le), p) {
			t.Errorf("calendar_list_events must REQUIRE %q — an unbounded window is the failure mode", p)
		}
	}
	if le.ResponseExtract != "$.items" {
		t.Errorf("calendar_list_events response_extract = %q, want $.items", le.ResponseExtract)
	}
}

func TestGoogleTasksProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("google_tasks")
	if !ok {
		t.Fatal("google_tasks provider not loaded")
	}
	if p.AuthParent != "google" {
		t.Errorf("auth_parent = %q, want google", p.AuthParent)
	}
	if p.Category != "Google" {
		t.Errorf("category = %q, want Google", p.Category)
	}

	acts := actionsOf(t, "google_tasks")
	for _, want := range []string{
		"tasks_list_tasklists", "tasks_list_tasks", "tasks_create_task",
		"tasks_complete_task", "tasks_delete_task",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	for _, name := range []string{"tasks_create_task", "tasks_complete_task", "tasks_delete_task"} {
		if !acts[name].Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}
	if e := acts["tasks_list_tasks"].ResponseExtract; e != "$.items" {
		t.Errorf("tasks_list_tasks response_extract = %q, want $.items", e)
	}
}

func TestTodoistProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("todoist")
	if !ok {
		t.Fatal("todoist provider not loaded")
	}
	if !p.IsAPIKey() {
		t.Error("todoist should authenticate with a pasted personal token")
	}
	if p.Auth.Placement != "header" || p.Auth.ValuePrefix != "Bearer " {
		t.Errorf("auth = %s/%q, want header/\"Bearer \"", p.Auth.Placement, p.Auth.ValuePrefix)
	}
	if p.Category != "Productivity" {
		t.Errorf("category = %q, want Productivity", p.Category)
	}

	acts := actionsOf(t, "todoist")
	for _, want := range []string{
		"todoist_list_projects", "todoist_list_tasks", "todoist_create_task",
		"todoist_close_task", "todoist_update_task", "todoist_add_comment",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	for _, name := range []string{"todoist_create_task", "todoist_close_task", "todoist_update_task", "todoist_add_comment"} {
		if !acts[name].Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}
	// Todoist unified Sync and REST into API v1; a v2 URL is the likeliest slip.
	for name, a := range acts {
		if !strings.Contains(a.Request.URL, "api.todoist.com/api/v1") {
			t.Errorf("%s targets %q, want the unified api/v1 surface", name, a.Request.URL)
		}
	}
}

func TestYNABProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("ynab")
	if !ok {
		t.Fatal("ynab provider not loaded")
	}
	if !p.IsAPIKey() || p.Auth.ValuePrefix != "Bearer " {
		t.Errorf("auth = %+v, want an api_key with a Bearer prefix", p.Auth)
	}
	if p.Category != "Finance" {
		t.Errorf("category = %q, want Finance", p.Category)
	}

	acts := actionsOf(t, "ynab")
	for _, want := range []string{
		"ynab_list_budgets", "ynab_get_month_summary", "ynab_list_accounts",
		"ynab_list_transactions", "ynab_create_transaction", "ynab_list_categories",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	if !acts["ynab_create_transaction"].Mutating {
		t.Error("ynab_create_transaction must be mutating")
	}

	// Milliunits are the single most likely misreading of this API: without it in the
	// description, a $1.00 coffee is reported as $1,000.
	for _, name := range []string{"ynab_list_transactions", "ynab_create_transaction", "ynab_get_month_summary"} {
		if !strings.Contains(strings.ToLower(acts[name].Description), "milliunit") {
			t.Errorf("%s description must explain milliunits", name)
		}
	}

	// A budget's full transaction history is unbounded; since_date keeps it usable.
	if !hasString(requiredParams(t, acts["ynab_list_transactions"]), "since_date") {
		t.Error("ynab_list_transactions must require since_date to bound the result")
	}

	// YNAB nests every payload under "data" — these only narrow because extract()
	// walks dotted paths. Before that fix they silently returned the whole envelope.
	for name, want := range map[string]string{
		"ynab_list_budgets":      "$.data.budgets",
		"ynab_list_accounts":     "$.data.accounts",
		"ynab_list_transactions": "$.data.transactions",
		"ynab_list_categories":   "$.data.category_groups",
	} {
		if got := acts[name].ResponseExtract; got != want {
			t.Errorf("%s response_extract = %q, want %q", name, got, want)
		}
	}
}

func TestRaindropProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("raindrop")
	if !ok {
		t.Fatal("raindrop provider not loaded")
	}
	if !p.IsAPIKey() || p.Auth.ValuePrefix != "Bearer " {
		t.Errorf("auth = %+v, want an api_key with a Bearer prefix", p.Auth)
	}
	if p.Category != "Productivity" {
		t.Errorf("category = %q, want Productivity", p.Category)
	}

	acts := actionsOf(t, "raindrop")
	for _, want := range []string{
		"raindrop_list_collections", "raindrop_list_bookmarks", "raindrop_search",
		"raindrop_create_bookmark", "raindrop_update_bookmark",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	for _, name := range []string{"raindrop_create_bookmark", "raindrop_update_bookmark"} {
		if !acts[name].Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}
	// A collection can hold thousands of bookmarks; both list paths must be pageable.
	for _, name := range []string{"raindrop_list_bookmarks", "raindrop_search"} {
		if !paramNames(t, acts[name])["perpage"] {
			t.Errorf("%s must accept perpage to bound its result", name)
		}
	}
}

func TestHomeAssistantProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("home_assistant")
	if !ok {
		t.Fatal("home_assistant provider not loaded")
	}
	if p.Category != "Self-hosted" {
		t.Errorf("category = %q, want Self-hosted", p.Category)
	}
	if !p.IsAPIKey() || p.Auth.ValuePrefix != "Bearer " {
		t.Errorf("auth = %+v, want an api_key with a Bearer prefix", p.Auth)
	}

	// A self-hosted provider must collect a base URL and normalize it, or every
	// action template concatenates onto whatever shape the user happened to type.
	var baseURL *ConnectInput
	for i := range p.ConnectInputs {
		if p.ConnectInputs[i].Key == "base_url" {
			baseURL = &p.ConnectInputs[i]
		}
	}
	if baseURL == nil {
		t.Fatal("no base_url connect input")
	}
	if !baseURL.Required {
		t.Error("base_url must be required")
	}
	if baseURL.Normalize != "base_url" {
		t.Errorf("base_url normalize = %q, want base_url", baseURL.Normalize)
	}

	acts := actionsOf(t, "home_assistant")
	for _, want := range []string{
		"ha_list_states", "ha_get_state", "ha_call_service",
		"ha_list_services", "ha_get_history", "ha_fire_event",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	for _, name := range []string{"ha_call_service", "ha_fire_event"} {
		if !acts[name].Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}
	// Every action must template the per-connection base URL, not a literal host.
	for name, a := range acts {
		if !strings.Contains(a.Request.URL, "{{conn.base_url}}") {
			t.Errorf("%s URL = %q, want it to template {{conn.base_url}}", name, a.Request.URL)
		}
	}
	// History over an unbounded window truncates in the time dimension.
	req := requiredParams(t, acts["ha_get_history"])
	for _, want := range []string{"entity_id", "start_time"} {
		if !hasString(req, want) {
			t.Errorf("ha_get_history required = %v, want %q", req, want)
		}
	}
}

// GET /api/states returns EVERY entity in the house and Home Assistant offers no
// server-side filter, so entity_prefix is honoured client-side. If this regresses,
// ha_list_states silently returns the whole house and truncates against the 8 KiB cap.
func TestHomeAssistantListStatesFiltersClientSide(t *testing.T) {
	acts := actionsOf(t, "home_assistant")
	ls := acts["ha_list_states"]

	if !hasString(requiredParams(t, ls), "entity_prefix") {
		t.Error("ha_list_states must require entity_prefix")
	}
	f := ls.ResponseFilter
	if f.Field != "entity_id" || f.PrefixArg != "entity_prefix" {
		t.Fatalf("response_filter = %+v, want entity_id/entity_prefix", f)
	}

	raw := []byte(`[
		{"entity_id":"sensor.kitchen_temp","state":"21"},
		{"entity_id":"light.kitchen","state":"on"},
		{"entity_id":"sensor.hall_temp","state":"19"}
	]`)
	got := applyResponseFilter(raw, f, "sensor.")
	var out []map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("filter output: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("kept %d entities, want 2 sensors: %s", len(out), got)
	}
}

func TestImmichProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("immich")
	if !ok {
		t.Fatal("immich provider not loaded")
	}
	if p.Category != "Self-hosted" {
		t.Errorf("category = %q, want Self-hosted", p.Category)
	}
	if p.Auth.HeaderName != "x-api-key" || p.Auth.ValuePrefix != "" {
		t.Errorf("auth header = %q prefix %q, want x-api-key with no prefix", p.Auth.HeaderName, p.Auth.ValuePrefix)
	}

	acts := actionsOf(t, "immich")
	for _, want := range []string{
		"immich_search_assets", "immich_get_asset", "immich_list_albums",
		"immich_get_album", "immich_create_album", "immich_server_statistics",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	if !acts["immich_create_album"].Mutating {
		t.Error("immich_create_album must be mutating")
	}
	for name, a := range acts {
		if !strings.Contains(a.Request.URL, "{{conn.base_url}}") {
			t.Errorf("%s URL = %q, want it to template {{conn.base_url}}", name, a.Request.URL)
		}
	}
	// A library holds tens of thousands of assets; search must be capped, and its
	// nested extract only narrows because extract() walks dotted paths.
	if !paramNames(t, acts["immich_search_assets"])["size"] {
		t.Error("immich_search_assets must accept size to cap its result")
	}
	if got := acts["immich_search_assets"].ResponseExtract; got != "$.assets.items" {
		t.Errorf("immich_search_assets response_extract = %q, want $.assets.items", got)
	}
}

func TestPaperlessProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("paperless")
	if !ok {
		t.Fatal("paperless provider not loaded")
	}
	if p.Category != "Self-hosted" {
		t.Errorf("category = %q, want Self-hosted", p.Category)
	}
	// Paperless uses "Token ", not "Bearer " — the likeliest authoring slip.
	if p.Auth.ValuePrefix != "Token " {
		t.Errorf("auth value_prefix = %q, want \"Token \"", p.Auth.ValuePrefix)
	}

	acts := actionsOf(t, "paperless")
	for _, want := range []string{
		"paperless_search_documents", "paperless_get_document", "paperless_get_document_text",
		"paperless_list_tags", "paperless_list_correspondents", "paperless_update_document_tags",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	if !acts["paperless_update_document_tags"].Mutating {
		t.Error("paperless_update_document_tags must be mutating")
	}
	for name, a := range acts {
		if !strings.Contains(a.Request.URL, "{{conn.base_url}}") {
			t.Errorf("%s URL = %q, want it to template {{conn.base_url}}", name, a.Request.URL)
		}
	}
	if !paramNames(t, acts["paperless_search_documents"])["page_size"] {
		t.Error("paperless_search_documents must accept page_size to bound its result")
	}
	// The metadata and the OCR text are separate tools on the same endpoint: the two
	// together routinely exceed the 8 KiB cap, so the model picks the one it needs.
	if acts["paperless_get_document"].ResponseExtract == acts["paperless_get_document_text"].ResponseExtract {
		t.Error("get_document and get_document_text must extract different subtrees")
	}
}

func TestOpenMeteoProvider(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := r.ProviderByName("open_meteo")
	if !ok {
		t.Fatal("open_meteo provider not loaded")
	}
	if !p.IsKeyless() {
		t.Errorf("auth kind = %q, want none — Open-Meteo needs no credential", p.Auth.Kind)
	}
	if p.Category != "Data & Reference" {
		t.Errorf("category = %q, want Data & Reference", p.Category)
	}

	acts := actionsOf(t, "open_meteo")
	for _, want := range []string{
		"weather_geocode", "weather_forecast", "weather_current", "weather_air_quality",
	} {
		if _, ok := acts[want]; !ok {
			t.Errorf("missing action %q", want)
		}
	}
	// Nothing here writes anything.
	for name, a := range acts {
		if a.Mutating {
			t.Errorf("%s is marked mutating, but Open-Meteo is read-only", name)
		}
	}
	// CC BY 4.0 requires attribution, and the agent is what surfaces the forecast —
	// so the credit has to reach it through the tool description.
	for _, name := range []string{"weather_forecast", "weather_current", "weather_air_quality", "weather_geocode"} {
		if !strings.Contains(acts[name].Description, "Open-Meteo") {
			t.Errorf("%s description must carry the CC BY attribution", name)
		}
	}
	// Without geocoding, every other action needs coordinates the model does not have.
	if !hasString(requiredParams(t, acts["weather_geocode"]), "name") {
		t.Error("weather_geocode must require a place name")
	}
}

// Wave-1 providers are either live-verified or explicitly marked. This test does not
// judge which — it fails only if a wave-1 provider is neither, which is the state the
// spec's verification bar exists to prevent.
func TestWave1ProvidersDeclareVerificationStatus(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	// Verified live with cmd/livecheck against real credentials or a real endpoint.
	// Moving a provider OUT of this list means setting unverified: true in its YAML.
	verified := map[string]bool{
		"open_meteo": true,
	}
	wave1 := []string{
		"google_calendar", "google_tasks", "todoist", "ynab", "raindrop",
		"home_assistant", "immich", "paperless", "open_meteo",
	}
	for _, name := range wave1 {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("wave-1 provider %q not loaded", name)
			continue
		}
		if verified[name] && p.Unverified {
			t.Errorf("%s is listed as live-verified but marked unverified in its YAML", name)
		}
		if !verified[name] && !p.Unverified {
			t.Errorf("%s was not live-verified, so its YAML must set unverified: true", name)
		}
	}
}

// The whole point of the keyless kind is that an agent or chat can actually CALL the
// provider. Storage is not enough: a connection has to survive ActiveBoundConns and
// then produce tool definitions. Neither ActiveBoundConns nor ListServiceConnections
// filters on encrypted_access_token today — but ConnectionsNearExpiry DOES filter on
// exactly that shape for the refresh column, so the pattern exists in this package and
// nothing but this test would catch it being copied onto the access-token column.
//
// If this regresses, Open-Meteo connects, shows on the connections page, passes every
// other test here, and the model silently never sees a weather tool.
func TestKeylessConnectionIsExposedAsTools(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	// A keyless row exactly as connectAPIKeyCore writes it: ACTIVE, labelled after the
	// provider, and carrying NO credential.
	rows := []db.ServiceConnection{{
		ID: "c-keyless", WorkspaceID: "w1", Provider: "open_meteo",
		AccountLabel: "Open-Meteo", AccountIdentity: "Open-Meteo",
		EncryptedAccessToken: "", Status: "ACTIVE",
	}}

	bound := ActiveBoundConns(rows)
	if len(bound) != 1 {
		t.Fatalf("ActiveBoundConns dropped the credential-less connection: %+v", bound)
	}
	if bound[0].Provider != "open_meteo" {
		t.Fatalf("bound provider = %q, want open_meteo", bound[0].Provider)
	}

	defs := reg.ToolDefs(bound)
	got := map[string]bool{}
	for _, d := range defs {
		got[d.Name] = true
	}
	for _, want := range []string{"weather_geocode", "weather_forecast", "weather_current", "weather_air_quality"} {
		if !got[want] {
			t.Errorf("tool %q not exposed for a keyless connection; got %d tools", want, len(defs))
		}
	}
}

// ha_list_states end to end through the REAL Execute path, against a stub Home
// Assistant. This is the one action whose correctness depends on three things holding
// at once: validateArgs accepting a required param that appears in NO request template,
// renderRequest leaving that param in args, and applyResponseFilter reading it after
// the response comes back. Each was verified by reading render.go; this executes them.
func TestHomeAssistantListStatesFiltersThroughExecute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"entity_id":"sensor.kitchen_temp","state":"21"},
			{"entity_id":"light.kitchen","state":"on"},
			{"entity_id":"lock.front_door","state":"locked"},
			{"entity_id":"sensor.hall_temp","state":"19"}
		]`))
	}))
	defer srv.Close()

	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	// Point the shipped manifest's {{conn.base_url}} at the stub.
	ref := ConnRef{ID: "c1", Provider: "home_assistant", Extra: map[string]string{"base_url": srv.URL}}

	res, err := Execute(context.Background(), reg, keylessStore{}, nil, ref,
		"ha_list_states", map[string]any{"entity_prefix": "sensor."}, Policy{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var out []map[string]any
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("payload %s: %v", res.Data, err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d entities, want only the 2 sensors: %s", len(out), res.Data)
	}
	for _, e := range out {
		if !strings.HasPrefix(e["entity_id"].(string), "sensor.") {
			t.Errorf("leaked a non-sensor entity: %v", e)
		}
	}
}

// The same action with NO prefix argument must return everything rather than nothing.
// An empty result would read to the model as "you have no entities".
func TestHomeAssistantListStatesMissingPrefixReturnsAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"entity_id":"sensor.a"},{"entity_id":"light.b"}]`))
	}))
	defer srv.Close()

	reg, _ := LoadBundled()
	ref := ConnRef{ID: "c1", Provider: "home_assistant", Extra: map[string]string{"base_url": srv.URL}}

	// validateArgs rejects the call outright when a required arg is missing, which is
	// the correct guard — so exercise the filter's no-op path directly against the
	// same manifest config rather than through a call that cannot be made.
	acts := actionsOf(t, "home_assistant")
	raw := []byte(`[{"entity_id":"sensor.a"},{"entity_id":"light.b"}]`)
	got := applyResponseFilter(raw, acts["ha_list_states"].ResponseFilter, asString(map[string]any{}["entity_prefix"]))
	if string(got) != string(raw) {
		t.Errorf("filter = %s, want everything unchanged when the prefix is absent", got)
	}
	_ = reg
	_ = ref
}
