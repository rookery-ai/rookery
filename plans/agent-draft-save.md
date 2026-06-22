# Agent Creation Draft Save Mechanism

## Context

Agent design sessions live entirely in-memory (`Flow.sessions map[string]*DesignSession`). When a user reloads the page, closes the browser, the server restarts, or the session hits an intermittent error (ErrUsageLimit, timeout), the whole session — conversation history, agent name, and any generated content — vanishes.

The key resource constraint: the coder subprocess (generation) takes minutes and consumes quota. We must **never re-run generation automatically on resume**. The only generated artifact worth preserving is a *successful* generation — which is already captured as `PendingAgentMD`/`PendingTools` in `StateVerifying`. A failed or limit-hit generation has no result, so a resumed session in `StateDesigning` simply retries when the user next says "approve" — this is correct, not wasteful.

**What the draft saves:** conversation history + FSM state + (if `StateVerifying`) generated content.  
**When a draft is deleted:** only on `finalizeAgent()` success. Explicit Cancel keeps the draft.  
**Draft TTL:** 7 days.

---

## Implementation

### 1. Migration: `migrations/006_agent_drafts.up.sql` (new file)

```sql
CREATE TABLE IF NOT EXISTS agent_drafts (
    user_id           TEXT PRIMARY KEY,
    agent_id          TEXT,
    agent_name        TEXT NOT NULL,
    is_edit           INTEGER NOT NULL DEFAULT 0,
    state             TEXT NOT NULL,              -- "designing" or "verifying"
    history_json      TEXT NOT NULL DEFAULT '[]',
    pending_agent_md  TEXT NOT NULL DEFAULT '',
    pending_tools_json TEXT NOT NULL DEFAULT '{}',
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at        DATETIME NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

`user_id` is the PK — one draft per user, overwritten on each save. No index needed beyond PK.

---

### 2. DB model: `internal/db/models.go`

Add at the bottom:

```go
type AgentDraft struct {
    UserID           string
    AgentID          string
    AgentName        string
    IsEdit           bool
    State            string // "designing" or "verifying"
    HistoryJSON      string
    PendingAgentMD   string
    PendingToolsJSON string
    UpdatedAt        time.Time
    ExpiresAt        time.Time
}
```

---

### 3. DB repository: `internal/db/repositories.go`

Add three functions at the bottom of the file:

```go
// UpsertAgentDraft saves or overwrites the user's draft.
func (d *DB) UpsertAgentDraft(draft *AgentDraft) error

// GetAgentDraft returns the user's draft, or ErrNotFound if absent or expired.
func (d *DB) GetAgentDraft(userID string) (*AgentDraft, error)
// Implementation must check: WHERE user_id=? AND expires_at > CURRENT_TIMESTAMP

// DeleteAgentDraft removes the user's draft.
func (d *DB) DeleteAgentDraft(userID string) error
```

`UpsertAgentDraft` uses `INSERT OR REPLACE INTO agent_drafts (...)` and sets `updated_at = CURRENT_TIMESTAMP`.

`ListExpiredAgentDrafts` (needed for GC goroutine, section 9):
```go
func (d *DB) ListExpiredAgentDrafts() ([]*AgentDraft, error)
// SELECT * FROM agent_drafts WHERE expires_at <= CURRENT_TIMESTAMP
```

---

### 4. Flow changes: `internal/agentdesigner/flow.go`

#### 4a. Extend `dbDesignStore` interface (line ~86)

Add to the existing interface:
```go
UpsertAgentDraft(d *db.AgentDraft) error
GetAgentDraft(userID string) (*db.AgentDraft, error)
DeleteAgentDraft(userID string) error
```

#### 4b. New FSM state

Add before `StateDescribing` in the const block:
```go
StateAwaitingResume DesignState = "awaiting_resume"
```

Add `pendingName string` to `DesignSession` (stores the agent name the user originally typed, used when they pick "new" instead of "resume").

#### 4c. Private helpers: `saveDraft` and `deleteDraft`

```go
// saveDraft serializes the current session and upserts it. Called while the Flow
// mutex is held; SQLite is fast enough that holding the lock for a single upsert
// is acceptable (consistent with how other db calls are made in runGeneration).
func (f *Flow) saveDraft(sess *DesignSession) {
    if f.db == nil { return }
    histJSON, _ := json.Marshal(sess.History)
    toolsJSON, _ := json.Marshal(sess.PendingTools)
    state := "designing"
    if sess.State == StateVerifying { state = "verifying" }
    _ = f.db.UpsertAgentDraft(&db.AgentDraft{
        UserID:           sess.UserID,
        AgentID:          sess.AgentID,
        AgentName:        sess.AgentName,
        IsEdit:           sess.IsEdit,
        State:            state,
        HistoryJSON:      string(histJSON),
        PendingAgentMD:   sess.PendingAgentMD,
        PendingToolsJSON: string(toolsJSON),
        ExpiresAt:        time.Now().Add(7 * 24 * time.Hour),
    })
}

