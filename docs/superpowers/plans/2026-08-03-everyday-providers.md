# Everyday Connector Providers (Wave 1) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship nine everyday-life connector providers — Google Calendar, Google Tasks, Todoist, YNAB, Raindrop.io, Home Assistant, Immich, Paperless-ngx and Open-Meteo — as ~50 curated typed actions.

**Architecture:** Every provider is two `go:embed`ed YAML files and one vendored SVG. `providers/<name>.yaml` declares auth, category and setup guidance; `connectors/<name>.yaml` declares the curated action manifest. `LoadBundled()` picks both up with no Go change. The only Go written in this plan is tests.

**Tech Stack:** YAML data files, Go tests, `scripts/vendor-brand-logos.sh`, `cmd/livecheck` for live verification.

## Prerequisite

**Plan 1 (`docs/superpowers/plans/2026-08-03-connector-framework.md`) must be merged first.** This plan depends on all of: the `none` auth kind, the four new categories (`Self-hosted`, `Health & Fitness`, `Finance`, `Data & Reference`), `ConnectInput.Normalize`, `NormalizeBaseURL`, the upstream logo source, `logocoverage.test.ts`, a committed `cmd/livecheck`, and — critically — **Plan 1 Task 10's fix to `extract` plus the new `ResponseFilter`**.

That last one is not optional. Before it, `extract` resolves only a **single top-level key**: `$.data.budgets` (YNAB) and `$.assets.items` (Immich) would silently return the entire raw payload, and Home Assistant's `ha_list_states` would have no way to filter at all. The failure is invisible in the YAML and shows up only as a truncated blob against the 8 KiB bridge cap on real data.

Plan 1 also lands two **auth-only fixture** providers — `open_meteo.yaml` and `immich.yaml` — with no action manifests. Tasks 9 and 7 below extend those files rather than creating them.

## Global Constraints

- **No new dependencies, and no Go production code.** A provider is data. If a provider seems to need a Go change, stop and raise it — that is a framework gap belonging in a Plan 1 follow-up, not here.
- **Branch, never commit to `main`.** Conventional Commits on every commit.
- **`make ci` must pass** before the PR.
- **Every action must narrow its output.** The connector bridge caps a result at 8 KiB (`maxBridgeResult`). `response_extract: "$"` is permitted only for a genuinely single-object response. Every list-shaped action must take a filter, cap or time-window parameter. This is a correctness requirement: an action that reliably truncates is an action the agent cannot use.
- **`mutating: true`** on anything that writes. **`public_write: true`** only for irreversible publishing to a public audience — none of the nine wave-1 providers has one (a Todoist task and a Paperless tag are private), and `TestPublicWriteImpliesMutating` enforces the pairing.
- **Setup steps must not say "shown above"** (`TestSetupStepsUsePlaceholderNotProse`), and an OAuth provider that is not an `auth_parent` child must name `{{redirect_uri}}` (`TestOAuthSetupStepsNameTheRedirectURI`).
- **Every provider slug needs a vendored logo** or `logocoverage.test.ts` fails. simple-icons **has**: `todoist`, `homeassistant`, `immich`, `paperlessngx`. simple-icons **lacks**: YNAB, Raindrop.io, Open-Meteo — those need the `upstream` source. Google Calendar and Google Tasks need their own marks; check lobehub and worldvectorlogo first.
- **Action names are the tool names** the model sees (`ToolDefs`). Prefix each with its service (`calendar_`, `todoist_`, `ynab_`, …) so a multi-provider workspace has no collisions.

---

### Task 1: Google Calendar

**Files:**
- Create: `internal/connectors/providers/google_calendar.yaml`
- Create: `internal/connectors/connectors/google_calendar.yaml`
- Create: `web/ui/src/assets/logos/google_calendar.svg`
- Test: `internal/connectors/everyday_test.go` (create)

**Interfaces:**
- Consumes: the `google` provider's OAuth app via `auth_parent`.
- Produces: seven actions — `calendar_list_calendars`, `calendar_list_events`, `calendar_get_event`, `calendar_create_event`, `calendar_update_event`, `calendar_delete_event`, `calendar_freebusy`.

**Why this is nearly free:** `web/handlers_services.go`'s `buildConsentURL` calls `oauth.ConsentURL(clientID, redirectURI, state, child.DefaultScopes)` — the **parent's** authorize endpoint and app credentials, the **child's own** scopes. So Calendar runs its own consent, stores its own token row with its own scopes, and existing Gmail connections need no re-consent. The one non-code prerequisite is that the Google Cloud Console OAuth app has the Calendar API enabled and its scopes registered.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/everyday_test.go`:

```go
package connectors

import (
	"encoding/json"
	"strings"
	"testing"
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
		if a := acts[name]; !a.Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}

	// list_events is the action most likely to blow the 8 KiB bridge cap, so it must
	// accept a bounded window rather than returning everything on the calendar.
	le := acts["calendar_list_events"]
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(le.Params, &schema); err != nil {
		t.Fatalf("calendar_list_events params: %v", err)
	}
	for _, p := range []string{"time_min", "time_max", "max_results"} {
		if _, ok := schema.Properties[p]; !ok {
			t.Errorf("calendar_list_events must accept %q to bound its result", p)
		}
	}
	if le.ResponseExtract == "$" {
		t.Error("calendar_list_events must narrow its response, not return the whole envelope")
	}
	if !strings.Contains(le.ResponseExtract, "items") {
		t.Errorf("response_extract = %q, want the items array", le.ResponseExtract)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestGoogleCalendarProvider -count=1`

Expected: FAIL — `google_calendar provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/google_calendar.yaml`:

```yaml
name: google_calendar
label: Google Calendar
category: Google
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/calendar.events
  - https://www.googleapis.com/auth/calendar.readonly
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Google Calendar reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, also enable the Google Calendar API."
  - "Then click Connect here to authorize Calendar access on the same Google account."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/google_calendar.yaml`:

```yaml
provider: google_calendar
actions:
  - name: calendar_list_calendars
    description: "List the calendars on this account, with their ids. Call this first — every other action needs a calendar_id, and 'primary' is the default one. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://www.googleapis.com/calendar/v3/users/me/calendarList"
    response_extract: "$.items"

  - name: calendar_list_events
    description: "List events in a time window. time_min/time_max are RFC3339 timestamps, e.g. 2026-08-03T00:00:00Z. Always pass a window — a calendar can hold years of events. Read-only."
    mutating: false
    params:
      type: object
      properties:
        calendar_id: {type: string, description: "calendar id, or 'primary'"}
        time_min:    {type: string, description: "RFC3339 lower bound, inclusive"}
        time_max:    {type: string, description: "RFC3339 upper bound, exclusive"}
        max_results: {type: integer, description: "cap on returned events, default 25"}
        query:       {type: string, description: "free-text search over event fields"}
      required: [calendar_id, time_min, time_max]
    request:
      method: GET
      url: "https://www.googleapis.com/calendar/v3/calendars/{{calendar_id|escape}}/events"
      query:
        timeMin: "{{time_min}}"
        timeMax: "{{time_max}}"
        maxResults: "{{max_results}}"
        q: "{{query}}"
        singleEvents: "true"
        orderBy: "startTime"
    response_extract: "$.items"

  - name: calendar_get_event
    description: "Get one event by id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        calendar_id: {type: string}
        event_id:    {type: string}
      required: [calendar_id, event_id]
    request:
      method: GET
      url: "https://www.googleapis.com/calendar/v3/calendars/{{calendar_id|escape}}/events/{{event_id}}"
    response_extract: "$"

  - name: calendar_create_event
    description: "Create an event. start/end are RFC3339 timestamps. attendees is an array of email addresses. Mutating."
    mutating: true
    params:
      type: object
      properties:
        calendar_id: {type: string}
        summary:     {type: string, description: "event title"}
        description: {type: string}
        location:    {type: string}
        start:       {type: string, description: "RFC3339 start"}
        end:         {type: string, description: "RFC3339 end"}
        attendees:   {type: array, items: {type: string}, description: "email addresses"}
      required: [calendar_id, summary, start, end]
    request:
      method: POST
      url: "https://www.googleapis.com/calendar/v3/calendars/{{calendar_id|escape}}/events"
      body:
        summary: "{{summary}}"
        description: "{{description}}"
        location: "{{location}}"
        start: {dateTime: "{{start}}"}
        end: {dateTime: "{{end}}"}
    response_extract: "$"

  - name: calendar_update_event
    description: "Update an event's fields by id (PATCH = partial: omitted fields are left alone). Mutating."
    mutating: true
    params:
      type: object
      properties:
        calendar_id: {type: string}
        event_id:    {type: string}
        summary:     {type: string}
        description: {type: string}
        location:    {type: string}
        start:       {type: string, description: "RFC3339 start"}
        end:         {type: string, description: "RFC3339 end"}
      required: [calendar_id, event_id]
    request:
      method: PATCH
      url: "https://www.googleapis.com/calendar/v3/calendars/{{calendar_id|escape}}/events/{{event_id}}"
      body:
        summary: "{{summary}}"
        description: "{{description}}"
        location: "{{location}}"
        start: {dateTime: "{{start}}"}
        end: {dateTime: "{{end}}"}
    response_extract: "$"

  - name: calendar_delete_event
    description: "Delete an event by id. Mutating and irreversible."
    mutating: true
    params:
      type: object
      properties:
        calendar_id: {type: string}
        event_id:    {type: string}
      required: [calendar_id, event_id]
    request:
      method: DELETE
      url: "https://www.googleapis.com/calendar/v3/calendars/{{calendar_id|escape}}/events/{{event_id}}"
    response_extract: "$"

  - name: calendar_freebusy
    description: "Query busy periods across one or more calendars in a window — use this to find a free slot rather than listing every event. Read-only."
    mutating: false
    params:
      type: object
      properties:
        calendar_ids: {type: array, items: {type: string}, description: "calendar ids to check"}
        time_min:     {type: string, description: "RFC3339 lower bound"}
        time_max:     {type: string, description: "RFC3339 upper bound"}
      required: [calendar_ids, time_min, time_max]
    request:
      method: POST
      url: "https://www.googleapis.com/calendar/v3/freeBusy"
      body:
        timeMin: "{{time_min}}"
        timeMax: "{{time_max}}"
    response_extract: "$.calendars"
```

