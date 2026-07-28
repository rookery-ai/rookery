# Connector Actions Browser Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a workspace owner see every action a connected service exposes, from a read-only view inside the existing service slide-over.

**Architecture:** A new read-only JSON endpoint serves a provider's curated action manifest from `connectors.Registry` (embedded data, no DB). The SPA fetches it lazily from a third view of `ServiceWizard`, reached by a button and dismissed by Back. Nothing about how agents discover or execute actions changes.

**Tech Stack:** Go 1.x + Echo v4 (backend), React 19 + TanStack Query v5 + Tailwind v4 + Vitest/Testing Library (frontend).

**Spec:** `docs/superpowers/specs/2026-07-28-connector-actions-browser-design.md`

**Worktree:** `/home/rookie/simple-agents-v2/.claude/worktrees/connector-actions-panel`, branch `worktree-connector-actions-panel`. Run every command from the worktree root.

## Global Constraints

- **Never serialize `connectors.Action.Request` or `connectors.Action.ResponseExtract`.** HTTP method, URL/query/body templates, and the extraction path are internal plumbing. Task 1 has a test sweeping every provider for this.
- **Array fields serialize as `[]`, never `null`.** Existing convention, asserted in `TestAPIServices_GET_Authed_ListsGoogle`.
- **The React Query key root for actions is `["service-actions", provider]`** — never `["services", …]`. React Query invalidates by key *prefix*, and every connect/disconnect mutation calls `invalidateQueries({queryKey:["services"]})`, which would evict the action cache.
- **No execute/"try it" affordance.** This feature is display-only. Do not add a button that calls `connectors.Execute`.
- **Conventional Commits** (`type(scope): summary`). Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`.
- **Go tests:** `go test ./web/... -count=1`. **UI tests:** `cd web/ui && npx vitest run <path>`.
- Tone classes available in `web/ui/src/index.css`: `bg-ok-soft`/`text-ok`, `bg-warn-soft`/`text-warn`, `bg-danger-soft`/`text-danger`, `bg-muted-surface`/`text-muted-2`, `border-border`.

---

### Task 1: Actions endpoint + action_count

**Files:**
- Modify: `web/api_services.go` (DTOs near line 28–56; `registerServicesAPI` line 20–26; `apiListServices` line 86–158)
- Modify: `web/api_parity_test.go:39-43` (the `want` table)
- Test: `web/api_services_test.go` (append)

**Interfaces:**
- Consumes: `s.connectors` (`*connectors.Registry`) — already a `Server` field, used by `apiListServices`. Methods `ProviderByName(name) (Provider, bool)` and `Actions(provider) []Action` both already exist.
- Produces: `GET /api/v1/services/:provider/actions` → `{"actions":[{name,description,mutating,public_write,params}]}`; and `action_count` on every element of `GET /api/v1/services`'s `providers` array.

**Background the implementer needs:**

`connectors.Action` (`internal/connectors/registry.go:206`) has fields `Name`, `Description`, `Mutating`, `PublicWrite`, `ParamsRaw`, `Request`, `ResponseExtract`, and `Params json.RawMessage` (the compiled JSON schema, built at load time from `ParamsRaw`). Only the first four plus `Params` are exposed.

`Params` is produced by `json.Marshal(action.ParamsRaw)`. When a manifest declares no `params:` block, `ParamsRaw` is a nil map and `Params` is the four bytes `null` — **not** empty. The handler must normalize that to `{}` or the frontend crashes reading `.properties` off `null`.

Registering `GET /services/:provider/actions` alongside the existing `DELETE /services/:id` mixes param names at the same path segment. This pattern already exists and works (`POST /services/:provider/creds` coexists with `DELETE /services/:id`), so it is not a new risk — but Step 2's run is what proves it.

- [ ] **Step 1: Write the failing tests**

Append to `web/api_services_test.go`:

```go
func TestAPIServices_ACTIONS_Unauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/services/github/actions", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIServices_ACTIONS_UnknownProvider(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services/not-a-real-provider/actions", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "not_found") {
		t.Fatalf("expected not_found code, got: %s", rec.Body.String())
	}
}