func (f *Flow) deleteDraft(userID string) {
    if f.db == nil { return }
    _ = f.db.DeleteAgentDraft(userID)
}
```

#### 4d. Call sites for `saveDraft`

- **`callCoder()`** — after the assistant response is appended to `sess.History` (line ~467), call `f.saveDraft(sess)`. This covers every conversation turn including the very first turn from `StartDesign(firstMessage)`.
- **`runGeneration()`** — after `sess.PendingAgentMD` and `sess.PendingTools` are set and state advances to `StateVerifying` (line ~677), call `f.saveDraft(sess)`.

#### 4e. Call site for `deleteDraft`

- **`finalizeAgent()`** — after `saveAndFinish()` (line ~839) or `updateAndFinish()` (line ~873) returns `nil`, call `f.deleteDraft(sess.UserID)`.

#### 4f. Public methods

```go
// HasDraft returns the draft if one exists and is not expired; nil otherwise.
func (f *Flow) HasDraft(userID string) *db.AgentDraft

// DismissDraft deletes the draft. For create-mode drafts in "verifying" state
// it also calls os.RemoveAll on the agent's pre-approved directory to avoid
// orphaned files accumulating on disk.
func (f *Flow) DismissDraft(userID string) error

// ResumeDraft reconstructs a DesignSession from the saved draft.
// It re-loads Skills, ConnectedPlatforms, UserProfile, and UserMemory the same
// way Start()/StartDesign() do — these are not stored in the draft because they
// are cheap to reload and may have changed.
// For is_edit drafts it re-runs loadAgentForEdit(userID, agentID); if the agent
// no longer exists it calls DismissDraft and returns an error so callers can
// tell the user.
// Returns the message to show the user to continue the conversation.
func (f *Flow) ResumeDraft(ctx context.Context, userID string) (string, error)

