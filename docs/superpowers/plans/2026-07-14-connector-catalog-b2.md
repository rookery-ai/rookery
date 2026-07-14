# Connector Catalog B2 Implementation Plan (10 token-first JSON providers)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add HubSpot, Calendly, Asana, Airtable, SendGrid, Intercom, ClickUp, Monday (personal-token) and Dropbox, Zoom (OAuth) as connector providers on the existing engine.

**Architecture:** Each provider is pure data — a `providers/<p>.yaml` (auth config) + a `connectors/<p>.yaml` (curated JSON-body actions rendered by the existing `renderBody`). No engine code changes. API-key providers use the Foundation's `auth: {kind: api_key, ...}`; Dropbox/Zoom use OAuth. One task per provider + one UI task.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, Echo v4. Tests: stdlib `testing` (LoadBundled parse + `renderRequest` body checks + `applyAuth` + counts).

## Global Constraints

- Package under change: `internal/connectors` (data files + one shared test file) + `web/handlers_services.go` (provider list). **No engine code changes, no DB migrations.**
- Auth is token-first: api_key for HubSpot/Calendly/Asana/Airtable/SendGrid/Intercom/ClickUp/Monday; OAuth2 for Dropbox/Zoom only.
- api_key header injection uses the Foundation's `applyAuth`: `value_prefix: "Bearer "` for Bearer providers; `value_prefix: ""` for ClickUp + Monday (raw token in `Authorization`). Intercom adds `static_headers: {Intercom-Version: "2.11"}`.
- Bodies are JSON via `renderBody` (nested maps/arrays, lone-`{{arg}}` type preservation + optional-key omission). Object/array params pass through as real values.
- Mutating actions (create/update/delete/send/cancel/reply/archive/move) set `mutating: true` (build-time guard).
- **Verification is unit/rendering only** — no live API calls; live E2E + livecheck deferred.
- Deferred: Dropbox/Airtable multipart **uploads** (multipart not supported by `renderBody`); use link/metadata/record actions instead.
- Add each new provider to `availableServiceProviders` (Task 11) — NOT before (keeps the UI from listing a provider whose actions don't exist yet mid-batch).
- Tests live in a new shared file `internal/connectors/b2_test.go`. Build: `go build ./...`. Test: `go test ./internal/connectors/... ./web/... -count=1`.
- Branch: `main`.

---

## File Structure

Per provider: `internal/connectors/providers/<p>.yaml` + `internal/connectors/connectors/<p>.yaml` (both create). Shared test: `internal/connectors/b2_test.go`. UI: `web/handlers_services.go`.

Providers (slugs): `hubspot`, `calendly`, `asana`, `airtable`, `sendgrid`, `intercom`, `clickup`, `monday`, `dropbox`, `zoom`.

**Shared test helper** (add once, in Task 1, to `b2_test.go`):

```go
package connectors

import (
	"encoding/json"
	"testing"
)

// b2Reg loads the bundled registry for B2 provider tests.
func b2Reg(t *testing.T) *Registry {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// renderB2 renders one action's request body to a generic map for assertions.
func renderB2(t *testing.T, r *Registry, provider, action string, args map[string]any) map[string]any {
	t.Helper()
	a, ok := r.Action(provider, action)
	if !ok {
		t.Fatalf("%s.%s missing", provider, action)
	}
	_, _, body, _, err := renderRequest(a, args, nil)
	if err != nil {
		t.Fatalf("render %s.%s: %v", provider, action, err)
	}
	var m map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("body not JSON for %s.%s: %v (%s)", provider, action, err, body)
		}
	}
	return m
}
```

Each provider task adds one `TestB2_<Provider>` to `b2_test.go`.

---

## Task 1: HubSpot (api_key) + shared test helper

**Files:**
- Create: `internal/connectors/providers/hubspot.yaml`, `internal/connectors/connectors/hubspot.yaml`, `internal/connectors/b2_test.go`

**Interfaces:**
- Produces: `hubspot` provider (api_key, Bearer) + 9 actions; the `b2Reg`/`renderB2` helpers (used by later tasks).

- [ ] **Step 1: Write the failing test** — add the shared helpers (above) AND:

```go
func TestB2_HubSpot(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("hubspot")
	if !ok || !p.IsAPIKey() {
		t.Fatal("hubspot must load as api_key")
	}
	if len(r.Actions("hubspot")) < 8 {
		t.Fatalf("want >=8 hubspot actions, got %d", len(r.Actions("hubspot")))
	}
	m := renderB2(t, r, "hubspot", "hubspot_create_contact", map[string]any{"properties": map[string]any{"email": "a@b.com"}})
	props, ok := m["properties"].(map[string]any)
	if !ok || props["email"] != "a@b.com" {
		t.Fatalf("properties object not passed through: %v", m)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB2_HubSpot -count=1` → FAIL (provider missing).

- [ ] **Step 3: Create `internal/connectors/providers/hubspot.yaml`**

```yaml
name: hubspot
label: HubSpot
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "HubSpot private-app access token"
  key_hint: "pat-..."
  setup_url: https://developers.hubspot.com/docs/api/private-apps
```

- [ ] **Step 4: Create `internal/connectors/connectors/hubspot.yaml`**

```yaml
provider: hubspot
actions:
  - name: hubspot_list_contacts
    description: "List HubSpot contacts. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request: {method: GET, url: "https://api.hubapi.com/crm/v3/objects/contacts", query: {limit: "{{limit}}"}}
    response_extract: "$.results"
  - name: hubspot_get_contact
    description: "Get one HubSpot contact by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.hubapi.com/crm/v3/objects/contacts/{{id}}"}
    response_extract: "$"
  - name: hubspot_search_contacts
    description: "Search HubSpot contacts by a text query. Read-only."
    mutating: false
    params: {type: object, properties: {query: {type: string}, limit: {type: integer}}, required: [query]}
    request:
      method: POST
      url: "https://api.hubapi.com/crm/v3/objects/contacts/search"
      body: {query: "{{query}}", limit: "{{limit}}"}
    response_extract: "$.results"
  - name: hubspot_create_contact
    description: "Create a HubSpot contact. properties is an object of contact fields (email, firstname, …). Mutating."
    mutating: true
    params: {type: object, properties: {properties: {type: object}}, required: [properties]}
    request: {method: POST, url: "https://api.hubapi.com/crm/v3/objects/contacts", body: {properties: "{{properties}}"}}
    response_extract: "$"
  - name: hubspot_update_contact
    description: "Update a HubSpot contact by id. Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, properties: {type: object}}, required: [id, properties]}
    request: {method: PATCH, url: "https://api.hubapi.com/crm/v3/objects/contacts/{{id}}", body: {properties: "{{properties}}"}}
    response_extract: "$"
  - name: hubspot_list_companies
    description: "List HubSpot companies. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request: {method: GET, url: "https://api.hubapi.com/crm/v3/objects/companies", query: {limit: "{{limit}}"}}
    response_extract: "$.results"
  - name: hubspot_create_company
    description: "Create a HubSpot company. properties is an object (name, domain, …). Mutating."
    mutating: true
    params: {type: object, properties: {properties: {type: object}}, required: [properties]}
    request: {method: POST, url: "https://api.hubapi.com/crm/v3/objects/companies", body: {properties: "{{properties}}"}}
    response_extract: "$"
  - name: hubspot_list_deals
    description: "List HubSpot deals. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request: {method: GET, url: "https://api.hubapi.com/crm/v3/objects/deals", query: {limit: "{{limit}}"}}
    response_extract: "$.results"
  - name: hubspot_create_deal
    description: "Create a HubSpot deal. properties is an object (dealname, amount, pipeline, dealstage). Mutating."
    mutating: true
    params: {type: object, properties: {properties: {type: object}}, required: [properties]}
    request: {method: POST, url: "https://api.hubapi.com/crm/v3/objects/deals", body: {properties: "{{properties}}"}}
    response_extract: "$"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_HubSpot -count=1 && go build ./...` → PASS + clean.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/providers/hubspot.yaml internal/connectors/connectors/hubspot.yaml internal/connectors/b2_test.go
git commit -m "feat(connectors): HubSpot provider (api-key) + 9 CRM actions"
```

---

## Task 2: Calendly (api_key)

**Files:** Create `providers/calendly.yaml`, `connectors/calendly.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test** — add to `b2_test.go`:

```go
func TestB2_Calendly(t *testing.T) {
	r := b2Reg(t)
	if p, ok := r.ProviderByName("calendly"); !ok || !p.IsAPIKey() {
		t.Fatal("calendly must load as api_key")
	}
	if len(r.Actions("calendly")) < 6 {
		t.Fatalf("want >=6 calendly actions, got %d", len(r.Actions("calendly")))
	}
	m := renderB2(t, r, "calendly", "calendly_cancel_event", map[string]any{"uuid": "U1", "reason": "x"})
	if m["reason"] != "x" {
		t.Fatalf("cancel body: %v", m)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB2_Calendly -count=1` → FAIL.

- [ ] **Step 3: Create `providers/calendly.yaml`**

```yaml
name: calendly
label: Calendly
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Calendly personal access token"
  key_hint: "eyJ..."
  setup_url: https://calendly.com/integrations/api_webhooks
```

- [ ] **Step 4: Create `connectors/calendly.yaml`**

```yaml
provider: calendly
actions:
  - name: calendly_get_current_user
    description: "Get the authenticated Calendly user (incl. the user URI needed by other actions). Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.calendly.com/users/me"}
    response_extract: "$.resource"
  - name: calendly_list_event_types
    description: "List event types for a user URI (from calendly_get_current_user). Read-only."
    mutating: false
    params: {type: object, properties: {user: {type: string, description: "user URI"}}, required: [user]}
    request: {method: GET, url: "https://api.calendly.com/event_types", query: {user: "{{user}}"}}
    response_extract: "$.collection"
  - name: calendly_list_scheduled_events
    description: "List scheduled events for a user URI. Read-only."
    mutating: false
    params: {type: object, properties: {user: {type: string}, count: {type: integer}}, required: [user]}
    request: {method: GET, url: "https://api.calendly.com/scheduled_events", query: {user: "{{user}}", count: "{{count}}"}}
    response_extract: "$.collection"
  - name: calendly_get_event
    description: "Get a scheduled event by uuid. Read-only."
    mutating: false
    params: {type: object, properties: {uuid: {type: string}}, required: [uuid]}
    request: {method: GET, url: "https://api.calendly.com/scheduled_events/{{uuid}}"}
    response_extract: "$.resource"
  - name: calendly_list_invitees
    description: "List invitees for a scheduled event uuid. Read-only."
    mutating: false
    params: {type: object, properties: {uuid: {type: string}}, required: [uuid]}
    request: {method: GET, url: "https://api.calendly.com/scheduled_events/{{uuid}}/invitees"}
    response_extract: "$.collection"
  - name: calendly_cancel_event
    description: "Cancel a scheduled event by uuid with an optional reason. Mutating."
    mutating: true
    params: {type: object, properties: {uuid: {type: string}, reason: {type: string}}, required: [uuid]}
    request: {method: POST, url: "https://api.calendly.com/scheduled_events/{{uuid}}/cancellation", body: {reason: "{{reason}}"}}
    response_extract: "$.resource"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Calendly -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Calendly provider (api-key) + 6 actions`.

---

## Task 3: Asana (api_key, data-wrapped bodies)

**Files:** Create `providers/asana.yaml`, `connectors/asana.yaml`; modify `b2_test.go`.

Asana wraps request bodies and responses in a `data` envelope.

- [ ] **Step 1: Test:**

```go
func TestB2_Asana(t *testing.T) {
	r := b2Reg(t)
	if p, ok := r.ProviderByName("asana"); !ok || !p.IsAPIKey() {
		t.Fatal("asana must load as api_key")
	}
	if len(r.Actions("asana")) < 8 {
		t.Fatalf("want >=8 asana actions, got %d", len(r.Actions("asana")))
	}
	m := renderB2(t, r, "asana", "asana_create_task", map[string]any{"name": "T", "workspace": "W1"})
	d, ok := m["data"].(map[string]any)
	if !ok || d["name"] != "T" {
		t.Fatalf("asana create_task must wrap in data: %v", m)
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/asana.yaml`**

```yaml
name: asana
label: Asana
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Asana personal access token"
  key_hint: "1/12...:..."
  setup_url: https://app.asana.com/0/my-apps
```

- [ ] **Step 4: Create `connectors/asana.yaml`**

```yaml
provider: asana
actions:
  - name: asana_list_workspaces
    description: "List Asana workspaces. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://app.asana.com/api/1.0/workspaces"}
    response_extract: "$.data"
  - name: asana_list_projects
    description: "List projects in a workspace. Read-only."
    mutating: false
    params: {type: object, properties: {workspace: {type: string}}, required: [workspace]}
    request: {method: GET, url: "https://app.asana.com/api/1.0/projects", query: {workspace: "{{workspace}}"}}
    response_extract: "$.data"
  - name: asana_list_tasks
    description: "List tasks in a project. Read-only."
    mutating: false
    params: {type: object, properties: {project: {type: string}}, required: [project]}
    request: {method: GET, url: "https://app.asana.com/api/1.0/tasks", query: {project: "{{project}}"}}
    response_extract: "$.data"
  - name: asana_get_task
    description: "Get an Asana task by gid. Read-only."
    mutating: false
    params: {type: object, properties: {task_gid: {type: string}}, required: [task_gid]}
    request: {method: GET, url: "https://app.asana.com/api/1.0/tasks/{{task_gid}}"}
    response_extract: "$.data"
  - name: asana_create_task
    description: "Create an Asana task. Provide workspace (and optionally projects[]). Mutating."
    mutating: true
    params:
      type: object
      properties:
        name: {type: string}
        notes: {type: string}
        workspace: {type: string}
        projects: {type: array, items: {type: string}}
      required: [name]
    request:
      method: POST
      url: "https://app.asana.com/api/1.0/tasks"
      body:
        data: {name: "{{name}}", notes: "{{notes}}", workspace: "{{workspace}}", projects: "{{projects}}"}
    response_extract: "$.data"
  - name: asana_update_task
    description: "Update an Asana task by gid. fields is an object of task fields. Mutating."
    mutating: true
    params: {type: object, properties: {task_gid: {type: string}, fields: {type: object}}, required: [task_gid, fields]}
    request: {method: PUT, url: "https://app.asana.com/api/1.0/tasks/{{task_gid}}", body: {data: "{{fields}}"}}
    response_extract: "$.data"
  - name: asana_complete_task
    description: "Mark an Asana task complete. Mutating."
    mutating: true
    params: {type: object, properties: {task_gid: {type: string}}, required: [task_gid]}
    request: {method: PUT, url: "https://app.asana.com/api/1.0/tasks/{{task_gid}}", body: {data: {completed: true}}}
    response_extract: "$.data"
  - name: asana_add_comment
    description: "Add a comment (story) to an Asana task. Mutating."
    mutating: true
    params: {type: object, properties: {task_gid: {type: string}, text: {type: string}}, required: [task_gid, text]}
    request: {method: POST, url: "https://app.asana.com/api/1.0/tasks/{{task_gid}}/stories", body: {data: {text: "{{text}}"}}}
    response_extract: "$.data"
  - name: asana_delete_task
    description: "Delete an Asana task by gid. Mutating and irreversible."
    mutating: true
    params: {type: object, properties: {task_gid: {type: string}}, required: [task_gid]}
    request: {method: DELETE, url: "https://app.asana.com/api/1.0/tasks/{{task_gid}}"}
    response_extract: "$"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Asana -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Asana provider (api-key) + 9 actions (data-wrapped bodies)`.

---

## Task 4: Airtable (api_key)

**Files:** Create `providers/airtable.yaml`, `connectors/airtable.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test:**

```go
func TestB2_Airtable(t *testing.T) {
	r := b2Reg(t)
	if p, ok := r.ProviderByName("airtable"); !ok || !p.IsAPIKey() {
		t.Fatal("airtable must load as api_key")
	}
	if len(r.Actions("airtable")) < 7 {
		t.Fatalf("want >=7 airtable actions, got %d", len(r.Actions("airtable")))
	}
	m := renderB2(t, r, "airtable", "airtable_create_record", map[string]any{"base_id": "b", "table_id": "t", "fields": map[string]any{"Name": "x"}})
	f, ok := m["fields"].(map[string]any)
	if !ok || f["Name"] != "x" {
		t.Fatalf("fields object not passed: %v", m)
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/airtable.yaml`**

```yaml
name: airtable
label: Airtable
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Airtable personal access token"
  key_hint: "pat..."
  setup_url: https://airtable.com/create/tokens
```

- [ ] **Step 4: Create `connectors/airtable.yaml`**

```yaml
provider: airtable
actions:
  - name: airtable_list_bases
    description: "List Airtable bases accessible to the token. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.airtable.com/v0/meta/bases"}
    response_extract: "$.bases"
  - name: airtable_get_base_schema
    description: "Get a base's tables/fields schema. Read-only."
    mutating: false
    params: {type: object, properties: {base_id: {type: string}}, required: [base_id]}
    request: {method: GET, url: "https://api.airtable.com/v0/meta/bases/{{base_id}}/tables"}
    response_extract: "$.tables"
  - name: airtable_list_records
    description: "List records in a table. Read-only."
    mutating: false
    params: {type: object, properties: {base_id: {type: string}, table_id: {type: string}, max: {type: integer}}, required: [base_id, table_id]}
    request: {method: GET, url: "https://api.airtable.com/v0/{{base_id}}/{{table_id}}", query: {maxRecords: "{{max}}"}}
    response_extract: "$.records"
  - name: airtable_get_record
    description: "Get one record by id. Read-only."
    mutating: false
    params: {type: object, properties: {base_id: {type: string}, table_id: {type: string}, record_id: {type: string}}, required: [base_id, table_id, record_id]}
    request: {method: GET, url: "https://api.airtable.com/v0/{{base_id}}/{{table_id}}/{{record_id}}"}
    response_extract: "$"
  - name: airtable_create_record
    description: "Create a record. fields is an object of column→value. Mutating."
    mutating: true
    params: {type: object, properties: {base_id: {type: string}, table_id: {type: string}, fields: {type: object}}, required: [base_id, table_id, fields]}
    request: {method: POST, url: "https://api.airtable.com/v0/{{base_id}}/{{table_id}}", body: {fields: "{{fields}}"}}
    response_extract: "$"
  - name: airtable_update_record
    description: "Update a record's fields by id (PATCH = partial). Mutating."
    mutating: true
    params: {type: object, properties: {base_id: {type: string}, table_id: {type: string}, record_id: {type: string}, fields: {type: object}}, required: [base_id, table_id, record_id, fields]}
    request: {method: PATCH, url: "https://api.airtable.com/v0/{{base_id}}/{{table_id}}/{{record_id}}", body: {fields: "{{fields}}"}}
    response_extract: "$"
  - name: airtable_delete_record
    description: "Delete a record by id. Mutating and irreversible."
    mutating: true
    params: {type: object, properties: {base_id: {type: string}, table_id: {type: string}, record_id: {type: string}}, required: [base_id, table_id, record_id]}
    request: {method: DELETE, url: "https://api.airtable.com/v0/{{base_id}}/{{table_id}}/{{record_id}}"}
    response_extract: "$"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Airtable -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Airtable provider (api-key) + 7 record/base actions`.

---

## Task 5: SendGrid (api_key, nested mail body)

**Files:** Create `providers/sendgrid.yaml`, `connectors/sendgrid.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test:**

```go
func TestB2_SendGrid(t *testing.T) {
	r := b2Reg(t)
	if p, ok := r.ProviderByName("sendgrid"); !ok || !p.IsAPIKey() {
		t.Fatal("sendgrid must load as api_key")
	}
	m := renderB2(t, r, "sendgrid", "sendgrid_send_mail", map[string]any{"to": "a@b.com", "from": "me@x.com", "subject": "S", "body": "hi"})
	ps, ok := m["personalizations"].([]any)
	if !ok || len(ps) != 1 {
		t.Fatalf("personalizations[] missing: %v", m)
	}
	to := ps[0].(map[string]any)["to"].([]any)
	if to[0].(map[string]any)["email"] != "a@b.com" {
		t.Fatalf("nested to.email wrong: %v", m)
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/sendgrid.yaml`**

```yaml
name: sendgrid
label: SendGrid
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "SendGrid API key"
  key_hint: "SG...."
  setup_url: https://app.sendgrid.com/settings/api_keys
```

- [ ] **Step 4: Create `connectors/sendgrid.yaml`**

```yaml
provider: sendgrid
actions:
  - name: sendgrid_send_mail
    description: "Send a plain-text email via SendGrid. Delivers real mail. Mutating."
    mutating: true
    params:
      type: object
      properties:
        to: {type: string}
        from: {type: string}
        subject: {type: string}
        body: {type: string}
      required: [to, from, subject, body]
    request:
      method: POST
      url: "https://api.sendgrid.com/v3/mail/send"
      body:
        personalizations:
          - to:
              - {email: "{{to}}"}
        from: {email: "{{from}}"}
        subject: "{{subject}}"
        content:
          - {type: "text/plain", value: "{{body}}"}
    response_extract: "$"
  - name: sendgrid_list_templates
    description: "List SendGrid dynamic templates. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.sendgrid.com/v3/templates", query: {generations: "dynamic"}}
    response_extract: "$.result"
  - name: sendgrid_get_template
    description: "Get a SendGrid template by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.sendgrid.com/v3/templates/{{id}}"}
    response_extract: "$"
  - name: sendgrid_list_lists
    description: "List SendGrid marketing contact lists. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.sendgrid.com/v3/marketing/lists"}
    response_extract: "$.result"
  - name: sendgrid_add_contact
    description: "Add or update a marketing contact by email. Mutating."
    mutating: true
    params: {type: object, properties: {email: {type: string}}, required: [email]}
    request:
      method: PUT
      url: "https://api.sendgrid.com/v3/marketing/contacts"
      body:
        contacts:
          - {email: "{{email}}"}
    response_extract: "$"
  - name: sendgrid_list_bounces
    description: "List bounced addresses. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.sendgrid.com/v3/suppression/bounces"}
    response_extract: "$"
  - name: sendgrid_list_verified_senders
    description: "List verified sender identities. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.sendgrid.com/v3/verified_senders"}
    response_extract: "$.results"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_SendGrid -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): SendGrid provider (api-key) + send/templates/contacts actions`.

---

## Task 6: Intercom (api_key + version header)

**Files:** Create `providers/intercom.yaml`, `connectors/intercom.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test** (verify the static version header is set on a request):

```go
func TestB2_Intercom(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("intercom")
	if !ok || !p.IsAPIKey() {
		t.Fatal("intercom must load as api_key")
	}
	if p.StaticHeaders["Intercom-Version"] == "" {
		t.Fatal("intercom must set a static Intercom-Version header")
	}
	if len(r.Actions("intercom")) < 8 {
		t.Fatalf("want >=8 intercom actions, got %d", len(r.Actions("intercom")))
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/intercom.yaml`**

```yaml
name: intercom
label: Intercom
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Intercom access token"
  key_hint: "dG9r...="
  setup_url: https://developers.intercom.com/building-apps/docs/authentication-types
static_headers:
  Intercom-Version: "2.11"
```

- [ ] **Step 4: Create `connectors/intercom.yaml`**

```yaml
provider: intercom
actions:
  - name: intercom_list_contacts
    description: "List Intercom contacts. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.intercom.io/contacts"}
    response_extract: "$.data"
  - name: intercom_get_contact
    description: "Get an Intercom contact by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.intercom.io/contacts/{{id}}"}
    response_extract: "$"
  - name: intercom_create_contact
    description: "Create an Intercom contact (role user or lead). Mutating."
    mutating: true
    params: {type: object, properties: {email: {type: string}, name: {type: string}, role: {type: string}}, required: [email]}
    request: {method: POST, url: "https://api.intercom.io/contacts", body: {email: "{{email}}", name: "{{name}}", role: "{{role}}"}}
    response_extract: "$"
  - name: intercom_update_contact
    description: "Update an Intercom contact by id (name/email/phone). Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, name: {type: string}, email: {type: string}, phone: {type: string}}, required: [id]}
    request: {method: PUT, url: "https://api.intercom.io/contacts/{{id}}", body: {name: "{{name}}", email: "{{email}}", phone: "{{phone}}"}}
    response_extract: "$"
  - name: intercom_list_conversations
    description: "List Intercom conversations. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.intercom.io/conversations"}
    response_extract: "$.conversations"
  - name: intercom_get_conversation
    description: "Get an Intercom conversation by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.intercom.io/conversations/{{id}}"}
    response_extract: "$"
  - name: intercom_reply_conversation
    description: "Reply to a conversation as an admin. Delivers a real reply. Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, admin_id: {type: string}, body: {type: string}}, required: [id, admin_id, body]}
    request:
      method: POST
      url: "https://api.intercom.io/conversations/{{id}}/reply"
      body: {message_type: "comment", type: "admin", admin_id: "{{admin_id}}", body: "{{body}}"}
    response_extract: "$"
  - name: intercom_create_note
    description: "Add a note to a contact. Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, body: {type: string}, admin_id: {type: string}}, required: [id, body]}
    request: {method: POST, url: "https://api.intercom.io/contacts/{{id}}/notes", body: {body: "{{body}}", admin_id: "{{admin_id}}"}}
    response_extract: "$"