func TestAPIServices_ACTIONS_ListsGithubActions(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services/github/actions", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"github_search_issues"`) {
		t.Fatalf("expected github_search_issues in response, got: %s", body)
	}
	for _, key := range []string{`"description"`, `"mutating"`, `"public_write"`, `"params"`} {
		if !contains(body, key) {
			t.Fatalf("expected response to contain field %s, got: %s", key, body)
		}
	}
	if contains(body, `"actions":null`) {
		t.Fatalf("actions must serialize as [] not null: %s", body)
	}
}

// The action manifests are the only place request templates live. Leaking them
// through this endpoint would disclose how every request is built, for no reader
// benefit — so sweep EVERY provider rather than trusting one spot check.
func TestAPIServices_ACTIONS_NeverLeaksRequestPlumbing(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	listRec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	var list struct {
		Providers []struct {
			Name        string `json:"name"`
			ActionCount int    `json:"action_count"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding services list: %v", err)
	}
	if len(list.Providers) == 0 {
		t.Fatal("expected at least one provider")
	}

	for _, p := range list.Providers {
		rec := doJSON(t, s, http.MethodGet, "/api/v1/services/"+p.Name+"/actions", nil, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", p.Name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, banned := range []string{`"request":`, `"response_extract":`, `"body_builder":`} {
			if contains(body, banned) {
				t.Fatalf("%s: response leaked request plumbing %s: %s", p.Name, banned, body)
			}
		}
		if contains(body, `"params":null`) {
			t.Fatalf("%s: params must normalize to {} not null: %s", p.Name, body)
		}
	}
}

// action_count exists so the UI can render a count and hide the entry button at
// zero without a second fetch. If it drifts from the real list the button lies.
func TestAPIServices_ActionCountMatchesActionsEndpoint(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	listRec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	var list struct {
		Providers []struct {
			Name        string `json:"name"`
			ActionCount int    `json:"action_count"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding services list: %v", err)
	}

	for _, p := range list.Providers {
		rec := doJSON(t, s, http.MethodGet, "/api/v1/services/"+p.Name+"/actions", nil, cookies)
		var got struct {
			Actions []struct {
				Name string `json:"name"`
			} `json:"actions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: decoding actions: %v", p.Name, err)
		}
		if p.ActionCount != len(got.Actions) {
			t.Fatalf("%s: action_count=%d but endpoint returned %d actions",
				p.Name, p.ActionCount, len(got.Actions))
		}
	}
}
```

`encoding/json` and `net/http` are already imported in this file; no import changes needed.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web/... -count=1 -run 'TestAPIServices_ACTIONS|TestAPIServices_ActionCount'`

Expected: FAIL. The two `ACTIONS` endpoint tests get 404 from Echo (no such route) rather than the expected codes, and the `action_count` tests see `0 != N` because the field does not exist yet and decodes as zero.

- [ ] **Step 3: Add the DTOs**

In `web/api_services.go`, after the `apiServiceProvider` struct (around line 56), add:

```go
// apiConnectorAction is one curated action a provider exposes. Deliberately a
// SUBSET of connectors.Action: Request (method/URL/query/body templates) and
// ResponseExtract are internal plumbing — noise to a reader and a needless
// widening of what the API discloses about how requests are built.
type apiConnectorAction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Mutating    bool            `json:"mutating"`
	PublicWrite bool            `json:"public_write"`
	Params      json.RawMessage `json:"params"`
}

type apiProviderActionsResponse struct {
	Actions []apiConnectorAction `json:"actions"`
}
```

Add `"encoding/json"` to the import block at the top of the file (it is not currently imported there).

- [ ] **Step 4: Add `ActionCount` to the provider DTO and populate it**

In `web/api_services.go`, add the field to `apiServiceProvider` (after `HasCreds`):

```go
	// ActionCount lets the UI show a count and hide the actions entry point at
	// zero without a second fetch. The actions themselves stay OFF this payload:
	// it loads on every visit to the connections page, and ~214 actions with
	// their JSON schemas is a real regression on that critical path.
	ActionCount   int                      `json:"action_count"`
```

In `apiListServices`, add to the `apiServiceProvider{...}` literal at the end of the loop:

```go
			ActionCount:   len(s.connectors.Actions(provider)),
```

- [ ] **Step 5: Add the handler**

In `web/api_services.go`, after `apiListServices`, add:

```go
// apiListProviderActions lists the curated actions a provider exposes. GET
// /api/v1/services/:provider/actions → {"actions":[...]}; unknown provider → 404.
// Read-only over embedded manifest data: no DB access and nothing
// workspace-scoped, so an unconnected provider lists its actions too — "what can
// this do for me" is the strongest reason to connect in the first place.
func (s *Server) apiListProviderActions(c echo.Context) error {
	provider := c.Param("provider")
	if _, ok := s.connectors.ProviderByName(provider); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown provider: "+provider)
	}

	acts := s.connectors.Actions(provider)
	out := make([]apiConnectorAction, 0, len(acts))
	for _, a := range acts {
		// A manifest with no params: block compiles to the literal bytes `null`,
		// not to empty — normalize so the client can always read .properties.
		params := a.Params
		if len(params) == 0 || string(params) == "null" {
			params = json.RawMessage(`{}`)
		}
		out = append(out, apiConnectorAction{
			Name:        a.Name,
			Description: a.Description,
			Mutating:    a.Mutating,
			PublicWrite: a.PublicWrite,
			Params:      params,
		})
	}
	return c.JSON(http.StatusOK, apiProviderActionsResponse{Actions: out})
}
```

- [ ] **Step 6: Register the route**

In `registerServicesAPI` (`web/api_services.go:20`), add after the `GET /services` line:

```go
	g.GET("/services/:provider/actions", s.apiListProviderActions)
```

- [ ] **Step 7: Add the route to the parity table**

In `web/api_parity_test.go`, change the services block (line 39–43) so it reads:

```go
		"GET /api/v1/services", "GET /api/v1/services/:provider/actions",
		"POST /api/v1/services/:provider/creds",
		"POST /api/v1/services/:provider/connect", "POST /api/v1/services/:provider/apikey",
		"DELETE /api/v1/services/:id",
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./web/... -count=1`

Expected: PASS, including `TestAPIParityInventory` and the pre-existing
`TestAPIServices_GET_Authed_ListsGoogle` (which must not regress on the
`action_count` addition).

- [ ] **Step 9: Commit**

```bash
git add web/api_services.go web/api_services_test.go web/api_parity_test.go
git commit -m "feat(api): serve a provider's connector actions

GET /api/v1/services/:provider/actions returns the curated action manifest
(name, description, mutating, public_write, params). Request templates and
response_extract are never serialized. GET /api/v1/services gains a cheap
action_count so the UI can render a count without a second fetch.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Frontend data layer

**Files:**
- Modify: `web/ui/src/lib/connections.ts` (`ServiceProvider` type at line 69–79; append hook after `useServices` at line 122–127)
- Modify: `web/ui/src/lib/connections.test.ts:19-34` (`providerFixture`)
- Modify: `web/ui/src/pages/connections/connections.test.tsx` (3 inline provider fixtures, from line 56)
- Modify: `web/ui/src/pages/connections/ServiceWizard.test.tsx:29-92` (5 inline provider fixtures)
- Test: `web/ui/src/lib/connections.test.ts` (append a hook test)

**Interfaces:**
- Consumes: `GET /api/v1/services/:provider/actions` and the `action_count` field, both from Task 1.
- Produces: exported types `ConnectorActionParams`, `ConnectorAction`; hook `useProviderActions(provider: string)` returning TanStack Query's result over `{ actions: ConnectorAction[] }`; `action_count: number` on `ServiceProvider`.

**Note on the fixtures:** `action_count` is a *required* field on `ServiceProvider`, not optional. An optional field would let a fixture silently omit it and hide drift from the real payload. The cost is updating 9 fixture sites, which Step 3 enumerates exactly.

- [ ] **Step 1: Write the failing hook test**

Append to `web/ui/src/lib/connections.test.ts`:

Add `useProviderActions` to the **existing** `from "./connections"` import block
at the top of the file (do not add a second import statement from the same
module — oxlint flags duplicate imports). Then add these new imports and the
test block:

```ts
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";

describe("useProviderActions", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches the provider's actions endpoint and returns the list", async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            actions: [
              {
                name: "github_search_issues",
                description: "Search issues",
                mutating: false,
                public_write: false,
                params: { properties: { query: { type: "string" } }, required: ["query"] },
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children);

    const { result } = renderHook(() => useProviderActions("github"), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/services/github/actions",
      expect.anything(),
    );
    expect(result.current.data?.actions[0].name).toBe("github_search_issues");
  });
});
```

If `expect(fetchMock).toHaveBeenCalledWith(...)` fails on the second argument's
shape, relax it to assert on `String(fetchMock.mock.calls[0][0])` instead — the
URL is what this test is about, not `api.get`'s internal `RequestInit`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web/ui && npx vitest run src/lib/connections.test.ts`

Expected: FAIL — `useProviderActions` is not exported from `./connections`.

- [ ] **Step 3: Add `action_count` to the type and every fixture**

In `web/ui/src/lib/connections.ts`, add to the `ServiceProvider` type after `has_creds`:

```ts
  action_count: number;
```

Then add `action_count: 0,` to each of these 9 fixture objects (TypeScript will
error on every one you miss, so `tsc` is the checklist):

- `web/ui/src/lib/connections.test.ts` — inside `providerFixture`'s returned object (1)
- `web/ui/src/pages/connections/connections.test.tsx` — the `gmail`, `notion`, and `jira` objects in the `providers` array (3)
- `web/ui/src/pages/connections/ServiceWizard.test.tsx` — `OAUTH_NO_CREDS`, `OAUTH_WITH_CREDS`, `OAUTH_NEEDS_REAUTH`, `OAUTH_ACTIVE_CONN`, `API_KEY_PROVIDER` (5)

In `ServiceWizard.test.tsx` set `OAUTH_WITH_CREDS.action_count = 3` (Task 4 needs
one fixture with a non-zero count) and leave the other four at `0`.

- [ ] **Step 4: Add the action types and the hook**

In `web/ui/src/lib/connections.ts`, after `useServices` (line 127), add:

```ts
// Mirrors apiConnectorAction. `params` is the action's JSON Schema. The manifests
// only ever use flat object schemas, so nothing deeper is modeled here.
export type ConnectorActionParams = {
  properties?: Record<string, { type?: string; description?: string }>;
  required?: string[];
};

export type ConnectorAction = {
  name: string;
  description: string;
  mutating: boolean;
  public_write: boolean;
  params: ConnectorActionParams;
};

/**
 * Fetches one provider's curated action list.
 *
 * The key root is "service-actions", NOT "services": React Query invalidates by
 * key prefix, so every connect/disconnect mutation's
 * invalidateQueries({queryKey:["services"]}) would evict these lists too.
 *
 * staleTime: Infinity because the manifests are compiled into the binary via
 * go:embed and cannot change while the server runs. Fetched lazily — only the
 * actions view mounts this hook, so opening the wizard costs nothing.
 */
export function useProviderActions(provider: string) {
  return useQuery({
    queryKey: ["service-actions", provider],
    queryFn: () =>
      api.get<{ actions: ConnectorAction[] }>(
        `/api/v1/services/${provider}/actions`,
      ),
    staleTime: Infinity,
  });
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/lib/connections.test.ts src/pages/connections && npx tsc -b`

Expected: PASS, and `tsc` clean — a fixture missing `action_count` fails the type-check.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/lib/connections.ts web/ui/src/lib/connections.test.ts \
  web/ui/src/pages/connections/connections.test.tsx \
  web/ui/src/pages/connections/ServiceWizard.test.tsx
git commit -m "feat(web/connections): useProviderActions hook and action_count

Types mirroring apiConnectorAction plus a lazily-fetched, permanently-cached
hook keyed under service-actions so the services mutations' prefix invalidation
cannot evict it.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: ProviderActions component

**Files:**
- Create: `web/ui/src/pages/connections/ProviderActions.tsx`
- Create: `web/ui/src/pages/connections/ProviderActions.test.tsx`

**Interfaces:**
- Consumes: `useProviderActions`, `ConnectorAction`, `ServiceProvider` from Task 2.
- Produces: `export function ProviderActions({ provider, onBack }: { provider: ServiceProvider; onBack: () => void })`. It owns its header — provider label, action count, and the "← Back" control that invokes `onBack`. `ServiceWizard` (Task 4) supplies the callback and decides where Back lands.

**Why its own file:** `ServiceWizard.tsx` is already 359 lines; a second substantial UI inside it costs readability for no gain.

- [ ] **Step 1: Write the failing tests**

Create `web/ui/src/pages/connections/ProviderActions.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProviderActions } from "./ProviderActions";
import type { ConnectorAction, ServiceProvider } from "@/lib/connections";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const PROVIDER: ServiceProvider = {
  name: "github",
  label: "GitHub",
  category: "Developer",
  kind: "oauth",
  setup_url: "",
  setup_steps: [],
  has_creds: true,
  action_count: 3,
  connect_inputs: [],
  connections: [],
};

const ACTIONS: ConnectorAction[] = [
  {
    name: "github_search_issues",
    description: "Search issues and pull requests across your repos",
    mutating: false,
    public_write: false,
    params: {
      properties: {
        query: { type: "string", description: "GitHub issue search query" },
        max: { type: "integer", description: "max results" },
      },
      required: ["query"],
    },
  },
  {
    name: "github_create_issue",
    description: "Open a new issue in a repository",
    mutating: true,
    public_write: false,
    params: { properties: { owner: { type: "string" } }, required: ["owner"] },
  },
  {
    name: "github_comment_publicly",
    description: "Post a public comment on an issue",
    mutating: true,
    public_write: true,
    params: {},
  },
];

function mockActions(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(jsonResponse(body, status))),
  );
}

function wrap(onBack = () => {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ProviderActions provider={PROVIDER} onBack={onBack} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("lists each action's description with a capability badge", async () => {
  mockActions({ actions: ACTIONS });
  wrap();

  expect(
    await screen.findByText("Search issues and pull requests across your repos"),
  ).toBeInTheDocument();
  expect(screen.getByText("Open a new issue in a repository")).toBeInTheDocument();

  expect(screen.getByText("read")).toBeInTheDocument();
  expect(screen.getByText("writes")).toBeInTheDocument();
  expect(screen.getByText("posts publicly")).toBeInTheDocument();
});

test("rows start collapsed and expand independently to show tool name and params", async () => {
  const user = userEvent.setup();
  mockActions({ actions: ACTIONS });
  wrap();

  const first = await screen.findByRole("button", {
    name: /Search issues and pull requests/,
  });
  expect(screen.queryByText("github_search_issues")).not.toBeInTheDocument();

  await user.click(first);
  expect(screen.getByText("github_search_issues")).toBeInTheDocument();
  // "max" is the OPTIONAL param: its span is a single text node. "query" is
  // required, so its span renders "query" + "*" and would not match exactly.
  expect(screen.getByText("max")).toBeInTheDocument();
  expect(screen.getByText("— GitHub issue search query")).toBeInTheDocument();

  // A second row stays collapsed — expansion is per-row, not global.
  expect(screen.queryByText("github_create_issue")).not.toBeInTheDocument();
});

test("an action with no parameters says so instead of rendering an empty list", async () => {
  const user = userEvent.setup();
  mockActions({ actions: ACTIONS });
  wrap();

  const publicRow = await screen.findByRole("button", {
    name: /Post a public comment/,
  });
  await user.click(publicRow);
  expect(screen.getByText("No parameters.")).toBeInTheDocument();
});

test("Back invokes onBack", async () => {
  const user = userEvent.setup();
  mockActions({ actions: ACTIONS });
  const onBack = vi.fn();
  wrap(onBack);

  await user.click(await screen.findByRole("button", { name: /Back/ }));
  expect(onBack).toHaveBeenCalledTimes(1);
});

test("a failed fetch shows an error instead of an empty list", async () => {
  mockActions({ error: { message: "boom" } }, 500);
  wrap();

  expect(await screen.findByText(/Couldn't load actions/)).toBeInTheDocument();
});

test("a provider with no actions shows an empty note", async () => {
  mockActions({ actions: [] });
  wrap();

  expect(
    await screen.findByText("This service exposes no actions yet."),
  ).toBeInTheDocument();
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/connections/ProviderActions.test.tsx`

Expected: FAIL — cannot resolve `./ProviderActions`.

- [ ] **Step 3: Write the component**

Create `web/ui/src/pages/connections/ProviderActions.tsx`:

```tsx
import { useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  useProviderActions,
  type ConnectorAction,
  type ServiceProvider,
} from "@/lib/connections";

/**
 * The capability badge. The read / writes / posts-publicly split mirrors the
 * manifest's own mutating + public_write pair rather than collapsing to a single
 * "writes": pausing an ad campaign is mutating but private and reversible, while
 * a LinkedIn post is neither, and that is exactly the distinction a reader
 * deciding whether to connect an account cares about.
 */
function capability(action: ConnectorAction): { label: string; tone: string } {
  if (action.public_write) {
    return { label: "posts publicly", tone: "bg-danger-soft text-danger" };
  }
  if (action.mutating) {
    return { label: "writes", tone: "bg-warn-soft text-warn" };
  }
  return { label: "read", tone: "bg-muted-surface text-muted-2" };
}

function ActionRow({ action }: { action: ConnectorAction }) {
  const [open, setOpen] = useState(false);
  const cap = capability(action);
  const properties = action.params?.properties ?? {};
  const required = new Set(action.params?.required ?? []);
  const names = Object.keys(properties);
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div className="rounded-lg border border-border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-start justify-between gap-3 p-3 text-left"
      >
        <span className="min-w-0 text-sm leading-relaxed">{action.description}</span>
        <span className="flex shrink-0 items-center gap-2">
          <span
            className={cn(
              "rounded-full px-2 py-0.5 text-[11px] font-medium",
              cap.tone,
            )}
          >
            {cap.label}
          </span>
          <Chevron className="size-4 text-muted-2" aria-hidden="true" />
        </span>
      </button>

      {open && (
        <div className="space-y-2 border-t border-border px-3 py-2">
          <code className="block font-mono text-xs text-muted-2">{action.name}</code>
          {names.length === 0 ? (
            <p className="text-xs text-muted-2">No parameters.</p>
          ) : (
            <ul className="space-y-1">
              {names.map((n) => (
                <li key={n} className="flex flex-wrap gap-x-2 text-xs">
                  <span className="font-mono">
                    {n}
                    {required.has(n) && <span className="text-danger">*</span>}
                  </span>
                  {properties[n].type && (
                    <span className="text-muted-2">{properties[n].type}</span>
                  )}
                  {properties[n].description && (
                    // One template literal, not `— {expr}`: JSX interpolation
                    // would split this into two text nodes, and Testing Library's
                    // getByText matches per text node.
                    <span className="text-muted-2">{`— ${properties[n].description}`}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * A read-only reference list of everything a provider lets an agent do.
 *
 * Rendered for UNCONNECTED providers too: the manifests are static embedded data
 * with nothing account-specific in them, and "what can this do for me" is the
 * strongest reason to connect in the first place.
 */
export function ProviderActions({
  provider,
  onBack,
}: {
  provider: ServiceProvider;
  onBack: () => void;
}) {
  const { data, isLoading, isError } = useProviderActions(provider.name);
  const actions = data?.actions ?? [];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
          {provider.label} · {provider.action_count} action
          {provider.action_count === 1 ? "" : "s"}
        </div>
        <button
          type="button"
          onClick={onBack}
          className="shrink-0 text-xs text-muted-2 underline underline-offset-2 hover:text-foreground"
        >
          ← Back
        </button>
      </div>

      {isLoading && <div className="p-4 text-sm text-muted-2">Loading actions…</div>}

      {isError && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
          {`Couldn't load actions for ${provider.label}.`}
        </div>
      )}

      {!isLoading && !isError && actions.length === 0 && (
        <div className="p-4 text-sm text-muted-2">
          This service exposes no actions yet.
        </div>
      )}

      {actions.map((a) => (
        <ActionRow key={a.name} action={a} />
      ))}
    </div>
  );
}

