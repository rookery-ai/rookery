# Designer Chat Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the agent-edit chat open in the same chrome as every other chat (no full-width→10%-gutter jump, no invisible first turn) and give designer bubbles the same `Day HH:MM` timestamp the chats page already shows.

**Architecture:** Delete `AgentEditPage`'s bespoke pre-screen and mount the shared `DesignerSurface` unconditionally, adding two optional props — `startEndpoint` (route the first message of a fresh session to `/agents/:id/edit/start`) and `acceptRecoveredSession` (reject an unrelated recovered session, which the deleted `hasMatchingDraft` gate used to do). Separately, stamp `CreatedAt` on designer history entries in both flows and emit it as a `created_at` string through the three history DTOs, then thread it into `ChatMessageBubble`.

**Tech Stack:** Go (Echo v4, `internal/agentdesigner`, `internal/skilldesigner`), React 18 + TypeScript + Tailwind (`web/ui`), Vitest + Testing Library, `go test`.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-28-designer-chat-parity-design.md`.
- No new HTTP routes. `web/api_parity_test.go` must stay green unmodified.
- No DB migration. Draft history round-trips through the existing `history_json` columns.
- `created_at` on the wire is a **string** formatted `time.RFC3339Nano`, omitted entirely when the source `time.Time` is zero. Never a `time.Time` DTO field — `omitempty` is a no-op on structs and would emit `"0001-01-01T00:00:00Z"` for pre-existing drafts.
- `DesignerSurface` stays entity-agnostic: every new prop is optional and defaults to today's behavior, so `AgentNewPage` and `SkillNewPage` are behaviorally unchanged.
- Conventional Commits (`fix(web/ui): …`, `feat(web): …`). Branch only — never commit to `main`.
- Run from the repo root: `go test ./... -count=1` and `cd web/ui && npx vitest run`.

---

### Task 1: Stamp and expose designer history timestamps (agent designer)

**Files:**
- Modify: `internal/agentdesigner/flow.go` (5 `db.ChatMessage{…}` literals: ~1152, ~1153, ~1511, ~1651, ~1665)
- Modify: `web/handlers_agents.go` (the two inline `histEntry` structs at ~55 and ~201)
- Test: `web/api_agents_test.go`

**Interfaces:**
- Produces: `web.designHistoryDTO(hist []db.ChatMessage) []designHistEntry` — the single history-DTO mapper, reused by Task 2's skill resume handler. `designHistEntry` has JSON fields `role`, `content`, `created_at` (omitempty string).

- [ ] **Step 1: Write the failing test**

Append to `web/api_agents_test.go`:

```go
// TestAPIDesignStateHistoryCarriesTimestamps pins that design-conversation turns
// reach the browser with a time, so DesignerSurface's bubbles can render the same
// `Day HH:MM` footer the chats page shows. A turn with no CreatedAt (a draft
// written before timestamps existed) must OMIT the field rather than emit a zero
// time, which would render a bubble stamped year 1.
func TestAPIDesignStateHistoryCarriesTimestamps(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent"); err != nil {
		t.Fatalf("start design session: %v", err)
	}
	sess := s.designFlow.GetSession(wsID)
	if sess == nil {
		t.Fatal("no design session after Start")
	}
	stamped := time.Date(2026, 7, 28, 9, 30, 0, 0, time.UTC)
	sess.History = []db.ChatMessage{
		{Role: "user", Content: "stamped turn", CreatedAt: stamped},
		{Role: "assistant", Content: "legacy turn"}, // zero CreatedAt
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/design/state", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("design state: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"created_at":"2026-07-28T09:30:00Z"`) {
		t.Fatalf("stamped history turn lost its created_at: %s", body)
	}
	if contains(body, "0001-01-01") {
		t.Fatalf("zero CreatedAt must be omitted, not serialized: %s", body)
	}
}
```

Add `"time"` and `"github.com/ilijad1/simple-agents/internal/db"` to that file's imports if absent.

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./web/ -run TestAPIDesignStateHistoryCarriesTimestamps -count=1`
Expected: FAIL — `stamped history turn lost its created_at`.

- [ ] **Step 3: Add the shared DTO in `web/handlers_agents.go`**

Above `handleResumeDraft`:

```go
// designHistEntry is the wire shape of one design-conversation turn. CreatedAt is
// a preformatted STRING, not a time.Time: `omitempty` does nothing for a struct,
// so a time.Time field would emit "0001-01-01T00:00:00Z" for drafts written
// before turns were timestamped, and the browser would render a bubble stamped
// year 1. RFC3339Nano matches what /api/v1/chats/:id/messages emits for
// created_at, so both chat surfaces feed formatMessageTime identical input.
type designHistEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

// designHistoryDTO maps session history to the wire shape. Shared by the agent
// resume/state handlers and the skill resume handler so the three cannot drift.
func designHistoryDTO(hist []db.ChatMessage) []designHistEntry {
	out := make([]designHistEntry, 0, len(hist))
	for _, m := range hist {
		e := designHistEntry{Role: m.Role, Content: m.Content}
		if !m.CreatedAt.IsZero() {
			e.CreatedAt = m.CreatedAt.Format(time.RFC3339Nano)
		}
		out = append(out, e)
	}
	return out
}
```

Then in `handleResumeDraft` and `handleDesignState`, delete the local `type histEntry struct {…}` declaration and the `hist := make(...)`/`for ... append(...)` loop, replacing each with:

```go
	hist := designHistoryDTO(snap.History)
```

- [ ] **Step 4: Stamp the appends in `internal/agentdesigner/flow.go`**

Every `db.ChatMessage{…}` literal in that file gains `CreatedAt: time.Now().UTC()`:

```go
	sess.History = append(sess.History,
		db.ChatMessage{Role: "user", Content: userMessage, CreatedAt: time.Now().UTC()},
		db.ChatMessage{Role: "assistant", Content: result.Text, CreatedAt: time.Now().UTC()},
	)
```

and the same addition on the three single-entry appends (`outcome.message`, `note`, `input`). Verify none is missed:

```bash
grep -n "db.ChatMessage{" internal/agentdesigner/flow.go | grep -v CreatedAt
```

Expected: no output.

- [ ] **Step 5: Run the tests**

Run: `go test ./web/ ./internal/agentdesigner/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentdesigner/flow.go web/handlers_agents.go web/api_agents_test.go
git commit -m "feat(web): timestamp agent design turns and expose created_at"
```

---

### Task 2: Stamp and expose designer history timestamps (skill designer)

**Files:**
- Modify: `internal/skilldesigner/flow.go` (5 `db.ChatMessage{…}` literals: ~283, ~408, ~416, ~417, ~628)
- Modify: `web/handlers_skill_design.go` (the inline `histEntry` struct at ~113)
- Test: `web/api_skills_test.go`

**Interfaces:**
- Consumes: `designHistoryDTO` / `designHistEntry` from Task 1 (same `web` package).

- [ ] **Step 1: Stamp the appends in `internal/skilldesigner/flow.go`**

Add `CreatedAt: time.Now().UTC()` to all five literals, e.g.:

```go
	sess.History = append(sess.History,
		db.ChatMessage{Role: "user", Content: userMessage, CreatedAt: time.Now().UTC()},
		db.ChatMessage{Role: "assistant", Content: reply, CreatedAt: time.Now().UTC()},
	)
```

Verify: `grep -n "db.ChatMessage{" internal/skilldesigner/flow.go | grep -v CreatedAt` → no output.

- [ ] **Step 2: Swap the skill resume DTO**

In `handleResumeSkillDraft` (`web/handlers_skill_design.go`), delete the local `type histEntry struct {…}` and its mapping loop. The zero-value seed becomes the shared type:

```go
	out := map[string]interface{}{
		"response":          resp,
		"state":             "",
		"history":           []designHistEntry{},
		"skill_id":          "",
		"skill_name":        "",
		"generation_failed": false,
	}
	if sess != nil {
		out["generation_failed"] = sess.GenerationFailed
		out["state"] = sess.State.String()
		out["history"] = designHistoryDTO(sess.History)
		out["skill_name"] = sess.SkillName
	}
```

- [ ] **Step 3: Build and run the tests**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS (this also confirms no other caller referenced the deleted local `histEntry` types).

- [ ] **Step 4: Commit**

```bash
git add internal/skilldesigner/flow.go web/handlers_skill_design.go
git commit -m "feat(web): timestamp skill design turns and reuse the shared history DTO"
```

---

### Task 3: `handleStartEditDesign` returns the session state

**Files:**
- Modify: `web/handlers_agents.go` (`handleDesignChat` ~138 and ~177, `handleStartEditDesign` ~326)
- Test: `web/api_agents_test.go`

**Interfaces:**
- Produces: `web.designTurnResponse(response string, snap agentdesigner.DesignSnapshot) map[string]interface{}` — the body every non-terminal design turn returns.

**Why this is required, not polish:** with the pre-screen gone (Task 6) `DesignerSurface` no longer remounts after the first edit message, so nothing runs the mount-recovery `GET /design/state` that used to supply `state`. Without `state` in this response `fsmState` stays `null`, `showDesigningActions` is false, and the **"🔨 Build it" button never appears** until the user sends a throwaway second message.

- [ ] **Step 1: Write the failing test**

Append to `web/api_agents_test.go`:

```go
// TestDesignTurnResponseCarriesState pins the shape every non-terminal design
// turn returns. handleStartEditDesign shares it with handleDesignChat: with the
// edit pre-screen gone, DesignerSurface no longer remounts after the first edit
// message, so this response is the ONLY thing that can move the stepper into
// "designing" and reveal the Build button.
func TestDesignTurnResponseCarriesState(t *testing.T) {
	snap := agentdesigner.DesignSnapshot{
		Active:           true,
		State:            "designing",
		GenerationFailed: true,
		CanKeepAsIs:      true,
	}
	out := designTurnResponse("here's my read on it", snap)

	if out["response"] != "here's my read on it" {
		t.Fatalf("response = %v", out["response"])
	}
	if out["done"] != false {
		t.Fatalf("done = %v, want false", out["done"])
	}
	if out["state"] != "designing" {
		t.Fatalf("state = %v, want designing", out["state"])
	}
	if out["generation_failed"] != true || out["can_keep_as_is"] != true {
		t.Fatalf("failure flags dropped: %#v", out)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./web/ -run TestDesignTurnResponseCarriesState -count=1`
Expected: FAIL to compile — `undefined: designTurnResponse`.

- [ ] **Step 3: Add the helper and use it at all three sites**

In `web/handlers_agents.go`:

```go
// designTurnResponse is the body every non-terminal design turn returns. The
// create chat and the first turn of an EDIT share it so they cannot drift: the
// edit page mounts DesignerSurface directly (no pre-screen remount), so this
// response is the only thing that tells the browser which FSM state it is in.
func designTurnResponse(response string, snap agentdesigner.DesignSnapshot) map[string]interface{} {
	return map[string]interface{}{
		"response":          response,
		"done":              false,
		"state":             snap.State,
		"generation_failed": snap.GenerationFailed,
		"can_keep_as_is":    snap.CanKeepAsIs,
	}
}
```

Replace both non-terminal `c.JSON(http.StatusOK, map[string]interface{}{…})` literals in `handleDesignChat` with `c.JSON(http.StatusOK, designTurnResponse(response, s.designFlow.Snapshot(u.ID)))` (keeping the existing `snap := …` local where one already exists), and replace `handleStartEditDesign`'s final return with:

```go
	return c.JSON(http.StatusOK, designTurnResponse(response, s.designFlow.Snapshot(u.ID)))
```

- [ ] **Step 4: Run the tests**

Run: `go test ./web/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/handlers_agents.go web/api_agents_test.go
git commit -m "fix(web): return the FSM state from the edit-start design turn"
```

---

### Task 4: `DesignerSurface` renders bubble timestamps

**Files:**
- Modify: `web/ui/src/components/designer/DesignerSurface.tsx`
- Test: `web/ui/src/components/designer/designer.test.tsx`

**Interfaces:**
- Consumes: `created_at` from Task 1/2's DTOs.
- Produces: `HistEntry = { role: Role; content: string; created_at?: string }`.

- [ ] **Step 1: Write the failing test**

Append to `designer.test.tsx`:

```tsx
test("design turns render a timestamp footer like every other chat", async () => {
  mockFetch({
    "/x/design": () => jsonResponse({ response: "What should it do?", done: false, state: "designing" }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("Build me a thing");
  await screen.findByText("What should it do?");

  // Both the optimistic user turn and the assistant reply are stamped locally —
  // the design POST returns prose only, never a time.
  await waitFor(() => expect(screen.getAllByTestId("message-time")).toHaveLength(2));
});

test("resumed history keeps the server's timestamps and stamps the resume message", async () => {
  mockFetch({
    "/x/state": () => jsonResponse({ active: false }),
    "/x/resume": () =>
      jsonResponse({
        response: "Where were we — you wanted a daily digest.",
        state: "designing",
        history: [
          { role: "user", content: "a daily digest", created_at: "2026-07-28T09:30:00Z" },
          { role: "assistant", content: "how often?", created_at: "2026-07-28T09:30:05Z" },
        ],
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      draft={{ name: "Digest" }}
      autoResume
      onDone={vi.fn()}
    />,
  );

  await screen.findByText("Where were we — you wanted a daily digest.");
  // Two restored turns + the freshly generated resume message, which is NOT part
  // of `history` and so has to be stamped client-side.
  await waitFor(() => expect(screen.getAllByTestId("message-time")).toHaveLength(3));
});
```

- [ ] **Step 2: Run and confirm both fail**

Run: `cd web/ui && npx vitest run src/components/designer/designer.test.tsx`
Expected: FAIL — `getAllByTestId("message-time")` finds nothing (the bubbles render no time).

- [ ] **Step 3: Thread `created_at` through the surface**

In `DesignerSurface.tsx`:

```tsx
type HistEntry = { role: Role; content: string; created_at?: string };

// Turns appended locally (the optimistic user bubble, each assistant reply from a
// design POST, and the resume message) carry no server time — the design
// endpoints return prose, not a transcript row. Stamping here is accurate to
// within the round-trip and keeps every bubble's footer consistent with the
// chats page, where the time comes from the DB.
function nowStamp(): string {
  return new Date().toISOString();
}
```

Then add `created_at: nowStamp()` to every local `setMessages` append — the optimistic user turn and the `res.response` appends in `handleSend` (both the `res.done` branch and the normal one) — and to the trailing resume message:

```tsx
      setMessages([...hist, { role: "assistant", content: res.response, created_at: nowStamp() }]);
```

Finally pass it to the bubble:

```tsx
            <ChatMessageBubble key={i} role={m.role} content={m.content} createdAt={m.created_at} />
```

- [ ] **Step 4: Run the tests**

Run: `cd web/ui && npx vitest run src/components/designer/designer.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/components/designer/DesignerSurface.tsx web/ui/src/components/designer/designer.test.tsx
git commit -m "fix(web/ui): show timestamps on designer chat bubbles"
```

---

### Task 5: `DesignerSurface` gains `startEndpoint`, `acceptRecoveredSession`, and a cancel guard

**Files:**
- Modify: `web/ui/src/components/designer/DesignerSurface.tsx`
- Test: `web/ui/src/components/designer/designer.test.tsx`

**Interfaces:**
- Produces (consumed by Task 6):
  - `startEndpoint?: string`
  - `acceptRecoveredSession?: (info: { isEdit: boolean; agentId: string }) => boolean`

- [ ] **Step 1: Write the failing tests**

Append to `designer.test.tsx`:

```tsx
test("startEndpoint takes the first message; later messages go to the design endpoint", async () => {
  const calls = mockFetch({
    "/x/start": () => jsonResponse({ response: "Here's what I found.", done: false, state: "designing" }),
    "/x/design": () => jsonResponse({ response: "Updated.", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      startEndpoint="/x/start"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("make it hourly");
  await screen.findByText("Here's what I found.");
  await sendViaComposer("actually daily");
  await screen.findByText("Updated.");

  const posts = calls.filter((c) => c.method === "POST").map((c) => c.url);
  expect(posts).toEqual(["/x/start", "/x/design"]);
});

test("a recovered session the caller rejects is not adopted and its build is not streamed", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({
        active: true,
        generating: true,
        state: "designing",
        is_edit: false,
        agent_id: "someone-else",
        history: [{ role: "user", content: "an unrelated conversation" }],
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      acceptRecoveredSession={(s) => s.isEdit && s.agentId === "a1"}
      onDone={vi.fn()}
    />,
  );

  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  expect(screen.queryByText("an unrelated conversation")).not.toBeInTheDocument();
  expect(FakeEventSource.instances).toHaveLength(0);
});

test("cancelling an untouched surface navigates without cancelling anyone's session", async () => {
  const calls = mockFetch({ "/x/state": () => jsonResponse({ active: false }) });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

  expect(calls.some((c) => c.url === "/x/cancel")).toBe(false);
});
```

- [ ] **Step 2: Run and confirm they fail**

Run: `cd web/ui && npx vitest run src/components/designer/designer.test.tsx`
Expected: FAIL — the first POST goes to `/x/design`, the rejected session's message renders, and `/x/cancel` is posted.

- [ ] **Step 3: Implement the three changes**

Add to `DesignerSurfaceProps`:

```tsx
  // POST target for the VERY FIRST message of a genuinely fresh session, instead
  // of endpoints.design — the agent editor's /agents/:id/edit/start, which
  // creates the session server-side. Every later message goes to
  // endpoints.design, because once created an edit session is indistinguishable
  // from a create session. Body is {message} only: startPayload is the OTHER way
  // to open a session and is deliberately not merged here.
  startEndpoint?: string;
  // Vetoes a recovered session. The design session is a per-workspace SINGLETON,
  // so mount recovery would otherwise adopt whatever is live — showing an
  // unrelated create conversation on an agent's edit page and offering to save
  // the wrong entity. Returning false makes the surface treat the session as
  // inactive. Omitted (every caller but AgentEditPage) accepts everything, which
  // is the pre-existing behavior.
  acceptRecoveredSession?: (info: { isEdit: boolean; agentId: string }) => boolean;
```

Destructure both in the component signature.

Add the touched ref beside the other refs:

```tsx
  // True once this surface owns a session: recovery ACCEPTED a live one, a
  // resume succeeded, or the user sent a message. handleCancel POSTs
  // endpoints.cancel, and the design session is a per-workspace singleton — so
  // without this, opening an agent's edit page while an unrelated build is
  // running and hitting Cancel would kill that build. An untouched surface has
  // nothing to cancel anyway.
  const sessionTouchedRef = useRef(false);
```

In `refetchState`, gate the whole active branch:

```tsx
      const snap = await api.get<StateSnapshot>(endpoints.state);
      if (doneRef.current || unmountedRef.current) return;
      const accepted =
        snap.active &&
        (!acceptRecoveredSession ||
          acceptRecoveredSession({ isEdit: !!snap.is_edit, agentId: snap.agent_id ?? "" }));
      if (accepted) {
        sessionTouchedRef.current = true;
        setResumeBanner(null);
        // …unchanged body, including `if (snap.generating) ensureSSE("recovery", …)`
      } else {
        setGenerating(false);
        // …unchanged draft/banner body
      }
```

The `ensureSSE("recovery", …)` call stays inside the accepted branch, so a rejected mid-build session never streams another entity's log here.

In `handleResume`, after the POST resolves, `sessionTouchedRef.current = true;`.

In `handleCancel`:

```tsx
  async function handleCancel() {
    // Nothing was ever started here — see sessionTouchedRef.
    if (sessionTouchedRef.current) {
      try {
        await api.post(endpoints.cancel);
      } catch {
        // Ignore — we're navigating away regardless.
      }
    }
    navigate(cancelTo);
  }
```

In `handleSend`, mark it touched and pick the endpoint:

```tsx
      const isFirstMessage = messages.length === 0 && !resumeBanner;
      const body: Record<string, unknown> = { message: text };
      // startEndpoint and startPayload are alternative ways to open a session;
      // no caller needs both, so the payload is never merged into a start POST.
      const url = isFirstMessage && startEndpoint ? startEndpoint : endpoints.design;
      if (isFirstMessage && startPayload && !startEndpoint) Object.assign(body, startPayload);

      const res = await api.post<DesignResponse>(url, body);
```

with `sessionTouchedRef.current = true;` set alongside the optimistic `setMessages` at the top of `handleSend`.

- [ ] **Step 4: Run the whole designer suite**

Run: `cd web/ui && npx vitest run src/components/designer/designer.test.tsx`
Expected: PASS, including the pre-existing zero-extra-`/state`-calls assertions.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/components/designer/DesignerSurface.tsx web/ui/src/components/designer/designer.test.tsx
git commit -m "feat(web/ui): let DesignerSurface open a session via a start endpoint"
```

---

### Task 6: `AgentEditPage` mounts the shared surface

**Files:**
- Modify: `web/ui/src/pages/agents/AgentEditPage.tsx` (delete the pre-screen branch entirely)
- Test: `web/ui/src/pages/agents/edit.test.tsx` (create)

**Interfaces:**
- Consumes: Task 5's `startEndpoint` + `acceptRecoveredSession`; Task 3's `state` in the edit-start response.

- [ ] **Step 1: Write the failing test**

Create `web/ui/src/pages/agents/edit.test.tsx`:

```tsx
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import AgentEditPage from "./AgentEditPage";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener() {}
  close() {}
}

let posts: string[];

function mockFetch() {
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "POST") posts.push(url);
      if (url === "/api/v1/agents" && method === "GET") return Promise.resolve(jsonResponse({ agents: [], draft: null }));
      if (url === "/api/v1/agents/a1" && method === "GET") {
        return Promise.resolve(jsonResponse({ agent: { id: "a1", name: "Inbox Triager" } }));
      }
      if (url === "/api/v1/agents/design/state") return Promise.resolve(jsonResponse({ active: false }));
      if (url === "/api/v1/agents/a1/edit/start") {
        return Promise.resolve(jsonResponse({ response: "The schedule row says hourly.", done: false, state: "designing" }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/agents/a1/edit"]}>
        <Routes>
          <Route path="/agents/:id/edit" element={<AgentEditPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  mockFetch();
});

afterEach(() => vi.unstubAllGlobals());

// The bug: the edit page used to open in its own full-width chrome and swap to
// the 10%-gutter DesignerSurface only after the first reply. One surface now
// owns it from the first paint — the stepper is the tell.
test("the edit chat opens in the designer chrome, with no pre-screen", async () => {
  wrap();
  expect(await screen.findByText("Diagnose")).toBeInTheDocument();
  expect(screen.queryByPlaceholderText("Describe the change…")).not.toBeInTheDocument();
});

// The first message used to vanish into a disabled composer for a whole coder
// round-trip. It must appear as a bubble immediately.
test("the first message is echoed as a bubble and routed to edit/start", async () => {
  wrap();
  await screen.findByText("Diagnose");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "run it once a day");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByText("run it once a day")).toBeInTheDocument();
  expect(await screen.findByText("The schedule row says hourly.")).toBeInTheDocument();
  await waitFor(() => expect(posts).toContain("/api/v1/agents/a1/edit/start"));
  expect(posts).not.toContain("/api/v1/agents/design");
});

// The state the server now returns for the first edit turn is what reveals this.
test("the Build button appears after the first reply", async () => {
  wrap();
  await screen.findByText("Diagnose");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "run it once a day");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByRole("button", { name: "🔨 Build it" })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and confirm they fail**

Run: `cd web/ui && npx vitest run src/pages/agents/edit.test.tsx`
Expected: FAIL — "Diagnose" is absent because the pre-screen renders instead.

- [ ] **Step 3: Rewrite `AgentEditPage.tsx`**

```tsx
import { useNavigate, useParams } from "react-router";
import {
  DesignerSurface,
  type DesignerEndpoints,
  type DesignerLabels,
} from "@/components/designer/DesignerSurface";
import { DesignerIntro } from "@/components/designer/DesignerIntro";
import { useAgents, useAgentDetail } from "@/lib/agents";

const ENDPOINTS: DesignerEndpoints = {
  design: "/api/v1/agents/design",
  cancel: "/api/v1/agents/design/cancel",
  resume: "/api/v1/agents/design/resume",
  dismiss: "/api/v1/agents/design/dismiss",
  progress: "/api/v1/agents/design/progress",
  state: "/api/v1/agents/design/state",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Diagnose", "Build", "Review"],
  buildButton: "🔨 Build it",
  saveButton: "✅ Save agent",
  entityName: "agent",
};

const INTRO_EXAMPLES = [
  "It runs too often — once a day in the morning is enough.",
  "It keeps telling me about things I've already seen.",
  "Send it to me only when something actually changed.",
];

// Conversational agent editing — the SAME surface as agent creation, so the chat
// never changes shape mid-conversation. The only difference is the first
// message: it POSTs to /agents/:id/edit/start, which creates the design session
// server-side. Once that returns, the session is indistinguishable from a create
// session and every later message goes through the normal design endpoint.
//
// This page used to render its own full-width pre-screen until that first reply
// landed, then swap in DesignerSurface — a visible layout jump, and a first turn
// that produced no bubble at all while the coder ran. DesignerSurface's
// startEndpoint prop removed the reason for a second surface to exist.
export default function AgentEditPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: detail } = useAgentDetail(id);
  const { data } = useAgents();
  const draft = data?.draft ?? null;
  // Only a draft for THIS agent's edit can be resumed here — a create draft, or
  // an edit draft for another agent, belongs to a different page.
  const matchingDraft = draft && draft.is_edit && draft.agent_id === id ? draft : null;
  const agentName = detail?.agent.name ?? "this agent";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DesignerSurface
        endpoints={ENDPOINTS}
        labels={LABELS}
        startEndpoint={`/api/v1/agents/${id}/edit/start`}
        // The design session is a per-workspace singleton; without this the
        // surface would adopt an unrelated live create session and offer to save
        // the wrong agent (the deleted pre-screen's job).
        acceptRecoveredSession={(s) => s.isEdit && s.agentId === id}
        intro={
          <DesignerIntro
            title={`What would you like to change about ${agentName}?`}
            blurb="Describe what's wrong or what you'd like different. I'll work out why it's happening, tell you what I plan to change, and only rebuild once you're happy."
            examples={INTRO_EXAMPLES}
          />
        }
        draft={matchingDraft ? { name: matchingDraft.agent_name } : null}
        cancelTo={`/agents/${id}`}
        onDone={() => navigate(`/agents/${id}`)}
      />
    </div>
  );
}
```

- [ ] **Step 4: Run the new tests**

Run: `cd web/ui && npx vitest run src/pages/agents/edit.test.tsx`
Expected: PASS.

- [ ] **Step 5: Run everything**

Run: `cd web/ui && npx vitest run` then, from the repo root, `go test ./... -count=1 -timeout 120s` and `npx tsc --noEmit -p web/ui/tsconfig.json` (or `cd web/ui && npm run build`).
Expected: all PASS; no unused-import or unused-variable TS errors from the deleted pre-screen (`useState`, `Link`, `Composer`, `api`, `ApiError`, `AlertTriangle` are all gone).

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/agents/AgentEditPage.tsx web/ui/src/pages/agents/edit.test.tsx
git commit -m "fix(web/ui): open agent editing in the shared designer chat"
```

---

### Task 7: Update CLAUDE.md and open the PR

**Files:**
- Modify: `CLAUDE.md` (the "Chat gutters (the 10% column)" and "Conversational agent editing" sections)

- [ ] **Step 1: Record the architecture change**

In the **Conversational agent editing** section, add after the first paragraph:

> The edit page mounts the SAME `DesignerSurface` as creation from the first paint — there is no pre-screen. Its first message POSTs `startEndpoint` (`/api/v1/agents/:id/edit/start`), which creates the session server-side and returns the full design-turn body (`designTurnResponse`, shared with `handleDesignChat`) so the stepper and Build button are correct without a remount. `acceptRecoveredSession` vetoes an unrelated recovered session, since the design session is a per-workspace singleton.

In the **Chat message chrome** section, add:

> Designer turns carry timestamps too: both flows stamp `db.ChatMessage.CreatedAt` on append, and `web.designHistoryDTO` emits it as a `created_at` RFC3339Nano **string**, omitted when zero (a `time.Time` DTO field would defeat `omitempty` and stamp legacy drafts year 1). Turns appended client-side are stamped in the browser.

- [ ] **Step 2: Full verification**

Run: `go test ./... -count=1 -timeout 120s` and `cd web/ui && npx vitest run && npm run build`
Expected: all PASS.

- [ ] **Step 3: Commit, push, open a draft PR**

```bash
git add CLAUDE.md
git commit -m "docs: record the designer chat parity changes"
git push -u origin HEAD
gh pr create --draft --title "fix(web/ui): one chat surface for agent editing + designer bubble timestamps" --body "..."
```

## Self-Review

**Spec coverage.** Part 1: pre-screen deletion + `startEndpoint` (Task 6/5), `acceptRecoveredSession` incl. the SSE gate (Task 5), cancel guard (Task 5), `handleStartEditDesign` state (Task 3), intro card carrying the agent name (Task 6). Part 2: stamping (Tasks 1–2), the three DTO sites (Tasks 1–2), client threading incl. the resume message (Task 4). Out-of-scope name gates: untouched. Testing section: covered by Tasks 1, 3, 4, 5, 6 plus Task 7's full run.

**Placeholders.** None — every code step carries real code. The `gh pr create --body` is written at execution time from the diff, which is the one place a literal body cannot be pre-written.

**Type consistency.** `designHistEntry`/`designHistoryDTO` defined in Task 1, consumed in Task 2. `designTurnResponse(response, snap)` defined and used in Task 3. `startEndpoint`/`acceptRecoveredSession({isEdit, agentId})` defined in Task 5, consumed in Task 6 with the same names and shape. `HistEntry.created_at` (Task 4) matches the `created_at` JSON tag (Task 1).