```

> Note: `intercom_update_contact` uses `body: "{{fields}}"` — a lone-placeholder whose value is the whole object; `renderBody` returns the object as the entire body. Verify `renderRequest` marshals a top-level object body correctly (it does: `renderBody` returns the map, `json.Marshal` emits it).

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Intercom -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Intercom provider (api-key + version header) + 8 actions`.

---

## Task 7: ClickUp (api_key, no Bearer prefix)

**Files:** Create `providers/clickup.yaml`, `connectors/clickup.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test** (verify the raw-token header via `applyAuth`):

```go
func TestB2_ClickUp(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("clickup")
	if !ok || !p.IsAPIKey() {
		t.Fatal("clickup must load as api_key")
	}
	if p.Auth.ValuePrefix != "" {
		t.Fatalf("clickup token must have empty value_prefix, got %q", p.Auth.ValuePrefix)
	}
	req, _ := http.NewRequest("GET", "https://api.clickup.com/api/v2/task/x", nil)
	applyAuth(req, p, "pk_123")
	if got := req.Header.Get("Authorization"); got != "pk_123" {
		t.Fatalf("clickup Authorization must be the raw token, got %q", got)
	}
	if len(r.Actions("clickup")) < 8 {
		t.Fatalf("want >=8 clickup actions, got %d", len(r.Actions("clickup")))
	}
}
```

(Ensure `b2_test.go` imports `net/http`.)

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/clickup.yaml`**