// OfferDraftResume creates a minimal session in StateAwaitingResume and returns
// the prompt to send the user. pendingAgentName is stored in the session for the
// "new" branch of stepAwaitingResume.
func (f *Flow) OfferDraftResume(userID, pendingAgentName string, draft *db.AgentDraft) (string, error)
```

**`ResumeDraft` message format:**

- `state="designing"`: "Resuming your draft for **\<name\>**. Here's where we left off:\n\n\<last assistant message from history\>\n\nWhat would you like to add or change? Say 'approve' when ready to generate."
- `state="verifying"`: "Resuming your draft for **\<name\>**. The coder has already built this version:\n\n```\n\<PendingAgentMD first 600 chars…\>\n```\n\nType `approve` to save it, or describe any changes you'd like."

**`DismissDraft` cleanup for create-mode verifying drafts:**

```go
if !draft.IsEdit && draft.State == "verifying" && draft.AgentID != "" {
    agentDir := agentDirPath(f.dataDir, draft.UserID, draft.AgentID)
    _ = os.RemoveAll(agentDir)
}
```

#### 4g. `stepAwaitingResume` — new FSM handler

```go
func (f *Flow) stepAwaitingResume(sess *DesignSession, msg string) (string, bool, string, error) {
    lower := strings.TrimSpace(strings.ToLower(msg))
    if lower == "resume" {
        f.mu.Unlock()
        resp, err := f.ResumeDraft(context.Background(), sess.UserID)
        f.mu.Lock()
        return resp, false, "", err
    }
    // "new" or anything else → dismiss draft, transition to StateDescribing
    _ = f.DismissDraft(sess.UserID)
    sess.State = StateDescribing
    sess.AgentName = sess.pendingName
    return fmt.Sprintf("Starting fresh. What should the '%s' agent do?", sess.pendingName), false, "", nil
}
```

Add `case StateAwaitingResume: return f.stepAwaitingResume(sess, msg)` to the `Step()` switch.

---

### 5. Web handler: `web/handlers_agents.go`

**`showNewAgent()` (GET /dashboard/agents/new):** add draft check and pass to template:
```go
draft := s.designFlow.HasDraft(u.ID)
// pass to template data as: "Draft": draft  (nil if none)
```

**New handler `handleResumeDraft` (POST /dashboard/agents/design/resume):**
```go
func (s *Server) handleResumeDraft(c echo.Context) error {
    u := currentUser(c)
    resp, err := s.designFlow.ResumeDraft(c.Request().Context(), u.ID)
    if err != nil {
        return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
    }
    sess := s.designFlow.GetSession(u.ID) // expose existing session getter or inline
    return c.JSON(http.StatusOK, map[string]any{
        "response": resp,
        "state":    sess.State,
        "history":  sess.History, // used by frontend to replay conversation
        "agent_id": sess.AgentID,
    })
}
```

`GetSession(userID string) *DesignSession` needs to be added to `Flow` (or inline the fields directly in the handler response).

**New handler `handleDismissDraft` (POST /dashboard/agents/design/dismiss):**
```go
func (s *Server) handleDismissDraft(c echo.Context) error {
    u := currentUser(c)
    _ = s.designFlow.DismissDraft(u.ID)
    return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
```

---

### 6. Route registration: `web/server.go` (line ~233, near `/design/cancel`)

```go
d.POST("/design/resume",  s.handleResumeDraft)
d.POST("/design/dismiss", s.handleDismissDraft)
```

---

### 7. Template: `web/templates/dashboard/agent_new.html`

Add a dismissable banner at the top of phase-start (before the name/description form), shown only when `{{ .Draft }}` is non-nil:

```html
{{ if .Draft }}
<div id="draft-banner" class="alert alert-info shadow-sm mb-4 flex items-center justify-between">
  <span>You have an unfinished draft: <strong>{{ .Draft.AgentName }}</strong></span>
  <div class="flex gap-2">
    <button id="btn-resume-draft" class="btn btn-sm btn-primary">Resume</button>
    <button id="btn-dismiss-draft" class="btn btn-sm btn-ghost">Start fresh</button>
  </div>
</div>
{{ end }}
```

JS additions (inside the existing `<script>` block):

```javascript
// Resume: fetch history + response, replay into chat, enter phase-chat
document.getElementById('btn-resume-draft')?.addEventListener('click', async () => {
  const res = await fetch('/dashboard/agents/design/resume', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' }
  });
  const data = await res.json();
  if (data.error) { /* show error */ return; }
  // replay full conversation history into the chat DOM
  data.history.forEach(m => appendMessage(m.role === 'assistant' ? 'ai' : 'user', m.content));
  agentId = data.agent_id;
  document.getElementById('phase-start').classList.add('hidden');
  document.getElementById('phase-chat').classList.remove('hidden');
  // the resumption message (already shows pending content if verifying) is the last history entry
});