export default ProviderActions;
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/connections/ProviderActions.test.tsx`

Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/connections/ProviderActions.tsx \
  web/ui/src/pages/connections/ProviderActions.test.tsx
git commit -m "feat(web/connections): ProviderActions reference list

Description-first rows with a read/writes/posts-publicly badge; tool name and
parameters behind a per-row expand so ten actions don't recreate the overload
the panel exists to avoid.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: Wire the actions view into ServiceWizard

**Files:**
- Modify: `web/ui/src/pages/connections/ServiceWizard.tsx` (imports line 1–19; state at line 133; render at line 189–355)
- Modify: `web/ui/src/pages/connections/ServiceWizard.test.tsx` (the `Handlers` type line 94–103 and `mockFetch` line 105–157; append tests)

**Interfaces:**
- Consumes: `ProviderActions` (Task 3), `action_count` (Task 2).
- Produces: nothing consumed by later tasks — this is the last task.

**Mechanism:** a `showActions` boolean *overlaying* the existing `view` state, not a widened `"creds" | "connect" | "actions"` union. `view` keeps meaning "which connect step am I on", so Back lands where the user left with no separate variable remembering it — the previous value was never overwritten. Because only the rendered body swaps and `ServiceWizard` itself stays mounted, `clientId` / `clientSecret` / `apiKey` / `label` / `inputs` survive the round trip untouched. This is the regression that ruled out opening a second slide-over: `AppShell`'s slide-over is a single slot, so `open()` would have unmounted the wizard and discarded typed input.

- [ ] **Step 1: Write the failing tests**

First extend the mock in `web/ui/src/pages/connections/ServiceWizard.test.tsx`.
Add to the `Handlers` type (line 94):

```ts
  actions?: (provider: string) => Response;