```yaml
name: clickup
label: ClickUp
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: ""
  key_label: "ClickUp personal API token"
  key_hint: "pk_..."
  setup_url: https://app.clickup.com/settings/apps
```

- [ ] **Step 4: Create `connectors/clickup.yaml`**

```yaml
provider: clickup
actions:
  - name: clickup_list_spaces
    description: "List spaces in a ClickUp team (workspace). Read-only."
    mutating: false
    params: {type: object, properties: {team_id: {type: string}}, required: [team_id]}
    request: {method: GET, url: "https://api.clickup.com/api/v2/team/{{team_id}}/space"}
    response_extract: "$.spaces"
  - name: clickup_list_folders
    description: "List folders in a space. Read-only."
    mutating: false
    params: {type: object, properties: {space_id: {type: string}}, required: [space_id]}
    request: {method: GET, url: "https://api.clickup.com/api/v2/space/{{space_id}}/folder"}
    response_extract: "$.folders"
  - name: clickup_list_lists
    description: "List lists in a folder. Read-only."
    mutating: false
    params: {type: object, properties: {folder_id: {type: string}}, required: [folder_id]}
    request: {method: GET, url: "https://api.clickup.com/api/v2/folder/{{folder_id}}/list"}
    response_extract: "$.lists"
  - name: clickup_list_tasks
    description: "List tasks in a list. Read-only."
    mutating: false
    params: {type: object, properties: {list_id: {type: string}}, required: [list_id]}
    request: {method: GET, url: "https://api.clickup.com/api/v2/list/{{list_id}}/task"}
    response_extract: "$.tasks"
  - name: clickup_get_task
    description: "Get a ClickUp task by id. Read-only."
    mutating: false
    params: {type: object, properties: {task_id: {type: string}}, required: [task_id]}
    request: {method: GET, url: "https://api.clickup.com/api/v2/task/{{task_id}}"}
    response_extract: "$"
  - name: clickup_create_task
    description: "Create a task in a list. Mutating."
    mutating: true
    params: {type: object, properties: {list_id: {type: string}, name: {type: string}, description: {type: string}}, required: [list_id, name]}
    request: {method: POST, url: "https://api.clickup.com/api/v2/list/{{list_id}}/task", body: {name: "{{name}}", description: "{{description}}"}}
    response_extract: "$"
  - name: clickup_update_task
    description: "Update a task by id (name/description/status). Mutating."
    mutating: true
    params: {type: object, properties: {task_id: {type: string}, name: {type: string}, description: {type: string}, status: {type: string}}, required: [task_id]}
    request: {method: PUT, url: "https://api.clickup.com/api/v2/task/{{task_id}}", body: {name: "{{name}}", description: "{{description}}", status: "{{status}}"}}
    response_extract: "$"
  - name: clickup_delete_task
    description: "Delete a task by id. Mutating and irreversible."
    mutating: true
    params: {type: object, properties: {task_id: {type: string}}, required: [task_id]}
    request: {method: DELETE, url: "https://api.clickup.com/api/v2/task/{{task_id}}"}
    response_extract: "$"
  - name: clickup_add_comment
    description: "Add a comment to a task. Mutating."
    mutating: true
    params: {type: object, properties: {task_id: {type: string}, comment_text: {type: string}}, required: [task_id, comment_text]}
    request: {method: POST, url: "https://api.clickup.com/api/v2/task/{{task_id}}/comment", body: {comment_text: "{{comment_text}}"}}
    response_extract: "$"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_ClickUp -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): ClickUp provider (api-key, raw-token header) + 9 actions`.