**Note on `calendar_freebusy`:** its `items` field wants `[{"id": "..."}]`, and `renderBody` can substitute an array but cannot restructure one — the same limitation that forced the `ga4_report` body builder. Run the test in Step 5; if the freebusy body is rejected by the API during live verification (Task 10), the fix is a `body_builder`, which is a Plan 1 follow-up, not a change here. Until then the action works when the caller's default calendar set suffices.

**Note on `{{calendar_id|escape}}`:** a calendar id is an email-like string sitting in a path segment, so it opts into escaping — the same reason a Search Console site URL does. Event ids are opaque tokens and do not.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestGoogleCalendarProvider -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo**

Add a `google_calendar` entry to `scripts/vendor-brand-logos.sh`. Check `LOBEHUB` and `WVL` first; fall back to the `UPSTREAM` manifest with a pinned URL. Then:

```bash
./scripts/vendor-brand-logos.sh
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS, including `logocoverage.test.ts`.

- [ ] **Step 7: Run the full connectors package**

Run: `go test ./internal/connectors/ -count=1`

Expected: PASS — in particular `TestEveryProviderHasAValidCategory`, `TestSetupStepsUsePlaceholderNotProse` and `TestPublicWriteImpliesMutating`.

- [ ] **Step 8: Commit**

```bash
git add internal/connectors/providers/google_calendar.yaml \
  internal/connectors/connectors/google_calendar.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/google_calendar.svg