```

And inside `mockFetch`'s fetch implementation, before the `delMatch` block:

```ts
      const actionsMatch = url.match(/^\/api\/v1\/services\/([^/]+)\/actions$/);
      if (actionsMatch && method === "GET") {
        return Promise.resolve(
          handlers.actions
            ? handlers.actions(actionsMatch[1])
            : jsonResponse({
                actions: [
                  {
                    name: "github_search_issues",
                    description: "Search issues across your repos",
                    mutating: false,
                    public_write: false,
                    params: { properties: { query: { type: "string" } }, required: ["query"] },
                  },
                ],
              }),
        );
      }
```

Then append these tests:

```tsx
test("shows an actions entry button carrying the provider's action count", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS);
  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByRole("button", { name: /What can it do/ })).toBeInTheDocument();
  expect(screen.getByText(/3 actions/)).toBeInTheDocument();
});

test("a provider with no actions shows no entry button", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_NO_CREDS); // action_count: 0
  await user.click(screen.getByText("open wizard"));

  await screen.findByLabelText("Client ID");
  expect(screen.queryByRole("button", { name: /What can it do/ })).not.toBeInTheDocument();
});

test("opening the actions view replaces the connect body, and Back restores it", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS); // has_creds: true → opens on the connect view
  await user.click(screen.getByText("open wizard"));

  await screen.findByLabelText("Label (optional)");
  await user.click(await screen.findByRole("button", { name: /What can it do/ }));

  expect(await screen.findByText("Search issues across your repos")).toBeInTheDocument();
  expect(screen.queryByLabelText("Label (optional)")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /Back/ }));
  expect(await screen.findByLabelText("Label (optional)")).toBeInTheDocument();
});