---

## Task 8: Monday (api_key, GraphQL bodies)

**Files:** Create `providers/monday.yaml`, `connectors/monday.yaml`; modify `b2_test.go`.

Monday is a single GraphQL endpoint; each action is a fixed query/mutation string + a `variables` object.

- [ ] **Step 1: Test:**

```go
func TestB2_Monday(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("monday")
	if !ok || !p.IsAPIKey() || p.Auth.ValuePrefix != "" {
		t.Fatal("monday must load as api_key with empty value_prefix")
	}
	m := renderB2(t, r, "monday", "monday_create_item", map[string]any{"board_id": "1", "item_name": "hi"})
	if _, ok := m["query"].(string); !ok {
		t.Fatalf("monday body must carry a graphql query string: %v", m)
	}
	v, ok := m["variables"].(map[string]any)
	if !ok || v["item_name"] != "hi" {
		t.Fatalf("monday variables not built: %v", m)
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/monday.yaml`**

```yaml
name: monday
label: Monday.com
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: ""
  key_label: "Monday.com API token"
  key_hint: "eyJ..."
  setup_url: https://developer.monday.com/api-reference/docs/authentication
```

- [ ] **Step 4: Create `connectors/monday.yaml`** (all POST to the one endpoint; body = `{query, variables}`)