// Dismiss: clear draft, hide banner, user continues with normal form
document.getElementById('btn-dismiss-draft')?.addEventListener('click', async () => {
  await fetch('/dashboard/agents/design/dismiss', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' }
  });
  document.getElementById('draft-banner').remove();
});
```

No new approve/request-changes buttons — the existing freeform "approve" / "go ahead" etc. text matching continues to work unchanged after resume.

**Platform parity:** Telegram gets the same draft-resume offer via `StateAwaitingResume` (section 4g). Both platforms can resume and dismiss a draft.

---

### 8. Router: `internal/gateway/router.go`

In the `/agent create <name>` handling path, before calling `flow.Start(userID, name)`:

```go
if draft := s.designFlow.HasDraft(userID); draft != nil {
    msg, err := s.designFlow.OfferDraftResume(userID, agentName, draft)
    if err != nil {
        return "⚠️ " + err.Error()
    }
    return msg
}
// No draft → proceed normally
return s.designFlow.Start(userID, agentName)
```

Subsequent messages route through `flow.Step()` → `stepAwaitingResume()` automatically.

---

### 9. Periodic GC for expired drafts + orphan create-dirs

In `cmd/simple-agents/main.go`, alongside the scheduler and reminder goroutines, add:

```go
go func() {
    for range time.Tick(24 * time.Hour) {
        expired, _ := database.ListExpiredAgentDrafts()
        for _, d := range expired {
            if !d.IsEdit && d.State == "verifying" && d.AgentID != "" {
                _ = os.RemoveAll(agentDirPath(cfg.DataDir, d.UserID, d.AgentID))
            }
            _ = database.DeleteAgentDraft(d.UserID)
        }
    }
}()
```

No schema change needed — uses the existing `expires_at` column.

---

## Critical correctness properties

| Property | How ensured |
|---|---|
| No coder re-run on resume | Session restored from draft; coder only runs when user says "approve" again |
| First turn always saved | `StartDesign(firstMessage)` → `callCoder()` → `saveDraft()` |
| Edit resume re-hydrates derived context | `ResumeDraft` reloads Skills, ConnectedPlatforms, UserProfile, UserMemory via the same loaders as `Start()` |
| Edit resume handles deleted agent | `ResumeDraft` calls `loadAgentForEdit()`; if agent gone → dismiss draft + return error message |
| No orphan create-dirs | `DismissDraft` removes agentDir if create+verifying; nightly GC handles expiry path |
| No duplicate schedule rows on edit | `reconcileScheduleOnSave` already handles this; edit resume re-runs `loadAgentForEdit` which reconciles schedule |
| Cancel keeps draft | `Cancel()` only kills subprocess + deletes session from map; does NOT call `deleteDraft()` |
| Platform parity | Web: resume/dismiss banner; Telegram: StateAwaitingResume "resume"/"new" |

---

## Verification

1. **Build:** `go build -o bin/simple-agents ./cmd/simple-agents` — must pass after migration 006 added and all interface implementations updated.

2. **Unit tests:** `go test ./internal/agentdesigner/... ./internal/db/... -v` — add tests for:
   - `TestDraftSaveAndResume_Designing`: Start session, send one message, simulate session loss, call `ResumeDraft`, assert History restored and context fields (Skills, ConnectedPlatforms) re-populated.
   - `TestDraftSaveAndResume_Verifying`: Drive session to StateVerifying, simulate session loss, call `ResumeDraft`, assert PendingAgentMD present in response.
   - `TestDraftDismissedOnFinalize`: Approve agent → assert draft row gone from DB.
   - `TestDraftCleanupOrphanDir`: `DismissDraft` on create+verifying draft → assert agentDir removed.
   - `TestEditDraftResume_AgentDeleted`: Edit draft with non-existent agent_id → `ResumeDraft` returns error, draft deleted.
   - `TestDraftExpiry`: Insert draft with `expires_at` in the past → `GetAgentDraft` returns ErrNotFound.

3. **Manual web test:**
   - Open `/dashboard/agents/new`, type name + first design message, close tab.
   - Reopen `/dashboard/agents/new` → draft banner appears with agent name.
   - Click "Resume" → conversation replayed, last message visible, can continue.
   - Say "approve" → generation runs, agent created → banner never reappears.
   - Repeat through StateVerifying: drive to "approve" in chat, close tab before typing final "approve", reopen → banner shows, resume shows AGENT.md content, type "approve" → agent finalized.
   - Click "Start fresh" → banner gone, clean form.

4. **Manual Telegram test:**
   - `/agent create testbot` → send one design message → go silent.
   - Later: `/agent create testbot` → bot replies "Found unfinished draft: testbot. Reply 'resume' to continue or 'new' to start fresh."
   - "resume" → last conversation exchange shown, can continue.
   - Complete agent creation → no draft prompt on next `/agent create`.

5. **Expiry GC test:** Manually `UPDATE agent_drafts SET expires_at = datetime('now', '-1 day')` in SQLite, trigger GC goroutine, assert row deleted and (for create+verifying) agentDir removed.