// The regression that ruled out opening a second slide-over panel: the shell's
// slide-over is a single slot, so a real second panel would unmount the wizard
// and silently discard whatever the user had typed.
test("half-typed OAuth credentials survive a trip through the actions view", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS);
  await user.click(screen.getByText("open wizard"));

  // Jump to the creds view, where the sensitive fields live.
  await user.click(await screen.findByText("edit app credentials"));
  await user.type(await screen.findByLabelText("Client ID"), "typed-client-id");

  await user.click(screen.getByRole("button", { name: /What can it do/ }));
  await screen.findByText("Search issues across your repos");
  await user.click(screen.getByRole("button", { name: /Back/ }));

  expect(await screen.findByLabelText("Client ID")).toHaveValue("typed-client-id");
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`

Expected: FAIL — no button matching `/What can it do/` exists.

- [ ] **Step 3: Import ProviderActions and add the state**

In `web/ui/src/pages/connections/ServiceWizard.tsx`, add to the imports:

```tsx
import { ProviderActions } from "./ProviderActions";
```

And after the existing `view` state (line 133), add:

```tsx
  // Overlays `view` rather than widening its union: `view` keeps meaning "which
  // connect step am I on", so Back lands where the user left without a separate
  // variable remembering it. ServiceWizard stays mounted throughout, so every
  // form field below survives the round trip — which a second slide-over panel
  // could not have done (the shell's slide-over is a single slot).
  const [showActions, setShowActions] = useState(false);