```yaml
provider: monday
actions:
  - name: monday_list_boards
    description: "List Monday.com boards (id, name). Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "query ($limit: Int) { boards (limit: $limit) { id name state } }"
        variables: {limit: "{{limit}}"}
    response_extract: "$.data"
  - name: monday_list_board_items
    description: "List items on a board via items_page. Read-only."
    mutating: false
    params: {type: object, properties: {board_id: {type: string}}, required: [board_id]}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "query ($board: [ID!]) { boards (ids: $board) { items_page { items { id name } } } }"
        variables: {board: "{{board_id}}"}
    response_extract: "$.data"
  - name: monday_list_groups
    description: "List groups on a board. Read-only."
    mutating: false
    params: {type: object, properties: {board_id: {type: string}}, required: [board_id]}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "query ($board: [ID!]) { boards (ids: $board) { groups { id title } } }"
        variables: {board: "{{board_id}}"}
    response_extract: "$.data"
  - name: monday_list_users
    description: "List Monday.com users. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "query { users { id name email } }"
    response_extract: "$.data"
  - name: monday_create_item
    description: "Create an item on a board (optionally in a group). Mutating."
    mutating: true
    params: {type: object, properties: {board_id: {type: string}, group_id: {type: string}, item_name: {type: string}}, required: [board_id, item_name]}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "mutation ($board: ID!, $group: String, $name: String!) { create_item (board_id: $board, group_id: $group, item_name: $name) { id } }"
        variables: {board: "{{board_id}}", group: "{{group_id}}", name: "{{item_name}}"}
    response_extract: "$.data"
  - name: monday_update_column_values
    description: "Update an item's column values. values is a JSON string of {columnId: value}. Mutating."
    mutating: true
    params: {type: object, properties: {board_id: {type: string}, item_id: {type: string}, values: {type: string}}, required: [board_id, item_id, values]}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "mutation ($board: ID!, $item: ID!, $vals: JSON!) { change_multiple_column_values (board_id: $board, item_id: $item, column_values: $vals) { id } }"
        variables: {board: "{{board_id}}", item: "{{item_id}}", vals: "{{values}}"}
    response_extract: "$.data"
  - name: monday_create_update
    description: "Post an update (comment) on an item. Mutating."
    mutating: true
    params: {type: object, properties: {item_id: {type: string}, body: {type: string}}, required: [item_id, body]}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "mutation ($item: ID!, $body: String!) { create_update (item_id: $item, body: $body) { id } }"
        variables: {item: "{{item_id}}", body: "{{body}}"}
    response_extract: "$.data"
  - name: monday_archive_item
    description: "Archive an item by id. Mutating."
    mutating: true
    params: {type: object, properties: {item_id: {type: string}}, required: [item_id]}
    request:
      method: POST
      url: "https://api.monday.com/v2"
      body:
        query: "mutation ($item: ID!) { archive_item (item_id: $item) { id } }"
        variables: {item: "{{item_id}}"}
    response_extract: "$.data"
```