git commit -m "feat(connectors): add Google Calendar"
```

---

### Task 2: Google Tasks

**Files:**
- Create: `internal/connectors/providers/google_tasks.yaml`
- Create: `internal/connectors/connectors/google_tasks.yaml`
- Create: `web/ui/src/assets/logos/google_tasks.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: the `google` provider's OAuth app via `auth_parent`; `actionsOf` from Task 1.
- Produces: five actions — `tasks_list_tasklists`, `tasks_list_tasks`, `tasks_create_task`, `tasks_complete_task`, `tasks_delete_task`.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
		if a := acts[name]; !a.Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}
	if e := acts["tasks_list_tasks"].ResponseExtract; e != "$.items" {
		t.Errorf("tasks_list_tasks response_extract = %q, want $.items", e)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestGoogleTasksProvider -count=1`

Expected: FAIL — `google_tasks provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/google_tasks.yaml`:

```yaml
name: google_tasks
label: Google Tasks
category: Google
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/tasks
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Google Tasks reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, also enable the Google Tasks API."
  - "Then click Connect here to authorize Tasks access on the same Google account."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/google_tasks.yaml`:

```yaml
provider: google_tasks
actions:
  - name: tasks_list_tasklists
    description: "List the task lists on this account, with their ids. Call this first — every other action needs a tasklist_id. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://tasks.googleapis.com/tasks/v1/users/@me/lists"
    response_extract: "$.items"

  - name: tasks_list_tasks
    description: "List tasks in a list. show_completed defaults to false, so this returns outstanding work unless asked otherwise. Read-only."
    mutating: false
    params:
      type: object
      properties:
        tasklist_id:    {type: string}
        max_results:    {type: integer, description: "cap on returned tasks, default 25"}
        show_completed: {type: boolean, description: "include completed tasks"}
        due_min:        {type: string, description: "RFC3339 earliest due date"}
        due_max:        {type: string, description: "RFC3339 latest due date"}
      required: [tasklist_id]
    request:
      method: GET
      url: "https://tasks.googleapis.com/tasks/v1/lists/{{tasklist_id}}/tasks"
      query:
        maxResults: "{{max_results}}"
        showCompleted: "{{show_completed}}"
        dueMin: "{{due_min}}"
        dueMax: "{{due_max}}"
    response_extract: "$.items"

  - name: tasks_create_task
    description: "Create a task in a list. due is an RFC3339 timestamp; Google Tasks stores only the date part. Mutating."
    mutating: true
    params:
      type: object
      properties:
        tasklist_id: {type: string}
        title:       {type: string}
        notes:       {type: string}
        due:         {type: string, description: "RFC3339 due date"}
      required: [tasklist_id, title]
    request:
      method: POST
      url: "https://tasks.googleapis.com/tasks/v1/lists/{{tasklist_id}}/tasks"
      body:
        title: "{{title}}"
        notes: "{{notes}}"
        due: "{{due}}"
    response_extract: "$"

  - name: tasks_complete_task
    description: "Mark a task completed. Mutating."
    mutating: true
    params:
      type: object
      properties:
        tasklist_id: {type: string}
        task_id:     {type: string}
      required: [tasklist_id, task_id]
    request:
      method: PATCH
      url: "https://tasks.googleapis.com/tasks/v1/lists/{{tasklist_id}}/tasks/{{task_id}}"
      body:
        status: "completed"
    response_extract: "$"

  - name: tasks_delete_task
    description: "Delete a task by id. Mutating and irreversible."
    mutating: true
    params:
      type: object
      properties:
        tasklist_id: {type: string}
        task_id:     {type: string}
      required: [tasklist_id, task_id]
    request:
      method: DELETE
      url: "https://tasks.googleapis.com/tasks/v1/lists/{{tasklist_id}}/tasks/{{task_id}}"
    response_extract: "$"
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestGoogleTasksProvider -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo and run the package**

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/google_tasks.yaml \
  internal/connectors/connectors/google_tasks.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/google_tasks.svg
git commit -m "feat(connectors): add Google Tasks"
```

---

### Task 3: Todoist

**Files:**
- Create: `internal/connectors/providers/todoist.yaml`
- Create: `internal/connectors/connectors/todoist.yaml`
- Create: `web/ui/src/assets/logos/todoist.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: `actionsOf` from Task 1.
- Produces: six actions — `todoist_list_projects`, `todoist_list_tasks`, `todoist_create_task`, `todoist_close_task`, `todoist_update_task`, `todoist_add_comment`.

**Verified against live docs:** Todoist unified its Sync and REST APIs into **API v1** at `api.todoist.com/api/v1`. Auth is a bearer token — either a personal API token from Settings → Integrations → Developer, or an OAuth access token. simple-icons carries the `todoist` mark (`#E44332`).

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
		if a := acts[name]; !a.Mutating {
			t.Errorf("%s must be mutating", name)
		}
	}
	// The v1 API is the unified one; a v2 URL is the most likely authoring mistake.
	for name, a := range acts {
		if !strings.Contains(a.Request.URL, "api.todoist.com/api/v1") {
			t.Errorf("%s targets %q, want the unified api/v1 surface", name, a.Request.URL)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestTodoistProvider -count=1`

Expected: FAIL — `todoist provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/todoist.yaml`:

```yaml
name: todoist
label: Todoist
category: Productivity
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Todoist API token"
  key_hint: "from Settings → Integrations → Developer"
  setup_url: https://app.todoist.com/app/settings/integrations/developer
setup_steps:
  - "In Todoist open Settings → Integrations → Developer."
  - "Copy the API token shown there."
  - "Paste it below. The token is personal and does not expire until you reset it."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/todoist.yaml`:

```yaml
provider: todoist
actions:
  - name: todoist_list_projects
    description: "List projects with their ids. Call this first when a task needs a specific project. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://api.todoist.com/api/v1/projects"
    response_extract: "$.results"

  - name: todoist_list_tasks
    description: "List active (not completed) tasks. Narrow with project_id or a Todoist filter expression such as 'today | overdue' — an unfiltered account can hold hundreds of tasks. Read-only."
    mutating: false
    params:
      type: object
      properties:
        project_id: {type: string, description: "restrict to one project"}
        filter:     {type: string, description: "Todoist filter query, e.g. 'today | overdue'"}
        limit:      {type: integer, description: "cap on returned tasks, default 30"}
      required: []
    request:
      method: GET
      url: "https://api.todoist.com/api/v1/tasks"
      query:
        project_id: "{{project_id}}"
        filter: "{{filter}}"
        limit: "{{limit}}"
    response_extract: "$.results"

  - name: todoist_create_task
    description: "Create a task. due_string accepts natural language Todoist parses itself, e.g. 'tomorrow at 9am'. priority is 1 (normal) to 4 (urgent). Mutating."
    mutating: true
    params:
      type: object
      properties:
        content:     {type: string, description: "the task text"}
        description: {type: string}
        project_id:  {type: string}
        due_string:  {type: string, description: "natural language due date"}
        priority:    {type: integer, description: "1 normal … 4 urgent"}
      required: [content]
    request:
      method: POST
      url: "https://api.todoist.com/api/v1/tasks"
      body:
        content: "{{content}}"
        description: "{{description}}"
        project_id: "{{project_id}}"
        due_string: "{{due_string}}"
        priority: "{{priority}}"
    response_extract: "$"

  - name: todoist_close_task
    description: "Complete a task by id. Mutating."
    mutating: true
    params:
      type: object
      properties:
        task_id: {type: string}
      required: [task_id]
    request:
      method: POST
      url: "https://api.todoist.com/api/v1/tasks/{{task_id}}/close"
    response_extract: "$"

  - name: todoist_update_task
    description: "Update a task's fields by id. Omitted fields are left alone. Mutating."
    mutating: true
    params:
      type: object
      properties:
        task_id:     {type: string}
        content:     {type: string}
        description: {type: string}
        due_string:  {type: string, description: "natural language due date"}
        priority:    {type: integer, description: "1 normal … 4 urgent"}
      required: [task_id]
    request:
      method: POST
      url: "https://api.todoist.com/api/v1/tasks/{{task_id}}"
      body:
        content: "{{content}}"
        description: "{{description}}"
        due_string: "{{due_string}}"
        priority: "{{priority}}"
    response_extract: "$"

  - name: todoist_add_comment
    description: "Add a comment to a task. Mutating."
    mutating: true
    params:
      type: object
      properties:
        task_id: {type: string}
        content: {type: string}
      required: [task_id, content]
    request:
      method: POST
      url: "https://api.todoist.com/api/v1/comments"
      body:
        task_id: "{{task_id}}"
        content: "{{content}}"
    response_extract: "$"
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestTodoistProvider -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo and run the package**

`todoist` is in simple-icons, so add it to the `SIMPLE` manifest in `scripts/vendor-brand-logos.sh`.

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/todoist.yaml \
  internal/connectors/connectors/todoist.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/todoist.svg
git commit -m "feat(connectors): add Todoist"
```

---

### Task 4: YNAB

**Files:**
- Create: `internal/connectors/providers/ynab.yaml`
- Create: `internal/connectors/connectors/ynab.yaml`
- Create: `web/ui/src/assets/logos/ynab.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: `actionsOf` from Task 1.
- Produces: six actions — `ynab_list_budgets`, `ynab_get_month_summary`, `ynab_list_accounts`, `ynab_list_transactions`, `ynab_create_transaction`, `ynab_list_categories`.

**Notes:** YNAB issues a Personal Access Token from Account Settings → Developer Settings. It is the first provider in the `Finance` category. **Amounts are in milliunits** — 1,000 = one currency unit — which every action description must say, or the model reports a $1.00 coffee as $1,000. YNAB is subscription-only, so tier-A live verification may not be attainable; if not, mark it (Task 10).

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(acts["ynab_list_transactions"].Params, &schema); err != nil {
		t.Fatalf("params: %v", err)
	}
	if !contains(schema.Required, "since_date") {
		t.Errorf("ynab_list_transactions required = %v, want since_date to bound the result", schema.Required)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestYNABProvider -count=1`

Expected: FAIL — `ynab provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/ynab.yaml`:

```yaml
name: ynab
label: YNAB
category: Finance
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "YNAB personal access token"
  key_hint: "from Account Settings → Developer Settings"
  setup_url: https://app.ynab.com/settings/developer
setup_steps:
  - "In YNAB open Account Settings → Developer Settings."
  - "Under Personal Access Tokens press New Token, enter your password and press Generate."
  - "Copy the token and paste it below — YNAB shows it only once."
  - "The API is rate-limited to 200 requests per hour per token."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/ynab.yaml`:

```yaml
provider: ynab
actions:
  - name: ynab_list_budgets
    description: "List budgets with their ids. Call this first — every other action needs a budget_id, and 'last-used' works as a shorthand for the most recently opened budget. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://api.ynab.com/v1/budgets"
    response_extract: "$.data.budgets"

  - name: ynab_get_month_summary
    description: "Get one month's totals: budgeted, activity, to-be-budgeted and age-of-money. month is YYYY-MM-01 or 'current'. All amounts are in MILLIUNITS — divide by 1000 for the currency amount, so 1500 means 1.50. Read-only."
    mutating: false
    params:
      type: object
      properties:
        budget_id: {type: string, description: "budget id, or 'last-used'"}
        month:     {type: string, description: "YYYY-MM-01, or 'current'"}
      required: [budget_id, month]
    request:
      method: GET
      url: "https://api.ynab.com/v1/budgets/{{budget_id}}/months/{{month}}"
    response_extract: "$.data.month"

  - name: ynab_list_accounts
    description: "List accounts with balances. Balances are in MILLIUNITS — divide by 1000. Read-only."
    mutating: false
    params:
      type: object
      properties:
        budget_id: {type: string}
      required: [budget_id]
    request:
      method: GET
      url: "https://api.ynab.com/v1/budgets/{{budget_id}}/accounts"
    response_extract: "$.data.accounts"

  - name: ynab_list_transactions
    description: "List transactions on or after since_date (YYYY-MM-DD). since_date is REQUIRED because a budget's full history is unbounded. Amounts are in MILLIUNITS — divide by 1000; outflows are negative. Read-only."
    mutating: false
    params:
      type: object
      properties:
        budget_id:  {type: string}
        since_date: {type: string, description: "YYYY-MM-DD, inclusive"}
        type:       {type: string, description: "optional filter: 'uncategorized' or 'unapproved'"}
      required: [budget_id, since_date]
    request:
      method: GET
      url: "https://api.ynab.com/v1/budgets/{{budget_id}}/transactions"
      query:
        since_date: "{{since_date}}"
        type: "{{type}}"
    response_extract: "$.data.transactions"

  - name: ynab_create_transaction
    description: "Record a transaction. amount is in MILLIUNITS and SIGNED: -4500 is 4.50 spent, 4500 is 4.50 received. date is YYYY-MM-DD. Mutating."
    mutating: true
    params:
      type: object
      properties:
        budget_id:   {type: string}
        account_id:  {type: string}
        date:        {type: string, description: "YYYY-MM-DD"}
        amount:      {type: integer, description: "signed milliunits: -4500 = 4.50 spent"}
        payee_name:  {type: string}
        category_id: {type: string}
        memo:        {type: string}
      required: [budget_id, account_id, date, amount]
    request:
      method: POST
      url: "https://api.ynab.com/v1/budgets/{{budget_id}}/transactions"
      body:
        transaction:
          account_id: "{{account_id}}"
          date: "{{date}}"
          amount: "{{amount}}"
          payee_name: "{{payee_name}}"
          category_id: "{{category_id}}"
          memo: "{{memo}}"
    response_extract: "$.data.transaction"

  - name: ynab_list_categories
    description: "List category groups and their categories with budgeted and balance figures, in MILLIUNITS. Read-only."
    mutating: false
    params:
      type: object
      properties:
        budget_id: {type: string}
      required: [budget_id]
    request:
      method: GET
      url: "https://api.ynab.com/v1/budgets/{{budget_id}}/categories"
    response_extract: "$.data.category_groups"
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestYNABProvider -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo**

YNAB is **not** in simple-icons — use the `UPSTREAM` manifest with a pinned URL to YNAB's own published mark.

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/ynab.yaml \
  internal/connectors/connectors/ynab.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/ynab.svg
git commit -m "feat(connectors): add YNAB"
```

---

### Task 5: Raindrop.io

**Files:**
- Create: `internal/connectors/providers/raindrop.yaml`
- Create: `internal/connectors/connectors/raindrop.yaml`
- Create: `web/ui/src/assets/logos/raindrop.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: `actionsOf` from Task 1.
- Produces: five actions — `raindrop_list_collections`, `raindrop_list_bookmarks`, `raindrop_search`, `raindrop_create_bookmark`, `raindrop_update_bookmark`.

**Verified against live docs:** a Raindrop **test token** is generated at `app.raindrop.io/settings/integrations` by opening your own app and copying the "Test token". Test tokens **do not expire**, and every request takes `Authorization: Bearer <token>`. No OAuth app is needed for personal use.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(acts[name].Params, &schema); err != nil {
			t.Fatalf("%s params: %v", name, err)
		}
		if _, ok := schema.Properties["perpage"]; !ok {
			t.Errorf("%s must accept perpage to bound its result", name)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestRaindropProvider -count=1`

Expected: FAIL — `raindrop provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/raindrop.yaml`:

```yaml
name: raindrop
label: Raindrop.io
category: Productivity
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Raindrop.io test token"
  key_hint: "from Settings → Integrations → your app → Test token"
  setup_url: https://app.raindrop.io/settings/integrations
setup_steps:
  - "In Raindrop.io open Settings → Integrations and press 'Create new app'. Any name will do — it is only a container for your own token."
  - "Open the app you just created and copy the 'Test token'."
  - "Paste it below. Test tokens do not expire, and this flow needs no OAuth app."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/raindrop.yaml`:

```yaml
provider: raindrop
actions:
  - name: raindrop_list_collections
    description: "List collections with their ids. Collection 0 means 'all bookmarks' and -1 means 'unsorted'. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://api.raindrop.io/rest/v1/collections"
    response_extract: "$.items"

  - name: raindrop_list_bookmarks
    description: "List bookmarks in a collection, newest first. Use collection_id 0 for all bookmarks and -1 for unsorted. Pages are 25 items unless perpage says otherwise. Read-only."
    mutating: false
    params:
      type: object
      properties:
        collection_id: {type: integer, description: "collection id; 0 = all, -1 = unsorted"}
        perpage:       {type: integer, description: "items per page, max 50"}
        page:          {type: integer, description: "zero-based page number"}
      required: [collection_id]
    request:
      method: GET
      url: "https://api.raindrop.io/rest/v1/raindrops/{{collection_id}}"
      query:
        perpage: "{{perpage}}"
        page: "{{page}}"
        sort: "-created"
    response_extract: "$.items"

  - name: raindrop_search
    description: "Search bookmarks by text across all collections. Supports Raindrop's own search syntax, e.g. '#tag' or 'type:article'. Read-only."
    mutating: false
    params:
      type: object
      properties:
        search:  {type: string, description: "search query"}
        perpage: {type: integer, description: "items per page, max 50"}
        page:    {type: integer, description: "zero-based page number"}
      required: [search]
    request:
      method: GET
      url: "https://api.raindrop.io/rest/v1/raindrops/0"
      query:
        search: "{{search}}"
        perpage: "{{perpage}}"
        page: "{{page}}"
    response_extract: "$.items"

  - name: raindrop_create_bookmark
    description: "Save a URL as a bookmark. Raindrop fetches the title and excerpt itself when they are omitted. Use collection_id -1 for unsorted. Mutating."
    mutating: true
    params:
      type: object
      properties:
        link:          {type: string, description: "the URL to save"}
        collection_id: {type: integer, description: "target collection; -1 = unsorted"}
        title:         {type: string}
        excerpt:       {type: string}
        tags:          {type: array, items: {type: string}}
      required: [link]
    request:
      method: POST
      url: "https://api.raindrop.io/rest/v1/raindrop"
      body:
        link: "{{link}}"
        title: "{{title}}"
        excerpt: "{{excerpt}}"
        tags: "{{tags}}"
        collection: {"$id": "{{collection_id}}"}
    response_extract: "$.item"

  - name: raindrop_update_bookmark
    description: "Update a bookmark's title, excerpt or tags by id. Omitted fields are left alone; supplying tags REPLACES the existing set. Mutating."
    mutating: true
    params:
      type: object
      properties:
        raindrop_id: {type: integer}
        title:       {type: string}
        excerpt:     {type: string}
        tags:        {type: array, items: {type: string}, description: "replaces the existing tags"}
      required: [raindrop_id]
    request:
      method: PUT
      url: "https://api.raindrop.io/rest/v1/raindrop/{{raindrop_id}}"
      body:
        title: "{{title}}"
        excerpt: "{{excerpt}}"
        tags: "{{tags}}"
    response_extract: "$.item"
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestRaindropProvider -count=1`

Expected: PASS.

**If `raindrop_create_bookmark`'s nested `collection: {"$id": …}` fails to render**, check `renderBody`'s handling of a key beginning with `$`. If YAML or the renderer mangles it, drop `collection` from the body and let bookmarks land in Unsorted — note the reduction here rather than silently shipping a broken field.

- [ ] **Step 6: Vendor the logo and run the package**

Raindrop.io is **not** in simple-icons — use the `UPSTREAM` manifest.

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/raindrop.yaml \
  internal/connectors/connectors/raindrop.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/raindrop.svg
git commit -m "feat(connectors): add Raindrop.io"
```

---

### Task 6: Home Assistant

**Files:**
- Create: `internal/connectors/providers/home_assistant.yaml`
- Create: `internal/connectors/connectors/home_assistant.yaml`
- Create: `web/ui/src/assets/logos/home_assistant.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: `NormalizeBaseURL` and `ConnectInput.Normalize` from Plan 1; `actionsOf` from Task 1.
- Produces: six actions — `ha_list_states`, `ha_get_state`, `ha_call_service`, `ha_list_services`, `ha_get_history`, `ha_fire_event`.

**Verified against live docs:** a Long-Lived Access Token is created from the user's profile page (`/profile`, Security tab). Every call carries `Authorization: Bearer <token>`. The REST API is served from the same host as the UI at `/api/`.

**The critical action is `ha_list_states`.** `GET /api/states` returns **every entity in the house** — a modest smart home blows the 8 KiB bridge cap on the first call, and the model gets a truncated blob it cannot narrow. This provider is the whole reason the "extract narrowly" rule exists, and the test below enforces it.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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

	// Every action must template the per-connection base URL rather than a literal host.
	for name, a := range acts {
		if !strings.Contains(a.Request.URL, "{{conn.base_url}}") {
			t.Errorf("%s URL = %q, want it to template {{conn.base_url}}", name, a.Request.URL)
		}
	}

	// GET /api/states returns EVERY entity in the house. Without a filter this action
	// truncates against the 8 KiB bridge cap on the first call in any real home.
	var states struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(acts["ha_list_states"].Params, &states); err != nil {
		t.Fatalf("ha_list_states params: %v", err)
	}
	if _, ok := states.Properties["entity_prefix"]; !ok {
		t.Error("ha_list_states must accept entity_prefix — the raw endpoint returns every entity in the house")
	}

	// History over an unbounded window is the same failure in the time dimension.
	var hist struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(acts["ha_get_history"].Params, &hist); err != nil {
		t.Fatalf("ha_get_history params: %v", err)
	}
	for _, want := range []string{"entity_id", "start_time"} {
		if !contains(hist.Required, want) {
			t.Errorf("ha_get_history required = %v, want %q", hist.Required, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestHomeAssistantProvider -count=1`

Expected: FAIL — `home_assistant provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/home_assistant.yaml`:

```yaml
name: home_assistant
label: Home Assistant
category: Self-hosted
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Long-Lived Access Token"
  key_hint: "created on your Home Assistant profile page"
  setup_url: https://www.home-assistant.io/docs/authentication/
connect_inputs:
  - key: base_url
    label: "Home Assistant URL"
    hint: "e.g. http://homeassistant.local:8123 — a LAN or Tailscale address is fine"
    required: true
    normalize: base_url
setup_steps:
  - "In Home Assistant click your user name in the sidebar to open your profile, then the Security tab."
  - "Scroll to Long-Lived Access Tokens and press 'Create Token'. Copy it — Home Assistant shows it only once."
  - "Enter your Home Assistant URL and the token below. A private address such as http://192.168.1.10:8123 works: connectors reach your own network on purpose."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/home_assistant.yaml`:

```yaml
provider: home_assistant
actions:
  - name: ha_list_states
    description: "List entity states. entity_prefix is REQUIRED and narrows to one domain or entity family, e.g. 'sensor.' or 'light.kitchen' — the underlying endpoint returns every entity in the house, which is far too large to return whole. Read-only."
    mutating: false
    params:
      type: object
      properties:
        entity_prefix: {type: string, description: "entity_id prefix to match, e.g. 'sensor.' or 'climate.'"}
      required: [entity_prefix]
    request:
      method: GET
      url: "{{conn.base_url}}/api/states"
    response_extract: "$"
    # Home Assistant offers NO server-side filter on /api/states, so entity_prefix is
    # honoured client-side by Plan 1's ResponseFilter. Without it the parameter would
    # be a lie and every call would truncate against the 8 KiB bridge cap.
    response_filter:
      field: entity_id
      prefix_arg: entity_prefix

  - name: ha_get_state
    description: "Get one entity's current state and attributes by entity_id, e.g. 'sensor.living_room_temperature'. Prefer this over listing when you already know the entity. Read-only."
    mutating: false
    params:
      type: object
      properties:
        entity_id: {type: string, description: "e.g. sensor.living_room_temperature"}
      required: [entity_id]
    request:
      method: GET
      url: "{{conn.base_url}}/api/states/{{entity_id}}"
    response_extract: "$"

  - name: ha_list_services
    description: "List the services each domain exposes, so a call can be made correctly. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "{{conn.base_url}}/api/services"
    response_extract: "$"

  - name: ha_call_service
    description: "Call a service, e.g. domain 'light' service 'turn_on' with entity_id 'light.kitchen'. This ACTS ON THE PHYSICAL HOME — lights, locks, heating. data carries any extra service fields such as brightness. Mutating."
    mutating: true
    params:
      type: object
      properties:
        domain:    {type: string, description: "e.g. light, switch, climate"}
        service:   {type: string, description: "e.g. turn_on, turn_off, set_temperature"}
        entity_id: {type: string, description: "target entity"}
        data:      {type: object, description: "extra service fields, e.g. {\"brightness\": 180}"}
      required: [domain, service, entity_id]
    request:
      method: POST
      url: "{{conn.base_url}}/api/services/{{domain}}/{{service}}"
      body:
        entity_id: "{{entity_id}}"
    response_extract: "$"

  - name: ha_get_history
    description: "State history for ONE entity from start_time (RFC3339) to end_time. Both an entity and a start time are required: history over every entity for all time is unusably large. Read-only."
    mutating: false
    params:
      type: object
      properties:
        entity_id:  {type: string}
        start_time: {type: string, description: "RFC3339 lower bound"}
        end_time:   {type: string, description: "RFC3339 upper bound"}
      required: [entity_id, start_time]
    request:
      method: GET
      url: "{{conn.base_url}}/api/history/period/{{start_time}}"
      query:
        filter_entity_id: "{{entity_id}}"
        end_time: "{{end_time}}"
        minimal_response: "true"
    response_extract: "$"

  - name: ha_fire_event
    description: "Fire a custom event on the Home Assistant event bus, for automations that listen for it. Mutating."
    mutating: true
    params:
      type: object
      properties:
        event_type: {type: string}
        data:       {type: object, description: "event payload"}
      required: [event_type]
    request:
      method: POST
      url: "{{conn.base_url}}/api/events/{{event_type}}"
      body_arg: data
    response_extract: "$"
```

**One thing to verify in Step 5, not assume: `ha_call_service`'s `data` field.** The body needs `entity_id` plus arbitrary extra fields flattened at the top level, which `renderBody` cannot do — it substitutes values, it does not merge one object into another. Two options: drop `data` and support only entity-targeted calls (`light.turn_on` on an entity, which covers most of what an agent asks for), or add a `body_builder`, which is a Plan 1 follow-up. **Prefer dropping it** and recording the limitation in the action description — a declared parameter the request discards is worse than an absent one.

If you drop it, remove `data` from the `params` block above and add to the description: "Extra service fields such as brightness are not supported yet — this calls a service on an entity."

- [ ] **Step 5: Run the test and resolve the note above**

Run: `go test ./internal/connectors/ -run TestHomeAssistantProvider -count=1`

Expected: PASS. Then resolve the `data` question and re-run until green.

- [ ] **Step 5b: Prove the client-side filter really narrows**

`ha_list_states` is the whole reason Plan 1's `ResponseFilter` exists, so test it end to end rather than trusting the YAML. Append to `internal/connectors/everyday_test.go`:

```go
// The filter must actually drop non-matching entities. Home Assistant has no
// server-side filter, so if this regresses, ha_list_states silently returns every
// entity in the house and truncates against the 8 KiB bridge cap.
func TestHomeAssistantListStatesFiltersClientSide(t *testing.T) {
	acts := actionsOf(t, "home_assistant")
	f := acts["ha_list_states"].ResponseFilter
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
```

Run: `go test ./internal/connectors/ -run TestHomeAssistantListStatesFiltersClientSide -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo and run the package**

`homeassistant` is in simple-icons (`#18BCF2`); map it to the `home_assistant` slug in the `SIMPLE` manifest.

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/home_assistant.yaml \
  internal/connectors/connectors/home_assistant.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/home_assistant.svg
git commit -m "feat(connectors): add Home Assistant"
```

---

### Task 7: Immich

**Files:**
- Modify: `internal/connectors/providers/immich.yaml` (the auth-only fixture from Plan 1 Task 6)
- Create: `internal/connectors/connectors/immich.yaml`
- Create: `web/ui/src/assets/logos/immich.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: the `immich.yaml` provider fixture and `NormalizeBaseURL` from Plan 1; `actionsOf` from Task 1.
- Produces: six actions — `immich_search_assets`, `immich_get_asset`, `immich_list_albums`, `immich_get_album`, `immich_create_album`, `immich_server_statistics`.

**Verified against live docs:** an Immich API key is created in Account Settings → API Keys and sent as the `x-api-key` header (no prefix). The provider file already exists from Plan 1 — this task adds only the actions manifest and a logo.

**Version caveat:** Immich has broken its API across releases more than once. Record the version tested against in the provider's setup steps so a future failure is diagnosable.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
	// A library holds tens of thousands of assets; search must be capped.
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(acts["immich_search_assets"].Params, &schema); err != nil {
		t.Fatalf("immich_search_assets params: %v", err)
	}
	if _, ok := schema.Properties["size"]; !ok {
		t.Error("immich_search_assets must accept size to cap its result")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestImmichProvider -count=1`

Expected: FAIL — `provider "immich" has no actions` (the provider loads from Plan 1's fixture; the manifest is missing).

- [ ] **Step 3: Add the tested-version note to the provider file**

In `internal/connectors/providers/immich.yaml`, append to `setup_steps`:

```yaml
  - "Tested against Immich v1.x — the API has changed across major releases, so a failing action may mean a version mismatch rather than a bad key."
```

Replace `v1.x` with the actual version verified in Task 10.

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/immich.yaml`:

```yaml
provider: immich
actions:
  - name: immich_search_assets
    description: "Search the photo library by natural-language description, e.g. 'dog on a beach'. Immich runs this as a semantic search over its own index. size caps the result — a library can hold tens of thousands of assets. Read-only."
    mutating: false
    params:
      type: object
      properties:
        query: {type: string, description: "natural-language description"}
        size:  {type: integer, description: "max assets to return, default 20"}
      required: [query]
    request:
      method: POST
      url: "{{conn.base_url}}/api/search/smart"
      body:
        query: "{{query}}"
        size: "{{size}}"
    response_extract: "$.assets.items"

  - name: immich_get_asset
    description: "Get one asset's metadata by id: capture time, camera, location, people. Returns metadata only, never image bytes. Read-only."
    mutating: false
    params:
      type: object
      properties:
        asset_id: {type: string}
      required: [asset_id]
    request:
      method: GET
      url: "{{conn.base_url}}/api/assets/{{asset_id}}"
    response_extract: "$"

  - name: immich_list_albums
    description: "List albums with their ids and asset counts. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "{{conn.base_url}}/api/albums"
    response_extract: "$"

  - name: immich_get_album
    description: "Get one album with its assets by id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        album_id: {type: string}
      required: [album_id]
    request:
      method: GET
      url: "{{conn.base_url}}/api/albums/{{album_id}}"
    response_extract: "$"

  - name: immich_create_album
    description: "Create an album, optionally seeded with asset ids. Mutating."
    mutating: true
    params:
      type: object
      properties:
        album_name:  {type: string}
        description: {type: string}
        asset_ids:   {type: array, items: {type: string}}
      required: [album_name]
    request:
      method: POST
      url: "{{conn.base_url}}/api/albums"
      body:
        albumName: "{{album_name}}"
        description: "{{description}}"
        assetIds: "{{asset_ids}}"
    response_extract: "$"

  - name: immich_server_statistics
    description: "Server totals: photo count, video count and disk usage. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "{{conn.base_url}}/api/server/statistics"
    response_extract: "$"
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestImmichProvider -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo and run the package**

`immich` is in simple-icons (`#4250AF`).

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/immich.yaml \
  internal/connectors/connectors/immich.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/immich.svg
git commit -m "feat(connectors): add Immich actions"
```

---

### Task 8: Paperless-ngx

**Files:**
- Create: `internal/connectors/providers/paperless.yaml`
- Create: `internal/connectors/connectors/paperless.yaml`
- Create: `web/ui/src/assets/logos/paperless.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: `NormalizeBaseURL` and `ConnectInput.Normalize` from Plan 1; `actionsOf` from Task 1.
- Produces: six actions — `paperless_search_documents`, `paperless_get_document`, `paperless_get_document_text`, `paperless_list_tags`, `paperless_list_correspondents`, `paperless_update_document_tags`.

**Verified against live docs:** an API token is created (or regenerated) from the "My Profile" link in the web UI's user dropdown. It is sent as `Authorization: Token <token>` — note **`Token`, not `Bearer`**, which is the single most likely authoring mistake here.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
	// A document archive is unbounded; search must be pageable.
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(acts["paperless_search_documents"].Params, &schema); err != nil {
		t.Fatalf("paperless_search_documents params: %v", err)
	}
	if _, ok := schema.Properties["page_size"]; !ok {
		t.Error("paperless_search_documents must accept page_size to bound its result")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestPaperlessProvider -count=1`

Expected: FAIL — `paperless provider not loaded`.

- [ ] **Step 3: Write the provider file**

Create `internal/connectors/providers/paperless.yaml`:

```yaml
name: paperless
label: Paperless-ngx
category: Self-hosted
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Token "
  key_label: "Paperless-ngx API token"
  key_hint: "from My Profile in the user dropdown"
  setup_url: https://docs.paperless-ngx.com/api/
connect_inputs:
  - key: base_url
    label: "Paperless-ngx URL"
    hint: "e.g. https://paper.example.com — a path prefix like /paperless is fine"
    required: true
    normalize: base_url
setup_steps:
  - "In Paperless-ngx open the user dropdown at the top right and click 'My Profile'."
  - "Press the circular arrow next to the API token to generate one, then copy it."
  - "Enter your Paperless-ngx URL and the token below. Make sure the account holds the permissions for the documents you want the agent to reach."
```

- [ ] **Step 4: Write the action manifest**

Create `internal/connectors/connectors/paperless.yaml`:

```yaml
provider: paperless
actions:
  - name: paperless_search_documents
    description: "Full-text search across documents. query searches the OCR'd content as well as titles. Returns metadata only — use paperless_get_document_text for the content. Read-only."
    mutating: false
    params:
      type: object
      properties:
        query:     {type: string, description: "full-text search query"}
        page_size: {type: integer, description: "results per page, default 10"}
        page:      {type: integer, description: "one-based page number"}
      required: [query]
    request:
      method: GET
      url: "{{conn.base_url}}/api/documents/"
      query:
        query: "{{query}}"
        page_size: "{{page_size}}"
        page: "{{page}}"
    response_extract: "$.results"

  - name: paperless_get_document
    description: "Get one document's metadata by id: title, correspondent, tags, document type, created date. Read-only."
    mutating: false
    params:
      type: object
      properties:
        document_id: {type: integer}
      required: [document_id]
    request:
      method: GET
      url: "{{conn.base_url}}/api/documents/{{document_id}}/"
    response_extract: "$"

  - name: paperless_get_document_text
    description: "Get a document's OCR'd text content by id. Use this to read what a document actually says — it may be long, so prefer searching first to find the right document. Read-only."
    mutating: false
    params:
      type: object
      properties:
        document_id: {type: integer}
      required: [document_id]
    request:
      method: GET
      url: "{{conn.base_url}}/api/documents/{{document_id}}/"
    response_extract: "$.content"

  - name: paperless_list_tags
    description: "List tags with their ids and document counts. Tag ids are needed to update a document's tags. Read-only."
    mutating: false
    params:
      type: object
      properties:
        page_size: {type: integer, description: "results per page, default 100"}
      required: []
    request:
      method: GET
      url: "{{conn.base_url}}/api/tags/"
      query:
        page_size: "{{page_size}}"
    response_extract: "$.results"

  - name: paperless_list_correspondents
    description: "List correspondents (who a document is from or to) with their ids and document counts. Read-only."
    mutating: false
    params:
      type: object
      properties:
        page_size: {type: integer, description: "results per page, default 100"}
      required: []
    request:
      method: GET
      url: "{{conn.base_url}}/api/correspondents/"
      query:
        page_size: "{{page_size}}"
    response_extract: "$.results"

  - name: paperless_update_document_tags
    description: "Replace a document's tags. tag_ids REPLACES the existing set, so read the current tags first and send the full list you want. Get ids from paperless_list_tags. Mutating."
    mutating: true
    params:
      type: object
      properties:
        document_id: {type: integer}
        tag_ids:     {type: array, items: {type: integer}, description: "the complete tag set to apply"}
      required: [document_id, tag_ids]
    request:
      method: PATCH
      url: "{{conn.base_url}}/api/documents/{{document_id}}/"
      body:
        tags: "{{tag_ids}}"
    response_extract: "$"
```

Note `paperless_get_document` and `paperless_get_document_text` hit the same endpoint and differ only in `response_extract`. That is deliberate: the full document object plus its OCR content would routinely exceed the 8 KiB cap, so the two are separate tools and the model picks the one it needs.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestPaperlessProvider -count=1`

Expected: PASS.

- [ ] **Step 6: Vendor the logo and run the package**

`paperlessngx` is in simple-icons (`#17541F`); map it to the `paperless` slug.

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/paperless.yaml \
  internal/connectors/connectors/paperless.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/paperless.svg
git commit -m "feat(connectors): add Paperless-ngx"
```

---

### Task 9: Open-Meteo

**Files:**
- Modify: `internal/connectors/providers/open_meteo.yaml` (the auth-only fixture from Plan 1 Task 3)
- Create: `internal/connectors/connectors/open_meteo.yaml`
- Create: `web/ui/src/assets/logos/open_meteo.svg`
- Modify: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: the `open_meteo.yaml` fixture and the `none` auth kind from Plan 1; `actionsOf` from Task 1.
- Produces: four actions — `weather_geocode`, `weather_forecast`, `weather_current`, `weather_air_quality`.

**Verified against live docs:** genuinely keyless — no signup, no key. Free tier is **non-commercial only**, limited to 10,000 calls/day, 5,000/hour and 600/minute, and "personal home automation" is explicitly named as qualifying. Data is licensed **CC BY 4.0, attribution required** — which is why every action description carries the credit line through to the agent.

**`weather_geocode` is load-bearing.** Without it the model must already know a place's latitude and longitude before it can ask about the weather there, which makes every other action unusable from a natural request like "what's the weather in Skopje".

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/everyday_test.go`:

```go
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
	if !strings.Contains(acts["weather_forecast"].Description, "Open-Meteo") {
		t.Error("weather_forecast description must carry the CC BY attribution")
	}
	// Without geocoding, every other action needs coordinates the model does not have.
	var geo struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(acts["weather_geocode"].Params, &geo); err != nil {
		t.Fatalf("weather_geocode params: %v", err)
	}
	if !contains(geo.Required, "name") {
		t.Errorf("weather_geocode required = %v, want name", geo.Required)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestOpenMeteoProvider -count=1`

Expected: FAIL — `provider "open_meteo" has no actions`.

- [ ] **Step 3: Write the action manifest**

Create `internal/connectors/connectors/open_meteo.yaml`:

```yaml
provider: open_meteo
actions:
  - name: weather_geocode
    description: "Turn a place name into coordinates, e.g. 'Skopje' → latitude 41.99, longitude 21.43. Call this FIRST — every other weather action needs coordinates. Read-only. Data by Open-Meteo (CC BY 4.0)."
    mutating: false
    params:
      type: object
      properties:
        name:  {type: string, description: "city or place name"}
        count: {type: integer, description: "max matches to return, default 5"}
      required: [name]
    request:
      method: GET
      url: "https://geocoding-api.open-meteo.com/v1/search"
      query:
        name: "{{name}}"
        count: "{{count}}"
        format: "json"
    response_extract: "$.results"

  - name: weather_current
    description: "Current conditions at a coordinate: temperature, apparent temperature, humidity, precipitation, wind and a WMO weather code. Use weather_geocode first to get coordinates. Read-only. Data by Open-Meteo (CC BY 4.0)."
    mutating: false
    params:
      type: object
      properties:
        latitude:  {type: number}
        longitude: {type: number}
        timezone:  {type: string, description: "IANA timezone, e.g. Europe/Skopje; 'auto' uses the coordinate's own"}
      required: [latitude, longitude]
    request:
      method: GET
      url: "https://api.open-meteo.com/v1/forecast"
      query:
        latitude: "{{latitude}}"
        longitude: "{{longitude}}"
        timezone: "{{timezone}}"
        current: "temperature_2m,apparent_temperature,relative_humidity_2m,precipitation,weather_code,wind_speed_10m"
    response_extract: "$.current"

  - name: weather_forecast
    description: "Daily forecast at a coordinate: min/max temperature, precipitation sum and probability, sunrise, sunset and a WMO weather code. forecast_days defaults to 7 and caps at 16. Use weather_geocode first. Read-only. Data by Open-Meteo (CC BY 4.0)."
    mutating: false
    params:
      type: object
      properties:
        latitude:      {type: number}
        longitude:     {type: number}
        forecast_days: {type: integer, description: "1–16, default 7"}
        timezone:      {type: string, description: "IANA timezone, e.g. Europe/Skopje"}
      required: [latitude, longitude]
    request:
      method: GET
      url: "https://api.open-meteo.com/v1/forecast"
      query:
        latitude: "{{latitude}}"
        longitude: "{{longitude}}"
        forecast_days: "{{forecast_days}}"
        timezone: "{{timezone}}"
        daily: "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,sunrise,sunset"
    response_extract: "$.daily"

  - name: weather_air_quality
    description: "Current air quality at a coordinate: European AQI, PM2.5, PM10, ozone and nitrogen dioxide. Use weather_geocode first. Read-only. Data by Open-Meteo (CC BY 4.0)."
    mutating: false
    params:
      type: object
      properties:
        latitude:  {type: number}
        longitude: {type: number}
        timezone:  {type: string, description: "IANA timezone"}
      required: [latitude, longitude]
    request:
      method: GET
      url: "https://air-quality-api.open-meteo.com/v1/air-quality"
      query:
        latitude: "{{latitude}}"
        longitude: "{{longitude}}"
        timezone: "{{timezone}}"
        current: "european_aqi,pm2_5,pm10,ozone,nitrogen_dioxide"
    response_extract: "$.current"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestOpenMeteoProvider -count=1`

Expected: PASS.

- [ ] **Step 5: Vendor the logo and run the package**

Open-Meteo is **not** in simple-icons — use the `UPSTREAM` manifest, pinned to a commit in `open-meteo/open-meteo`.

```bash
./scripts/vendor-brand-logos.sh
go test ./internal/connectors/ -count=1
cd web/ui && npx vitest run src/components/brand/
```

Expected: PASS on all three.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/connectors/open_meteo.yaml \
  internal/connectors/everyday_test.go \
  scripts/vendor-brand-logos.sh web/ui/src/assets/logos/open_meteo.svg
git commit -m "feat(connectors): add Open-Meteo actions"
```

---

### Task 10: Live verification and marking

**Files:**
- Modify: whichever `internal/connectors/providers/*.yaml` fail live verification
- Modify: `internal/connectors/registry.go` (add the `Unverified` field)
- Test: `internal/connectors/everyday_test.go`

**Interfaces:**
- Consumes: `cmd/livecheck` from Plan 1; all nine providers.
- Produces: `Provider.Unverified bool` (yaml `unverified`) — set on any provider not confirmed against its live API. It is data, so the SPA can surface it later with no schema change.

**The bar** (from the spec): live-verify what is attainable; mark the rest rather than letting it silently join the "hand-authored and unverified" pile CLAUDE.md already records as a known gap.

- [ ] **Step 1: Add the `Unverified` field**

In `internal/connectors/registry.go`, add to the `Provider` struct, after `Category`:

```go
	// Unverified marks a provider whose action manifest has NOT been confirmed against
	// the live API with cmd/livecheck. It is data rather than a comment so the UI can
	// surface it, and so "which providers are guesses" is answerable by a test.
	Unverified bool `yaml:"unverified"`
```

- [ ] **Step 2: Write the accounting test**

Append to `internal/connectors/everyday_test.go`:

```go
// Wave-1 providers are either live-verified or explicitly marked. This test does not
// judge which — it fails only if a wave-1 provider is neither, which is the state the
// spec's verification bar exists to prevent.
func TestWave1ProvidersDeclareVerificationStatus(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	// Verified live with cmd/livecheck against real credentials. Moving a provider
	// OUT of this list means marking it unverified: true in its YAML.
	verified := map[string]bool{
		"google_calendar": true,
		"google_tasks":    true,
		"todoist":         true,
		"raindrop":        true,
		"open_meteo":      true,
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
```

Update the `verified` map to match what Step 3 actually achieves — it is the record, not an aspiration.

- [ ] **Step 3: Run live verification**

For each provider, obtain a credential, connect it through the SPA, and run at least one **read-only** action:

```bash
go run ./cmd/livecheck google_calendar calendar_list_calendars '{}'
go run ./cmd/livecheck google_tasks tasks_list_tasklists '{}'
go run ./cmd/livecheck todoist todoist_list_projects '{}'
go run ./cmd/livecheck raindrop raindrop_list_collections '{}'
go run ./cmd/livecheck open_meteo weather_geocode '{"name":"Skopje"}'
go run ./cmd/livecheck ynab ynab_list_budgets '{}'
go run ./cmd/livecheck home_assistant ha_list_states '{"entity_prefix":"sensor."}'
go run ./cmd/livecheck immich immich_list_albums '{}'
go run ./cmd/livecheck paperless paperless_list_tags '{}'
```

Then verify one action per provider that exercises its **narrowing**, since that is what the 8 KiB cap makes load-bearing:

```bash
go run ./cmd/livecheck google_calendar calendar_list_events \
  '{"calendar_id":"primary","time_min":"2026-08-01T00:00:00Z","time_max":"2026-08-08T00:00:00Z","max_results":10}'
go run ./cmd/livecheck open_meteo weather_forecast '{"latitude":41.99,"longitude":21.43,"forecast_days":3}'
```

For every call, check the result is **under 8 KiB** and is the narrowed shape the `response_extract` promised. A result that arrives truncated means the extract or the filter parameter is wrong — fix the manifest, do not lower the bar.

**YNAB is subscription-only**, so its credential may be unattainable. If so, set `unverified: true` in `ynab.yaml` and drop it from the `verified` map. Same for any self-hosted service not running on this install.

- [ ] **Step 4: Mark whatever did not verify**

For each unverified provider, add to its YAML, immediately after `category`:

```yaml
unverified: true
```

- [ ] **Step 5: Run the accounting test**

Run: `go test ./internal/connectors/ -run TestWave1ProvidersDeclareVerificationStatus -count=1`

Expected: PASS, with the `verified` map matching reality.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/everyday_test.go internal/connectors/providers/
git commit -m "test(connectors): record wave-1 live-verification status"
```

---

### Task 11: Documentation, full gate and PR

**Files:**
- Modify: `CLAUDE.md` (provider count and the everyday-tier paragraph)

**Interfaces:**
- Consumes: every prior task.
- Produces: a green `make ci` and a draft PR.

- [ ] **Step 1: Update the provider counts in CLAUDE.md**

`CLAUDE.md` states "**45 providers (~272 actions)**" in the `internal/connectors` table row and "**32 providers**" in the key-packages table — both now stale. Get the real numbers:

```bash
ls internal/connectors/providers/*.yaml | wc -l
grep -c '^  - name:' internal/connectors/connectors/*.yaml | awk -F: '{s+=$2} END {print s}'
```

Update both figures, and add this paragraph to the "Connector service layer" section:

```markdown
**The everyday tier** (wave 1, 2026-08) opened a second axis alongside the business/SaaS
providers: services people use in their own lives. Three shapes, all data-only —
**personal cloud** (Todoist, YNAB, Raindrop.io) paste a token; **self-hosted**
(Home Assistant, Immich, Paperless-ngx) pair a token with the user's own `base_url`,
collected via `connect_inputs` with `normalize: base_url` and reached because
connectors deliberately do not use the private-address dial guard; and **keyless**
(Open-Meteo) needs no credential at all via `auth.kind: none`. Google Calendar and
Google Tasks ride the existing Google OAuth app through `auth_parent` — each child
consents separately with its OWN scopes, so adding them did not disturb existing Gmail
connections. Providers not confirmed against their live API carry `unverified: true`
in their YAML; see `TestWave1ProvidersDeclareVerificationStatus`.
```

- [ ] **Step 2: Run the full local gate**

Run: `make ci`

Expected: PASS on gofmt, `go vet`, `go test -race`, the six-target cross-compile and the frontend gate. Allow 15+ minutes.

- [ ] **Step 3: Smoke-test the connections page**

```bash
make deploy
curl -sS http://127.0.0.1:8080/healthz
```

Then open the SPA connections page and confirm: the new `Self-hosted`, `Finance` and `Data & Reference` headings appear with their providers; `Health & Fitness` shows **no** heading (it has no providers); every new card shows a real logo rather than a letter tile; and Open-Meteo's card has a bare Connect button with no key field.

- [ ] **Step 4: Push and open a draft PR**

```bash
git push -u origin HEAD
gh pr create --draft \
  --title "feat(connectors): add nine everyday connector providers" \
  --body "Implements Plan 2 of docs/superpowers/specs/2026-08-03-everyday-connectors-design.md: Google Calendar, Google Tasks, Todoist, YNAB, Raindrop.io, Home Assistant, Immich, Paperless-ngx and Open-Meteo — roughly 50 curated actions across the personal-cloud, self-hosted and keyless tiers. Live-verification status is recorded per provider via the unverified flag."
```

---

## Self-Review

**Spec coverage.** All nine wave-1 providers from the spec's table have a task (1–9), in the spec's own order. The spec's "Extract narrowly" section is enforced by a global constraint plus per-provider tests — most sharply in Task 6, where `ha_list_states` is the case the rule was written for. The spec's verification plan is Task 10, including the `unverified` marker it calls for and the YNAB-subscription risk it names. The spec's action-sketch counts are matched: Calendar 7, Tasks 5, Todoist 6, YNAB 6, Raindrop 5, Home Assistant 6, Immich 6, Paperless 6, Open-Meteo 4 = 51, matching the stated ~50.

**Type consistency.** `actionsOf` is defined in Task 1 and used in Tasks 2–9. `contains` is defined in Task 4 and used in Tasks 6 and 9 — Task 4 must therefore precede them, which the numbering ensures. `Provider.Unverified` is added in Task 10 and read only there. Every test references `Action`, `Provider`, `ConnectInput` and `RequestTemplate` fields that exist in `internal/connectors/registry.go` today.

**Placeholder scan.** No TBD/TODO. Every YAML file is written out in full rather than described. Two places direct the implementer to **verify rather than assume**, each with a stated fallback: `ha_call_service`'s `data` merging (Task 6) and Raindrop's `$id` nested key (Task 5). These are honest unknowns about this repo's renderer that a plan cannot settle without running it — each names what to check and what to do if the answer is no.

**Extractor dependency, resolved during planning rather than during implementation.** Every `response_extract` in this plan was checked against the real `extract` implementation. `$.items`, `$.results`, `$.current`, `$.daily`, `$.item` and `$.calendars` work today. `$.data.budgets`, `$.data.transactions`, `$.data.month`, `$.data.category_groups` and `$.assets.items` do **not** — they need Plan 1 Task 10, which also fixes the two already-shipped `$.data.children` actions in `reddit.yaml` that have been silently returning whole envelopes. Home Assistant's filter is a declared `response_filter`, not a JSONPath expression, because this repo's extractor is deliberately not a JSONPath engine.

**Known risk carried forward.** `calendar_freebusy` needs `items: [{"id": …}]`, which `renderBody` substitutes but cannot restructure — the same limitation that produced the `ga4_report` body builder. Flagged inline in Task 1 with the fallback (a `body_builder`, which would be a Plan 1 follow-up) rather than discovered during live verification.

---

## As-built notes (implemented 2026-08-03)

This plan was executed. Deviations from what was written, and why:

- **`ha_call_service`'s `data` field was dropped**, as the plan recommended: `renderBody`
  substitutes values but cannot merge one object into another, and a declared parameter
  the request discards is worse than an absent one. The action description says so.
- **`raindrop_create_bookmark`'s nested `collection: {"$id": …}` was dropped** for the
  same class of reason. Bookmarks land in Unsorted, which the description states.
- **`calendar_freebusy` was narrowed to one calendar**, not a list. `renderBody` can
  substitute an array but cannot restructure one into `[{"id": …}]`, so the action takes
  a single `calendar_id` and templates the item inline.
- **Verification outcome differs from the plan's optimistic list.** Only Open-Meteo was
  live-verified — it is keyless, so it needs no credential; all four actions returned
  correctly narrowed payloads of 121–362 bytes. The other eight carry `unverified: true`
  because no credential was available. `TestWave1ProvidersDeclareVerificationStatus`
  enforces that honesty rather than leaving it to memory.
- **The live test is behind a `//go:build livecheck` tag** so CI never depends on a third
  party being reachable. Run it with
  `go test ./internal/connectors/ -tags livecheck -run TestLiveOpenMeteo -v`.