```

- [ ] **Step 4: Render the entry button and the view swap**

Replace the `return (` block's opening (line 189–191), which currently reads:

```tsx
  return (
    <PanelBody>
      {hasConnections && (
```

with:

```tsx
  if (showActions) {
    return (
      <PanelBody>
        <ProviderActions provider={provider} onBack={() => setShowActions(false)} />
      </PanelBody>
    );
  }

  return (
    <PanelBody>
      {provider.action_count > 0 && (
        <button
          type="button"
          onClick={() => setShowActions(true)}
          className="flex w-full items-center justify-between gap-2 rounded-lg border border-border px-3 py-2 text-sm transition-colors hover:border-primary/40"
        >
          <span className="font-medium">What can it do?</span>
          <span className="shrink-0 text-xs text-muted-2">
            {provider.action_count} action{provider.action_count === 1 ? "" : "s"} →
          </span>
        </button>
      )}

      {hasConnections && (
```

The rest of the component is unchanged.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/connections && npx tsc -b`

Expected: PASS across `ServiceWizard.test.tsx`, `ProviderActions.test.tsx`, and
`connections.test.tsx`, with `tsc` clean.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s && cd web/ui && npm test && npx tsc -b && npm run lint`

Expected: all green. Report any failure with its output rather than proceeding.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/pages/connections/ServiceWizard.tsx \
  web/ui/src/pages/connections/ServiceWizard.test.tsx
git commit -m "feat(web/connections): actions view inside the service wizard

A 'What can it do? · N actions' button swaps the panel body for the read-only
action list and Back swaps it home. Implemented as a boolean overlaying `view`
so the wizard stays mounted and half-typed credentials survive the round trip.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Verification

After Task 4, confirm the feature end to end against a **temporary data dir**, never the operator's live install:

```bash
make build
SA_DATA_DIR=$(mktemp -d) SA_PORT=8099 ./bin/simple-agents serve
```

Then in a second shell:

```bash
curl -sS http://127.0.0.1:8099/api/v1/services/github/actions
```

Expected: 401 unauthenticated (the guard is working). The full UI check requires
logging in and entering a workspace through the SPA at `http://127.0.0.1:8099/`,
opening Connections → GitHub → "What can it do?".

## Out of scope (do not build)

- Any "try it" / "run this action" button. Display only.
- A global cross-provider action catalog.
- Converting `AppShell`'s slide-over into a stack.
- Changes to `connectors.ToolDefs`, `agent_connections`, or how agents bind actions.