> Note: GraphQL variables typed `Int`/`ID!` receive whatever type the agent passed; string ids are valid for `ID!`. `limit` should be passed as an integer by the caller. `monday_update_column_values` takes `values` as a JSON **string** (Monday's `JSON` scalar). These are documented in the descriptions; correctness of the GraphQL itself is unit-checked for body shape only (live GraphQL validation is deferred).

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Monday -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Monday.com provider (api-key) + 8 GraphQL actions`.

---

## Task 9: Dropbox (OAuth2)

**Files:** Create `providers/dropbox.yaml`, `connectors/dropbox.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test:**

```go
func TestB2_Dropbox(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("dropbox")
	if !ok || p.IsAPIKey() {
		t.Fatal("dropbox must load as OAuth (not api_key)")
	}
	if p.AuthorizeURL == "" || p.TokenURL == "" {
		t.Fatal("dropbox missing OAuth endpoints")
	}
	m := renderB2(t, r, "dropbox", "dropbox_list_folder", map[string]any{"path": "/docs"})
	if m["path"] != "/docs" {
		t.Fatalf("dropbox list_folder body: %v", m)
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/dropbox.yaml`**

```yaml
name: dropbox
label: Dropbox
authorize_url: https://www.dropbox.com/oauth2/authorize
token_url: https://api.dropboxapi.com/oauth2/token
default_scopes:
  - files.metadata.read
  - files.content.read
  - files.content.write
  - sharing.write
authorize_extra:
  token_access_type: offline
label_help: ""
setup_url: https://www.dropbox.com/developers/apps
setup_steps:
  - "Go to dropbox.com/developers/apps → Create app → Scoped access → Full Dropbox."
  - "On the Permissions tab, enable files.metadata.read, files.content.read, files.content.write, sharing.write."
  - "On Settings, add the redirect URI shown above under OAuth 2 Redirect URIs."
  - "Copy the App key (client id) and App secret (client secret) and paste them below."
```

> Remove the stray `label_help: ""` line if the Provider struct has no such field (it does not) — it will be ignored by yaml.v3, but omit it to keep the file clean. Keep `label:` set to "Dropbox".

- [ ] **Step 4: Create `connectors/dropbox.yaml`** (Dropbox RPC endpoints are all POST + JSON)

```yaml
provider: dropbox
actions:
  - name: dropbox_list_folder
    description: "List entries in a Dropbox folder path (use \"\" for the root). Read-only."
    mutating: false
    params: {type: object, properties: {path: {type: string}}, required: [path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/list_folder", body: {path: "{{path}}"}}
    response_extract: "$.entries"
  - name: dropbox_get_metadata
    description: "Get metadata for a file/folder path. Read-only."
    mutating: false
    params: {type: object, properties: {path: {type: string}}, required: [path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/get_metadata", body: {path: "{{path}}"}}
    response_extract: "$"
  - name: dropbox_search
    description: "Search files/folders by query string. Read-only."
    mutating: false
    params: {type: object, properties: {query: {type: string}}, required: [query]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/search_v2", body: {query: "{{query}}"}}
    response_extract: "$.matches"
  - name: dropbox_create_folder
    description: "Create a folder at a path. Safe write."
    mutating: false
    params: {type: object, properties: {path: {type: string}}, required: [path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/create_folder_v2", body: {path: "{{path}}"}}
    response_extract: "$"
  - name: dropbox_move
    description: "Move/rename a file or folder. Mutating."
    mutating: true
    params: {type: object, properties: {from_path: {type: string}, to_path: {type: string}}, required: [from_path, to_path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/move_v2", body: {from_path: "{{from_path}}", to_path: "{{to_path}}"}}
    response_extract: "$"
  - name: dropbox_copy
    description: "Copy a file or folder. Safe write."
    mutating: false
    params: {type: object, properties: {from_path: {type: string}, to_path: {type: string}}, required: [from_path, to_path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/copy_v2", body: {from_path: "{{from_path}}", to_path: "{{to_path}}"}}
    response_extract: "$"
  - name: dropbox_delete
    description: "Delete a file or folder at a path. Mutating and irreversible."
    mutating: true
    params: {type: object, properties: {path: {type: string}}, required: [path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/files/delete_v2", body: {path: "{{path}}"}}
    response_extract: "$"
  - name: dropbox_create_shared_link
    description: "Create a shareable link for a path. Mutating (creates a public-ish link)."
    mutating: true
    params: {type: object, properties: {path: {type: string}}, required: [path]}
    request: {method: POST, url: "https://api.dropboxapi.com/2/sharing/create_shared_link_with_settings", body: {path: "{{path}}"}}
    response_extract: "$"
```

> File **upload** (multipart/`Dropbox-API-Arg` header) is deferred.

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Dropbox -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Dropbox provider (OAuth) + 8 file actions`.

---

## Task 10: Zoom (OAuth2, Basic token auth)

**Files:** Create `providers/zoom.yaml`, `connectors/zoom.yaml`; modify `b2_test.go`.

- [ ] **Step 1: Test:**

```go
func TestB2_Zoom(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("zoom")
	if !ok || p.IsAPIKey() {
		t.Fatal("zoom must load as OAuth")
	}
	if p.TokenAuth != "basic" {
		t.Fatalf("zoom token endpoint must use basic auth, got %q", p.TokenAuth)
	}
	m := renderB2(t, r, "zoom", "zoom_create_meeting", map[string]any{"topic": "Sync", "type": float64(2)})
	if m["topic"] != "Sync" {
		t.Fatalf("zoom create_meeting body: %v", m)
	}
}
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Create `providers/zoom.yaml`**

```yaml
name: zoom
label: Zoom
authorize_url: https://zoom.us/oauth/authorize
token_url: https://zoom.us/oauth/token
token_auth: basic
default_scopes:
  - meeting:read
  - meeting:write
  - user:read
  - recording:read
setup_url: https://marketplace.zoom.us/develop/create
setup_steps:
  - "Go to marketplace.zoom.us → Develop → Build App → General App (User-managed)."
  - "Add the redirect URI shown above under OAuth."
  - "Add the scopes listed for this connector, then copy the Client ID and Client Secret below."
```

- [ ] **Step 4: Create `connectors/zoom.yaml`**

```yaml
provider: zoom
actions:
  - name: zoom_list_meetings
    description: "List the authenticated user's scheduled Zoom meetings. Read-only."
    mutating: false
    params: {type: object, properties: {type: {type: string, description: "scheduled|live|upcoming"}}}
    request: {method: GET, url: "https://api.zoom.us/v2/users/me/meetings", query: {type: "{{type}}"}}
    response_extract: "$.meetings"
  - name: zoom_get_meeting
    description: "Get a Zoom meeting by id. Read-only."
    mutating: false
    params: {type: object, properties: {meeting_id: {type: string}}, required: [meeting_id]}
    request: {method: GET, url: "https://api.zoom.us/v2/meetings/{{meeting_id}}"}
    response_extract: "$"
  - name: zoom_create_meeting
    description: "Create a Zoom meeting for the authenticated user. type 2 = scheduled. Mutating."
    mutating: true
    params:
      type: object
      properties:
        topic: {type: string}
        type: {type: integer, description: "1 instant, 2 scheduled"}
        start_time: {type: string, description: "ISO-8601, e.g. 2026-08-01T15:00:00Z"}
        duration: {type: integer, description: "minutes"}
      required: [topic]
    request:
      method: POST
      url: "https://api.zoom.us/v2/users/me/meetings"
      body: {topic: "{{topic}}", type: "{{type}}", start_time: "{{start_time}}", duration: "{{duration}}"}
    response_extract: "$"
  - name: zoom_update_meeting
    description: "Update a Zoom meeting by id (topic/start_time/duration/agenda). Mutating."
    mutating: true
    params: {type: object, properties: {meeting_id: {type: string}, topic: {type: string}, start_time: {type: string}, duration: {type: integer}, agenda: {type: string}}, required: [meeting_id]}
    request: {method: PATCH, url: "https://api.zoom.us/v2/meetings/{{meeting_id}}", body: {topic: "{{topic}}", start_time: "{{start_time}}", duration: "{{duration}}", agenda: "{{agenda}}"}}
    response_extract: "$"
  - name: zoom_delete_meeting
    description: "Delete a Zoom meeting by id. Mutating."
    mutating: true
    params: {type: object, properties: {meeting_id: {type: string}}, required: [meeting_id]}
    request: {method: DELETE, url: "https://api.zoom.us/v2/meetings/{{meeting_id}}"}
    response_extract: "$"
  - name: zoom_list_users
    description: "List Zoom users on the account. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.zoom.us/v2/users"}
    response_extract: "$.users"
  - name: zoom_get_user
    description: "Get a Zoom user by id or email ('me' for self). Read-only."
    mutating: false
    params: {type: object, properties: {user_id: {type: string}}, required: [user_id]}
    request: {method: GET, url: "https://api.zoom.us/v2/users/{{user_id}}"}
    response_extract: "$"
  - name: zoom_list_recordings
    description: "List the authenticated user's cloud recordings. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.zoom.us/v2/users/me/recordings"}
    response_extract: "$.meetings"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB2_Zoom -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Zoom provider (OAuth, basic token auth) + 8 meeting actions`.

---

## Task 11: Expose the 10 providers in the UI

**Files:** Modify `web/handlers_services.go`.

**Interfaces:** Consumes all 10 providers (Tasks 1-10). The auth-kind-aware card (Foundation) already renders api_key vs OAuth.

- [ ] **Step 1: Write the failing test** — add to `internal/connectors/b2_test.go` a guard that all 10 load with the expected auth kind (this is the real invariant; the `availableServiceProviders` slice itself is web-package state verified by build):

```go
func TestB2_AllProvidersLoad(t *testing.T) {
	r := b2Reg(t)
	apiKey := []string{"hubspot", "calendly", "asana", "airtable", "sendgrid", "intercom", "clickup", "monday"}
	oauth := []string{"dropbox", "zoom"}
	for _, name := range apiKey {
		p, ok := r.ProviderByName(name)
		if !ok || !p.IsAPIKey() {
			t.Fatalf("%s should load as api_key", name)
		}
		if len(r.Actions(name)) == 0 {
			t.Fatalf("%s has no actions", name)
		}
	}
	for _, name := range oauth {
		p, ok := r.ProviderByName(name)
		if !ok || p.IsAPIKey() {
			t.Fatalf("%s should load as OAuth", name)
		}
		if len(r.Actions(name)) == 0 {
			t.Fatalf("%s has no actions", name)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB2_AllProvidersLoad -count=1` → PASS if Tasks 1-10 done (this task's real work is the UI list).

- [ ] **Step 3: Add the 10 to `availableServiceProviders`** in `web/handlers_services.go`:

```go
var availableServiceProviders = []string{
	"google", "github", "notion", "outlook", "jira", "slack", "openai",
	"google_drive", "google_sheets", "google_docs", "teams",
	"hubspot", "calendly", "asana", "airtable", "sendgrid", "intercom", "clickup", "monday", "dropbox", "zoom",
}
```

- [ ] **Step 4: Build + smoke** — `go build -o bin/simple-agents ./cmd/simple-agents && go test ./internal/connectors/... ./web/... -count=1`. Manual: `make deploy`, open `/dashboard/connectors/services` — confirm the 8 api_key providers show a paste-key form and Dropbox/Zoom show the OAuth creds form.

- [ ] **Step 5: Commit**

```bash
git add web/handlers_services.go internal/connectors/b2_test.go
git commit -m "feat(web): expose B2 providers (HubSpot, Asana, Airtable, SendGrid, Calendly, Intercom, ClickUp, Monday, Dropbox, Zoom)"
```

---

## Self-Review

**Spec coverage:** all 10 providers (Tasks 1-10) with the spec's auth kinds (api_key ×8 with Bearer/raw-prefix/version-header variants; OAuth ×2 incl. Zoom `token_auth: basic`, Dropbox `token_access_type: offline`); ~8-10 actions each; Monday GraphQL bodies; SendGrid nested mail body; UI exposure (Task 11); multipart uploads deferred. ✓

**Placeholder scan:** No TBD/TODO. The `label_help` note in Task 9 is a concrete "omit this line" instruction. Action selections are complete YAML, not "verify later."

**Type consistency:** every task uses `b2Reg`/`renderB2` (defined Task 1), `ProviderByName`/`Action`/`Actions`/`IsAPIKey`/`Auth.ValuePrefix`/`StaticHeaders`/`TokenAuth`/`AuthorizeURL`/`TokenURL`/`applyAuth` — all existing symbols from the Foundation/registry. `availableServiceProviders` extended once (Task 11). Provider slugs consistent between provider file `name:`, connector file `provider:`, tests, and the UI list.

**Notes for the executor:**
- `b2_test.go` needs imports `encoding/json`, `testing`, and (from Task 7) `net/http`. Add `net/http` when Task 7 lands.
- **Every `body:` in this plan is a YAML map** (required, since `RequestTemplate.Body` is `map[string]any` — a scalar `body:` value would fail `LoadBundled`). Object args are passed via a map with a lone-placeholder value (`body: {properties: "{{properties}}"}`, `body: {data: "{{fields}}"}`) or via explicit named fields (the update actions). Do not write `body: "{{arg}}"` at the top level.
- The update actions (`intercom_update_contact`, `clickup_update_task`, `zoom_update_meeting`) use explicit named fields (partial update — absent fields are omitted by `renderBody`), matching their create-action style.
