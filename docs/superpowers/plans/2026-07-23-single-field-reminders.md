# Single-field Reminders Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users create reminders from one natural-language sentence on the web, and surface reminders above the inbox on the home screen.

**Architecture:** Extract the single-string parse strategy (currently inlined in `gateway/router.go`) into a pure `reminder.ParseReminderText`; call it from both the Telegram router and the web `POST /reminders` handler (which gains a `text` field); collapse the web UI's two-field reminder form into one input moved above the inbox, with a loading state and a success toast.

**Tech Stack:** Go (stdlib + echo v4), React + TypeScript (vite, @tanstack/react-query, vitest + testing-library).

## Global Constraints

- Go tests: `go test ./... -count=1 -timeout 120s`. Frontend: `cd web/ui && npm test`.
- Conventional Commits; branch already isolated (`worktree-reminders-single-field`).
- Back-compat: the `{message, when}` two-field POST body must keep working unchanged.
- The LLM path (~4s) is never exercised in a hermetic unit test.
- Filler strip matches a **prefix + trailing space** only, never a substring.

---

### Task 1: `reminder.ParseReminderText` — pure single-string parser

**Files:**
- Create: `internal/reminder/parsetext.go`
- Test: `internal/reminder/parsetext_test.go`

**Interfaces:**
- Consumes: `ParseNaturalTime` (timeparser.go), `TimeParserFunc` (llmparser.go).
- Produces: `func ParseReminderText(ctx context.Context, text string, now time.Time, loc *time.Location, llm TimeParserFunc, workspaceID string) (when time.Time, message string, err error)`. Zero `when` + non-empty `message` + nil err = "no time found". `parseDurationExpr(string) (time.Duration, bool)` is a local helper (the router's `parseDuration` lives in the gateway package and is not importable here — reimplement the minimal `30m`/`2h` form locally).

- [ ] **Step 1: Write the failing tests**

```go
package reminder

import (
	"context"
	"testing"
	"time"
)

func TestParseReminderText_Deterministic(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, loc)
	cases := []struct {
		name     string
		in       string
		wantMsg  string
		wantZero bool // true = no time expected
		checkAt  func(time.Time) bool
	}{
		{"remind-me-to-split", "remind me in 10 minutes to call the doctor", "call the doctor", false,
			func(at time.Time) bool { return at.Equal(now.Add(10 * time.Minute)) }},
		{"filler-reminder-to", "reminder to buy milk in 2 hours", "", true, nil}, // no " to "-splittable time; "to buy milk in 2 hours" has " to " none → LLM path; llm nil → zero
		{"bare-to-split", "in 1 hour to submit invoice", "submit invoice", false,
			func(at time.Time) bool { return at.Equal(now.Add(time.Hour)) }},
		{"legacy-duration", "30m stretch break", "stretch break", false,
			func(at time.Time) bool { return at.Equal(now.Add(30 * time.Minute)) }},
		{"no-time-no-llm", "call the doctor", "call the doctor", true, nil},
		{"meeting-not-stripped", "in 5 minutes to prep the meeting", "prep the meeting", false,
			func(at time.Time) bool { return at.Equal(now.Add(5 * time.Minute)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, msg, err := ParseReminderText(context.Background(), tc.in, now, loc, nil, "w1")
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.wantZero && !at.IsZero() {
				t.Fatalf("expected zero time, got %v", at)
			}
			if !tc.wantZero {
				if at.IsZero() {
					t.Fatalf("expected a time, got zero")
				}
				if tc.checkAt != nil && !tc.checkAt(at) {
					t.Fatalf("time mismatch: got %v", at)
				}
			}
			if tc.wantMsg != "" && msg != tc.wantMsg {
				t.Fatalf("message: got %q want %q", msg, tc.wantMsg)
			}
		})
	}
}

func TestStripReminderFiller(t *testing.T) {
	cases := map[string]string{
		"remind me in 10 minutes": "in 10 minutes",
		"reminder to buy milk":    "buy milk",
		"remind buy milk":         "buy milk",
		"me buy milk":             "buy milk",
		"meeting at 3pm":          "meeting at 3pm", // NOT stripped (substring guard)
		"reminder about taxes":    "about taxes",    // "reminder " prefix stripped, "about" kept
		"buy milk":                "buy milk",
	}
	for in, want := range cases {
		if got := stripReminderFiller(in); got != want {
			t.Errorf("stripReminderFiller(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/reminder/ -run 'ParseReminderText|StripReminderFiller' -v`
Expected: FAIL — `ParseReminderText`/`stripReminderFiller` undefined.

- [ ] **Step 3: Implement `parsetext.go`**

```go
package reminder

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// fillerPrefixes are stripped from the FRONT of a web reminder phrase, longest
// first, matched only as a prefix followed by a space — never a substring, so
// "meeting" and "reminder about X" survive.
var fillerPrefixes = []string{"remind me to ", "remind me ", "reminder to ", "reminder ", "remind me", "remind ", "me "}

func stripReminderFiller(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, p := range fillerPrefixes {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSpace(s[len(p):])
		}
	}
	return s
}

var reDurationExpr = regexp.MustCompile(`^(\d+)\s*(m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks)$`)

// parseDurationExpr parses a compact duration token like "30m", "2h", "1d".
func parseDurationExpr(s string) (time.Duration, bool) {
	m := reDurationExpr.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	switch m[2][:1] {
	case "m":
		return time.Duration(n) * time.Minute, true
	case "h":
		return time.Duration(n) * time.Hour, true
	case "d":
		return time.Duration(n) * 24 * time.Hour, true
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, true
	}
	return 0, false
}

// ParseReminderText extracts (when, message) from one natural-language string.
// Pure: no state, no prompting. Zero `when` + non-empty `message` + nil err
// means "understood the message but found no time" — the caller decides whether
// to prompt or reject. err is non-nil only when the LLM call itself fails.
func ParseReminderText(ctx context.Context, text string, now time.Time, loc *time.Location, llm TimeParserFunc, workspaceID string) (time.Time, string, error) {
	if loc == nil {
		loc = time.UTC
	}
	arg := stripReminderFiller(text)

	var message string
	var remindAt time.Time

	// 1. " to " split → regex / duration on the left part.
	if idx := strings.Index(arg, " to "); idx >= 0 {
		timeExpr := strings.TrimSpace(arg[:idx])
		message = strings.TrimSpace(arg[idx+4:])
		if t, err := ParseNaturalTime(timeExpr, now, loc); err == nil {
			remindAt = t
		} else if d, ok := parseDurationExpr(timeExpr); ok {
			remindAt = now.Add(d)
		}
	}

	// 2. LLM on the whole (stripped) string.
	if remindAt.IsZero() && llm != nil {
		when, extractedMsg, err := llm(ctx, workspaceID, arg, now, loc)
		if err != nil {
			return time.Time{}, "", err
		}
		if !when.IsZero() {
			remindAt = when
			if extractedMsg != "" {
				message = extractedMsg
			}
		} else if extractedMsg != "" {
			message = extractedMsg
		}
	}

	// 3. Legacy first-word-duration fallback: "30m stretch break".
	if remindAt.IsZero() {
		if parts := strings.SplitN(arg, " ", 2); len(parts) == 2 {
			if d, ok := parseDurationExpr(parts[0]); ok {
				remindAt = now.Add(d)
				if message == "" {
					message = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	if message == "" {
		message = arg
	}
	return remindAt, message, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/reminder/ -count=1 -v`
Expected: PASS. (Note: the `filler-reminder-to` case in Task 1 has `wantMsg:""` so message isn't asserted — only that with `llm=nil` the time is zero.)

- [ ] **Step 5: Commit**

```bash
git add internal/reminder/parsetext.go internal/reminder/parsetext_test.go
git commit -m "feat(reminder): pure ParseReminderText single-string parser"
```

---

### Task 2: Web `POST /reminders` accepts a `text` field

**Files:**
- Modify: `web/api_home.go` (`apiCreateReminderRequest`, `apiCreateReminder`)
- Test: `web/api_home_test.go` (add single-field cases)

**Interfaces:**
- Consumes: `reminder.ParseReminderText` (Task 1), existing `buildLLMTimeParser`, `profile.LoadLocation`.
- Produces: same 201 `apiReminder` response; new 400 code `no_time`.

- [ ] **Step 1: Add the failing test** (append to `web/api_home_test.go`)

```go
func TestAPIRemindersSingleField(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Single natural sentence, regex-deterministic (no LLM).
	rec := doJSON(t, s, http.MethodPost, "/api/v1/reminders",
		map[string]string{"text": "in 10 minutes to call the doctor"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("single-field create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Message  string `json:"message"`
		RemindAt string `json:"remind_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Message != "call the doctor" || created.RemindAt == "" {
		t.Fatalf("unexpected: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go test ./web/ -run TestAPIRemindersSingleField -v`
Expected: FAIL (message is the whole sentence or 400 — `text` not yet handled).

- [ ] **Step 3: Implement**

In `web/api_home.go`, add `Text string \`json:"text"\`` to `apiCreateReminderRequest`. At the top of `apiCreateReminder` (after `bindAPI`), branch when `text` is present:

```go
	if txt := strings.TrimSpace(req.Text); txt != "" {
		now := time.Now()
		loc := profile.LoadLocation(s.db, u.ID)
		llmFn := buildLLMTimeParser(s.coderForWorkspace(u.ID))
		remindAt, message, err := reminder.ParseReminderText(c.Request().Context(), txt, now, loc, llmFn, u.ID)
		if err != nil {
			return jsonErr(c, http.StatusBadRequest, "unparseable_time", `couldn't understand that; try "remind me in 10 minutes to call the doctor"`)
		}
		if remindAt.IsZero() {
			return jsonErr(c, http.StatusBadRequest, "no_time", `couldn't find a time in that — try adding one, e.g. "in 10 minutes" or "tomorrow at 3pm"`)
		}
		if strings.TrimSpace(message) == "" {
			return jsonErr(c, http.StatusBadRequest, "missing_field", "what should I remind you about?")
		}
		r := &db.Reminder{ID: uuid.New().String(), WorkspaceID: u.ID, Message: message, RemindAt: remindAt}
		if err := s.db.CreateReminder(r); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
		s.audit.Log(u.ID, "create_reminder", "reminder:"+r.ID, message, c.RealIP())
		return c.JSON(http.StatusCreated, toAPIReminder(r))
	}
	// ...existing two-field path unchanged below...
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./web/ -run 'TestAPIReminders' -count=1 -v`
Expected: PASS (new single-field + existing CRUD + unparseable all green).

- [ ] **Step 5: Commit**

```bash
git add web/api_home.go web/api_home_test.go
git commit -m "feat(web/reminders): accept a single natural-language text field"
```

---

### Task 3: Refactor Telegram `handleRemind` to delegate to `ParseReminderText`

**Files:**
- Modify: `internal/gateway/router.go` (`handleRemind`, lines ~753-812)

**Interfaces:**
- Consumes: `reminder.ParseReminderText` (Task 1).
- Produces: identical `/remind` behavior, incl. the `pendingReminderMsg` follow-up.

- [ ] **Step 1: Replace the inline strategy (steps 1–3 in `handleRemind`) with a delegation**

Keep the `arg == ""` usage help, the `list`/`delete` subcommand switch, and the final `createReminder` call **unchanged**. Replace the block from `arg = strings.TrimPrefix(arg, "me ")` through the legacy fallback (down to just before the `if remindAt.IsZero()` "Couldn't understand" send) with:

```go
	now := time.Now()
	loc := profile.LoadLocation(r.db, msg.WorkspaceID)

	remindAt, message, err := reminder.ParseReminderText(ctx, arg, now, loc, r.timeParserFallback, msg.WorkspaceID)
	if err != nil {
		send("Couldn't understand that time. Try:\n• /remind in 10 minutes to check oven\n• /remind next Tuesday to call doctor\n• /remind 30m old format\n• /remind to write a note _(I'll ask when)_")
		return nil
	}
	if remindAt.IsZero() {
		// Understood the message but found no time — ask for one, remembering the message.
		if message == "" {
			message = arg
		}
		r.mu.Lock()
		r.pendingReminderMsg[msg.WorkspaceID] = message
		r.mu.Unlock()
		send(fmt.Sprintf("⏰ When should I remind you about **%s**?\nReply with a time, e.g. 'in 10 minutes', 'tomorrow at 9am', 'next Friday evening'", message))
		return nil
	}
	if message == "" {
		send("Please include a reminder message. Example: /remind in 10 minutes to check the oven")
		return nil
	}
	return r.createReminder(ctx, msg.WorkspaceID, message, remindAt, send)
```

Delete the now-dead `if remindAt.IsZero()` "Couldn't understand"/"Please include" tail that followed (its logic moved above). `ParseReminderText` already strips a leading `"me "`, so the explicit `TrimPrefix(arg, "me ")` is removed.

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./internal/gateway/`
Expected: clean (watch for now-unused imports/vars — remove `parseDuration` usage here only if it becomes unused package-wide; it is still used elsewhere, leave it).

- [ ] **Step 3: Run the full Go suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/router.go
git commit -m "refactor(gateway): delegate /remind parsing to reminder.ParseReminderText"
```

---

### Task 4: Frontend — one field, moved above inbox, loading + toast

**Files:**
- Modify: `web/ui/src/lib/home.ts` (`useCreateReminder` arg → `{ text }`)
- Modify: `web/ui/src/pages/home/HomePage.tsx` (`AddReminderForm`, section order)
- Test: `web/ui/src/pages/home/home.test.tsx` (rewrite the two reminder-form tests + mock)

**Interfaces:**
- Consumes: `POST /api/v1/reminders` with `{ text }` (Task 2); `useToast` from `@/components/shell/Toast`.
- Produces: single-input reminder form; `RemindersSection` rendered before `InboxSection`.

- [ ] **Step 1: Rewrite the mock + the two reminder tests in `home.test.tsx`**

Change the POST mock to key off `text`:

```ts
      if (url === "/api/v1/reminders" && method === "POST") {
        const body = JSON.parse(String(init?.body));
        const text: string = body.text ?? "";
        if (text.includes("banana")) {
          return Promise.resolve(
            jsonResponse({ error: { code: "unparseable_time", message: "couldn't understand that time" } }, 400),
          );
        }
        const r = { id: "r2", message: "check the oven", remind_at: "2026-07-17T16:00:00Z", sent: false };
        reminders = [...reminders, r];
        return Promise.resolve(jsonResponse(r, 201));
      }
```

Replace the two form tests:

```ts
test("reminders: adding with an unparseable time shows the error inline and keeps the text", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Call the dentist");

  await userEvent.type(screen.getByLabelText(/reminder/i), "check the oven banana");
  await userEvent.click(screen.getByRole("button", { name: /add reminder/i }));

  expect(await screen.findByText(/couldn't understand that time/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/reminder/i)).toHaveValue("check the oven banana");
});

test("reminders: adding a valid sentence clears the field and shows the new reminder", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Call the dentist");

  await userEvent.type(screen.getByLabelText(/reminder/i), "in 10 minutes to check the oven");
  await userEvent.click(screen.getByRole("button", { name: /add reminder/i }));

  await waitFor(() => expect(screen.getByLabelText(/reminder/i)).toHaveValue(""));
});
```

- [ ] **Step 2: Run to verify the tests fail**

Run: `cd web/ui && npx vitest run src/pages/home/home.test.tsx`
Expected: FAIL (two labels no longer exist / arg shape mismatch).

- [ ] **Step 3: Update `useCreateReminder` in `home.ts`**

```ts
export function useCreateReminder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ text }: { text: string }) =>
      api.post<Reminder>("/api/v1/reminders", { text }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
}
```

- [ ] **Step 4: Rewrite `AddReminderForm` (single input + loading + toast) in `HomePage.tsx`**

```tsx
function AddReminderForm() {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const create = useCreateReminder();
  const { toast } = useToast();

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const r = await create.mutateAsync({ text });
      setText("");
      const at = new Date(r.remind_at);
      toast(`Reminder set for ${at.toLocaleString([], {
        weekday: "short", hour: "2-digit", minute: "2-digit",
      })}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <form onSubmit={handleSubmit} className="mt-1.5 space-y-1 px-1">
      <Input
        aria-label="Reminder"
        placeholder="Remind me in 10 minutes to call the doctor…"
        value={text}
        onChange={(e) => setText(e.target.value)}
        disabled={create.isPending}
        className="h-7 text-xs"
      />
      {error && <p className="text-[11px] text-danger">{error}</p>}
      <Button
        type="submit"
        size="xs"
        variant="outline"
        disabled={!text.trim() || create.isPending}
        className="w-full"
      >
        {create.isPending ? <><Loader2 className="animate-spin" /> Setting…</> : <><Plus /> Add reminder</>}
      </Button>
    </form>
  );
}
```

Add `Loader2` to the `lucide-react` import; import `useToast` from `@/components/shell/Toast` (verify the export name in Step 5 first).

- [ ] **Step 5: Move `RemindersSection` above `InboxSection`** — in `HomePage`'s ContextPane body swap the order:

```tsx
          <div className="min-h-0 flex-1 overflow-y-auto p-3">
            <RemindersSection />
            <InboxSection />
          </div>
```

Also adjust the section spacing so the top one doesn't have an unbalanced offset: `RemindersSection`'s wrapper `pt-3` → `pb-3` border-bottom like the inbox had; make `InboxSection` no longer need its bottom border. (Confirm visually; keep it minimal — swapping the `border-b`/`pt` between the two.)

- [ ] **Step 6: Verify the Toast hook name**

Run: `grep -n "export" web/ui/src/components/shell/Toast.tsx | grep -i "toast"`
Adjust the import/usage in Step 4 to the real exported hook + method signature (e.g. `useToast()` returning `{ toast }` or a bare function). Fix if different.

- [ ] **Step 7: Run the frontend tests**

Run: `cd web/ui && npx vitest run src/pages/home/`
Expected: PASS.

- [ ] **Step 8: Typecheck + build the UI**

Run: `cd web/ui && npx tsc --noEmit && npm run build`
Expected: clean.

- [ ] **Step 9: Commit**

```bash
git add web/ui/src/lib/home.ts web/ui/src/pages/home/HomePage.tsx web/ui/src/pages/home/home.test.tsx
git commit -m "feat(web/home): single-field reminder input, moved above the inbox"
```

---

### Task 5: Full verification + PR

- [ ] **Step 1:** `go test ./... -count=1 -timeout 120s` → PASS.
- [ ] **Step 2:** `cd web/ui && npx vitest run && npx tsc --noEmit` → PASS.
- [ ] **Step 3:** `make build` (ui + go) → artifact builds.
- [ ] **Step 4:** Push branch, open draft PR summarizing the three-layer change (shared parser, web API `text` field, single-field UI moved above inbox).

## Self-Review

- **Spec coverage:** shared parser → Task 1; web `text` field + `no_time` → Task 2; Telegram parity via delegation → Task 3; one field / moved up / loading / toast → Task 4; tests across all → each task + Task 5. ✓
- **Placeholders:** none — all code shown. Step 5.5 (spacing) and 4.6/4.5 (toast hook name, section spacing) are explicit "verify against real export/render" steps, not vague TODOs. ✓
- **Type consistency:** `ParseReminderText` signature identical in Tasks 1/2/3; `useCreateReminder({ text })` identical in Tasks 4.3/4.4; `no_time` vs `unparseable_time` codes consistent. ✓
