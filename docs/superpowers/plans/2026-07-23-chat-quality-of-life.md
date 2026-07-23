# Chat quality-of-life Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-title chats from their first real exchange (keeping the creation timestamp as smaller text), OCR uploaded images when tesseract is present, and make a new chat open on the first click.

**Architecture:** A shared `internal/chat` helper renames a chat once, gated to skip the default-name check and attachment-confirmation turns, via an injected `TitleGenerator` wired from the per-workspace coder — called from both the web send handler and the gateway text handler so all surfaces behave identically. Image OCR follows the existing `pdftotext`-if-present pattern inside `internal/convert`. The new-chat click bug is a React-Query cache-timing fix (optimistic insert).

**Tech Stack:** Go (SQLite via modernc, urfave/cli, Echo), React + TypeScript + @tanstack/react-query + vitest, tesseract CLI (optional).

## Global Constraints

- **Conventional Commits** (`type(scope): summary`); branch is `chat-kb-improvements`, never commit to `main`.
- **Platform-parity rule:** web and chat apps (Telegram/Discord/Slack) must get the same behaviour; the auto-title trigger lives at both persist sites.
- Auto-titling must **never** block a chat turn or surface an error — failure is a silent no-op.
- `internal/convert` stays a **pure function** (no network, no LLM); shelling to a local CLI that is present-or-not is allowed (mirrors `pdf.go`).
- Tests that require an external binary (`tesseract`) **self-skip** when it is absent, mirroring the `python3`/`pdftotext` convention.
- Go tests: `go test ./... -count=1 -timeout 120s`. Frontend tests: `cd web/ui && npx vitest run <file>`.

---

### Task 1: `db.UpdateChatName` + auto-title helper in `internal/chat`

**Files:**
- Modify: `internal/db/repositories.go` (add `UpdateChatName` near `AddChatMessage`, ~line 735)
- Modify: `internal/chat/chat.go` (add `TitleGenerator` type + `isDefaultChatName` + `sanitizeTitle` + `MaybeAutoTitle`)
- Test: `internal/chat/autotitle_test.go` (new)
- Test: `internal/db/repositories_test.go` (add an `UpdateChatName` case if a chats test file exists; otherwise put it in the chat test using a real DB helper)

**Interfaces:**
- Consumes: `db.Chat` (fields `ID`, `Name`, `WorkspaceID`, `CreatedAt`), `db.DB.UpdateChatName`.
- Produces:
  - `func (d *DB) UpdateChatName(chatID, name string) error`
  - `type TitleGenerator func(ctx context.Context, workspaceID, firstUserMsg, firstReply string) (string, error)`
  - `func MaybeAutoTitle(database *db.DB, gen TitleGenerator, ch *db.Chat, firstUserMsg, firstReply string)`
  - `func isDefaultChatName(name string) bool`
  - `func sanitizeTitle(raw string) string`

- [ ] **Step 1: Write the failing test for `sanitizeTitle` and `isDefaultChatName`**

Create `internal/chat/autotitle_test.go`:

```go
package chat

import "testing"

func TestIsDefaultChatName(t *testing.T) {
	cases := map[string]bool{
		"Chat 2026-07-23 15:04": true,
		"Chat 2026-01-02 09:00": true,
		"Invoice questions":     false,
		"Chat about chat":       false,
		"":                      false,
	}
	for name, want := range cases {
		if got := isDefaultChatName(name); got != want {
			t.Errorf("isDefaultChatName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"  Invoice Questions  ":            "Invoice Questions",
		"\"Trip Planning\"":                "Trip Planning",
		"Budget review.":                   "Budget review",
		"Title: Meeting Notes":             "Meeting Notes",
		"line one\nline two":               "line one line two",
		"":                                 "",
	}
	for raw, want := range cases {
		if got := sanitizeTitle(raw); got != want {
			t.Errorf("sanitizeTitle(%q) = %q, want %q", raw, got, want)
		}
	}
	// Over-long titles are capped.
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	if got := sanitizeTitle(long); len(got) > 60 {
		t.Errorf("sanitizeTitle did not cap length: got %d chars", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run 'TestIsDefaultChatName|TestSanitizeTitle' -v`
Expected: FAIL (compile error — `isDefaultChatName`/`sanitizeTitle` undefined).

- [ ] **Step 3: Implement the helpers in `internal/chat/chat.go`**

Add these imports to the existing `import` block: `"context"` (already present), `"regexp"`. Then append:

```go
// defaultChatNameRE matches the auto-generated "Chat <timestamp>" name that
// apiCreateChat / the gateway assign to a fresh chat. A chat still carrying
// this name has never been titled from content, so it is eligible for a
// one-time auto-title; anything else was named by the user or a prior
// auto-title and is left alone.
var defaultChatNameRE = regexp.MustCompile(`^Chat \d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)

func isDefaultChatName(name string) bool {
	return defaultChatNameRE.MatchString(name)
}

// attachmentPrefix is the leading text of the attachment-confirmation turn the
// web chat posts after importing a file (see attachFiles in ChatWindow.tsx).
// Those turns are real coder turns but must NOT drive the chat title, or a
// chat whose first action is a file drop would be named after the file.
const attachmentPrefix = "📎 Attached"

// sanitizeTitle normalizes a model-produced title into a short, single-line
// label: strips surrounding whitespace/quotes, a leading "Title:" prefix,
// trailing sentence punctuation, collapses internal newlines to spaces, and
// caps length. Returns "" when nothing usable remains (caller then skips the
// rename).
func sanitizeTitle(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	// Collapse runs of whitespace produced by the newline replacement.
	s = strings.Join(strings.Fields(s), " ")
	// Drop a leading "Title:" the model sometimes echoes.
	if i := strings.Index(strings.ToLower(s), "title:"); i == 0 {
		s = strings.TrimSpace(s[len("title:"):])
	}
	s = strings.Trim(s, "\"'“”")
	s = strings.TrimRight(s, ".!?, ")
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
		s = strings.TrimSpace(s)
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/ -run 'TestIsDefaultChatName|TestSanitizeTitle' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test for `UpdateChatName` and `MaybeAutoTitle`**

Add to `internal/chat/autotitle_test.go` (uses the same in-memory/temp DB helper other `internal/db` or `internal/chat` tests use — check an existing test in this package or `internal/db` for the constructor, e.g. `db.Open(":memory:")` or a `testDB(t)` helper, and reuse it verbatim):

```go
func TestMaybeAutoTitle(t *testing.T) {
	database := newTestDB(t) // reuse the existing test-DB constructor pattern
	ws := seedWorkspace(t, database)
	ch := &db.Chat{ID: "c1", WorkspaceID: ws, Name: "Chat 2026-07-23 15:04", Active: true}
	if err := database.CreateChat(ch); err != nil {
		t.Fatal(err)
	}

	gen := func(_ context.Context, _ , _, _ string) (string, error) { return "Invoice Questions", nil }

	// Real user exchange → chat gets titled.
	MaybeAutoTitle(database, gen, ch, "how do I read this invoice?", "Here is how…")
	waitFor(t, func() bool {
		got, _ := database.GetChat("c1")
		return got.Name == "Invoice Questions"
	})

	// Already titled → never re-titled.
	ch2, _ := database.GetChat("c1")
	MaybeAutoTitle(database, func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("generator must not run for an already-titled chat")
		return "", nil
	}, ch2, "another question", "another answer")

	// Attachment-confirmation first turn → skipped (stays default).
	ch3 := &db.Chat{ID: "c2", WorkspaceID: ws, Name: "Chat 2026-07-23 16:00", Active: true}
	database.CreateChat(ch3)
	MaybeAutoTitle(database, func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("generator must not run for an attachment-confirmation turn")
		return "", nil
	}, ch3, "📎 Attached **invoice.pdf** to my knowledge base as `notes/invoice.md`.", "Got it.")
}
```

> Note: `newTestDB`, `seedWorkspace`, and `waitFor` are illustrative — before writing this test, open an existing `internal/chat` or `internal/db` test file and reuse whatever DB-construction + workspace-seed helpers already exist there. If a `waitFor`-style poll helper does not exist, add a tiny local one that polls a predicate for up to ~2s (the rename runs in a goroutine).

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestMaybeAutoTitle -v`
Expected: FAIL (`UpdateChatName`/`MaybeAutoTitle` undefined).

- [ ] **Step 7: Implement `UpdateChatName` in `internal/db/repositories.go`**

Add near `AddChatMessage` (~line 735):

```go
// UpdateChatName renames a chat. Used by the auto-title flow to replace the
// default "Chat <timestamp>" name with a content-derived topic.
func (d *DB) UpdateChatName(chatID, name string) error {
	_, err := d.Exec(`UPDATE chats SET name=? WHERE id=?`, name, chatID)
	return err
}
```

- [ ] **Step 8: Implement `TitleGenerator` + `MaybeAutoTitle` in `internal/chat/chat.go`**

```go
// TitleGenerator produces a short chat title from the first real exchange.
// Injected (not built here) so internal/chat stays free of a coder dependency;
// main.go supplies a closure backed by the per-workspace coder.
type TitleGenerator func(ctx context.Context, workspaceID, firstUserMsg, firstReply string) (string, error)

// MaybeAutoTitle renames a chat exactly once, from its first substantive
// exchange. It is a no-op (never an error, never blocking) unless the chat
// still carries its default "Chat <timestamp>" name AND the user message that
// produced this turn is a real message rather than an attachment confirmation.
// The rename runs in the background so it never delays the user's reply; any
// failure leaves the default name in place.
func MaybeAutoTitle(database *db.DB, gen TitleGenerator, ch *db.Chat, firstUserMsg, firstReply string) {
	if ch == nil || gen == nil {
		return
	}
	if !isDefaultChatName(ch.Name) {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(firstUserMsg), attachmentPrefix) {
		return
	}
	if strings.TrimSpace(firstUserMsg) == "" || strings.TrimSpace(firstReply) == "" {
		return
	}
	chatID := ch.ID
	workspaceID := ch.WorkspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := gen(ctx, workspaceID, firstUserMsg, firstReply)
		if err != nil {
			slog.Debug("auto-title: generator failed", "chat", chatID, "err", err)
			return
		}
		title := sanitizeTitle(raw)
		if title == "" {
			return
		}
		if err := database.UpdateChatName(chatID, title); err != nil {
			slog.Warn("auto-title: update chat name", "chat", chatID, "err", err)
		}
	}()
}
```

(`time` and `slog` are already imported in `chat.go`.)

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test ./internal/chat/ ./internal/db/ -count=1 -v`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/db/repositories.go internal/chat/chat.go internal/chat/autotitle_test.go
git commit -m "feat(chat): one-time auto-title helper + UpdateChatName"
```

---

### Task 2: Title prompt + wire `MaybeAutoTitle` into both surfaces

**Files:**
- Modify: `internal/prompts/prompts.go` (add `BuildChatTitlePrompt` + `ChatTitleUserPrompt`)
- Modify: `cmd/simple-agents/main.go` (build the `titleGen` closure; pass to web server + router)
- Modify: `internal/gateway/router.go` (add `titleGen` field + `WithTitleGenerator`; call `chat.MaybeAutoTitle` after persisting)
- Modify: `web/server.go` (add `titleGen chat.TitleGenerator` field + `WithTitleGenerator` setter)
- Modify: `web/handlers_misc.go` (call `chat.MaybeAutoTitle` after persisting the assistant turn)
- Test: `internal/prompts/prompts_test.go` (assert the title prompt shape)

**Interfaces:**
- Consumes: `chat.TitleGenerator`, `chat.MaybeAutoTitle`, `coder.Coder.WithNoTools`, `coder.Coder.Chat(ctx, workspaceID, history []db.ChatMessage, systemContext, userMessage string) (*coder.Result, error)`, the existing `coderFor` closure in `main.go`, `s.coderForWorkspace` in web.
- Produces: `prompts.BuildChatTitlePrompt() string`, `prompts.ChatTitleUserPrompt(userMsg, reply string) string`, `(*gateway.Router).WithTitleGenerator(chat.TitleGenerator) *Router`, `(*web.Server).WithTitleGenerator(chat.TitleGenerator) *Server`.

- [ ] **Step 1: Write the failing prompt test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestBuildChatTitlePrompt(t *testing.T) {
	sys := BuildChatTitlePrompt()
	for _, want := range []string{"title", "3", "6"} { // mentions a short word-count target
		if !strings.Contains(strings.ToLower(sys), want) {
			t.Errorf("title system prompt missing %q; got:\n%s", want, sys)
		}
	}
	u := ChatTitleUserPrompt("hello there", "general kenobi")
	if !strings.Contains(u, "hello there") || !strings.Contains(u, "general kenobi") {
		t.Errorf("user prompt must include both turns; got:\n%s", u)
	}
	// Truncates very long turns.
	long := strings.Repeat("x", 5000)
	got := ChatTitleUserPrompt(long, long)
	if len(got) > 6000 {
		t.Errorf("user prompt not truncated: %d chars", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompts/ -run TestBuildChatTitlePrompt -v`
Expected: FAIL (undefined functions).

- [ ] **Step 3: Implement the prompts in `internal/prompts/prompts.go`**

```go
// BuildChatTitlePrompt is the system prompt for the one-shot chat auto-title
// call. It asks for a bare topic label — no preamble, no quotes.
func BuildChatTitlePrompt() string {
	return "You name chat conversations. Given the first message and reply, respond with a " +
		"concise 3 to 6 word topic in Title Case that captures what the conversation is about. " +
		"Respond with ONLY the title — no quotes, no trailing punctuation, no preamble, no explanation."
}

// ChatTitleUserPrompt builds the user turn for the auto-title call, bounding
// each side so a long exchange can't blow the token budget.
func ChatTitleUserPrompt(userMsg, reply string) string {
	const cap = 2000
	trunc := func(s string) string {
		if len(s) > cap {
			return s[:cap]
		}
		return s
	}
	return "First message:\n" + trunc(userMsg) + "\n\nReply:\n" + trunc(reply)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/prompts/ -run TestBuildChatTitlePrompt -v`
Expected: PASS.

- [ ] **Step 5: Add the `titleGen` field + setter to the gateway Router**

In `internal/gateway/router.go`, add to the `Router` struct (after `vault`):

```go
	titleGen chat.TitleGenerator // optional; auto-titles a chat from its first exchange
```

Add the import `"github.com/ilijad1/simple-agents/internal/chat"` to the file's import block. Add the setter next to the other `With*` setters:

```go
// WithTitleGenerator enables one-time content-based auto-titling of chats.
func (r *Router) WithTitleGenerator(g chat.TitleGenerator) *Router {
	r.titleGen = g
	return r
}
```

- [ ] **Step 6: Call `MaybeAutoTitle` in the router after persisting**

In `internal/gateway/router.go`, in the block that persists the chat turn (currently):

```go
	if activeChat != nil && assistantReply != "" {
		_ = r.db.AddChatMessage(activeChat.ID, "user", msg.Text)
		_ = r.db.AddChatMessage(activeChat.ID, "assistant", assistantReply)
		_ = r.db.TouchChat(activeChat.ID)
	}
```

append inside the `if` (after `TouchChat`):

```go
		chat.MaybeAutoTitle(r.db, r.titleGen, activeChat, msg.Text, assistantReply)
```

- [ ] **Step 7: Add the `titleGen` field + setter to the web Server**

In `web/server.go`, add to the `Server` struct:

```go
	titleGen chat.TitleGenerator
```

Ensure `web/server.go` imports `"github.com/ilijad1/simple-agents/internal/chat"` (it likely already does — check; add if missing). Add the setter:

```go
// WithTitleGenerator enables one-time content-based auto-titling of chats.
func (s *Server) WithTitleGenerator(g chat.TitleGenerator) *Server {
	s.titleGen = g
	return s
}
```

- [ ] **Step 8: Call `MaybeAutoTitle` in the web send handler**

In `web/handlers_misc.go`, after the two `AddChatMessage` calls (lines ~169–170):

```go
	_ = s.db.AddChatMessage(id, "user", text)
	_ = s.db.AddChatMessage(id, "assistant", result.Text)
```

add:

```go
	if ch, err := s.db.GetChat(id); err == nil {
		chat.MaybeAutoTitle(s.db, s.titleGen, ch, text, result.Text)
	}
```

Ensure `web/handlers_misc.go` imports `"github.com/ilijad1/simple-agents/internal/chat"`.

- [ ] **Step 9: Build the `titleGen` closure in `main.go` and inject it**

In `cmd/simple-agents/main.go`, after the `coderFor` closure is defined (~line 140) and `prompts` is imported, add:

```go
			titleGen := func(ctx context.Context, workspaceID, userMsg, reply string) (string, error) {
				res, err := coderFor(workspaceID).WithNoTools().
					Chat(ctx, workspaceID, nil, prompts.BuildChatTitlePrompt(), prompts.ChatTitleUserPrompt(userMsg, reply))
				if err != nil {
					return "", err
				}
				return res.Text, nil
			}
```

Then chain `.WithTitleGenerator(titleGen)` onto the `gateway.NewRouter(...)` builder (line ~381), and chain `.WithTitleGenerator(titleGen)` onto the `web.NewServer(...)` result (find where the server is constructed in `main.go` and add the call — if `NewServer` returns `(*Server, error)`, call the setter on the value after the error check).

(Confirm `"context"` and the `prompts` package are imported in `main.go` — both are almost certainly already present; add if the build complains.)

- [ ] **Step 10: Build and run the full suite**

Run: `go build ./... && go test ./internal/prompts/ ./internal/gateway/ ./internal/chat/ ./web/ -count=1`
Expected: builds clean, tests PASS. Fix any import-cycle or missing-import errors surfaced by the build (the design keeps `internal/chat` free of a `coder`/`prompts` import specifically to avoid a cycle — do not add those imports to `internal/chat`).

- [ ] **Step 11: Commit**

```bash
git add internal/prompts/prompts.go internal/prompts/prompts_test.go internal/gateway/router.go web/server.go web/handlers_misc.go cmd/simple-agents/main.go
git commit -m "feat(chat): wire auto-title into web + gateway surfaces"
```

---

### Task 3: Show the creation timestamp as smaller text

**Files:**
- Modify: `web/ui/src/lib/utils.ts` (add `formatShortDate` if absent)
- Modify: `web/ui/src/pages/chats/ChatsPage.tsx` (list row secondary line)
- Modify: `web/ui/src/pages/chats/ChatWindow.tsx` (header)
- Test: `web/ui/src/pages/chats/chats.test.tsx` (assert the timestamp renders)

**Interfaces:**
- Consumes: `Chat.created_at` (already on the DTO and the `Chat` type in `lib/chats.ts`), `cn`, existing `timeAgo`.
- Produces: `formatShortDate(iso: string): string` in `lib/utils.ts`.

- [ ] **Step 1: Write the failing test**

Add to `web/ui/src/pages/chats/chats.test.tsx` a case asserting a chat row renders a formatted creation date (use the existing render/setup harness in that file; match on a stable fragment of `formatShortDate`'s output, e.g. the year or `Jul`):

```tsx
it("shows the creation timestamp as secondary text", async () => {
  // ...render ChatsPage with a chat whose created_at is 2026-07-23T15:04:00Z
  // (reuse the file's existing mock-server / QueryClient setup)
  expect(await screen.findByText(/2026|Jul/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/chats/chats.test.tsx`
Expected: FAIL (no such text yet).

- [ ] **Step 3: Add `formatShortDate` to `lib/utils.ts`**

```ts
// formatShortDate renders an ISO timestamp as a short local date+time,
// e.g. "Jul 23, 15:04" — the small "kept timestamp" shown under a chat's
// content-derived title.
export function formatShortDate(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
```

- [ ] **Step 4: Render it in the list row (`ChatsPage.tsx`)**

Replace the existing secondary line:

```tsx
                <span className="text-xs text-muted-2">{timeAgo(c.updated_at)}</span>
```

with a two-part small line that keeps activity time and adds the creation timestamp:

```tsx
                <span className="text-xs text-muted-2">
                  {formatShortDate(c.created_at)} · {timeAgo(c.updated_at)}
                </span>
```

Add `formatShortDate` to the existing `import { cn, timeAgo } from "@/lib/utils";` line.

- [ ] **Step 5: Render it in the window header (`ChatWindow.tsx`)**

Replace the header title block:

```tsx
        <div className="flex min-w-0 items-center gap-2">
          <h2 className="truncate text-sm font-bold">{chat.name}</h2>
          <StatusChip active={chat.active} />
        </div>
```

with one that stacks the small creation timestamp under the title:

```tsx
        <div className="flex min-w-0 items-center gap-2">
          <div className="min-w-0">
            <h2 className="truncate text-sm font-bold">{chat.name}</h2>
            <span className="text-xs text-muted-2">{formatShortDate(chat.created_at)}</span>
          </div>
          <StatusChip active={chat.active} />
        </div>
```

Add the import: `import { cn, formatShortDate } from "@/lib/utils";` (merge with the existing `cn` import).

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/chats/chats.test.tsx`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/lib/utils.ts web/ui/src/pages/chats/ChatsPage.tsx web/ui/src/pages/chats/ChatWindow.tsx web/ui/src/pages/chats/chats.test.tsx
git commit -m "feat(web/chat): show chat creation timestamp as secondary text"
```

---

### Task 4: Image OCR on upload (tesseract-if-present)

**Files:**
- Modify: `internal/convert/convert.go` (extract the `KindImage` arm into `imageToMarkdown`)
- Create: `internal/convert/image.go` (`imageToMarkdown` + tesseract detection/exec)
- Test: `internal/convert/image_test.go` (new)
- Test fixture: `internal/convert/testdata/ocr_sample.png` (a small PNG with known text — generate in the test-fixture step)

**Interfaces:**
- Consumes: `convert.Options` (has `Filename`), `convert.Result`, `titleFromFilename`, `normalizeText`, `Kind`/`KindImage`.
- Produces: `func imageToMarkdown(data []byte, opt Options) (Result, error)`.

- [ ] **Step 1: Write the failing test (fallback path always covered; OCR path self-skips)**

Create `internal/convert/image_test.go`:

```go
package convert

import (
	"os/exec"
	"strings"
	"testing"
)

// The no-tesseract fallback must always produce the honest stub, on every host.
func TestImageToMarkdown_FallbackWhenNoTesseract(t *testing.T) {
	res, err := imageToMarkdownWith(nil, Options{Filename: "photo.png"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Extractor != "none" {
		t.Errorf("extractor = %q, want none", res.Extractor)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a no-OCR warning")
	}
}

// The OCR path runs only when tesseract is installed.
func TestImageToMarkdown_OCR(t *testing.T) {
	bin, err := exec.LookPath("tesseract")
	if err != nil {
		t.Skip("tesseract not on PATH; skipping OCR path")
	}
	data := mustReadFixture(t, "ocr_sample.png") // small PNG containing the text "HELLO OCR"
	res, err := imageToMarkdownWith(data, Options{Filename: "ocr_sample.png"}, bin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(res.Markdown), "HELLO") {
		t.Errorf("OCR text missing; got: %q", res.Markdown)
	}
	if res.Extractor != "tesseract" {
		t.Errorf("extractor = %q, want tesseract", res.Extractor)
	}
}
```

> `mustReadFixture` — reuse the existing fixture-reading helper in `internal/convert` tests (check `pdf_test.go`/`ooxml_test.go` for the pattern; if none, add `os.ReadFile("testdata/"+name)`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/convert/ -run TestImageToMarkdown -v`
Expected: FAIL (`imageToMarkdownWith` undefined).

- [ ] **Step 3: Implement `internal/convert/image.go`**

```go
package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// imageToMarkdown extracts text from an image via tesseract when it is on PATH,
// falling back to an honest "no OCR" stub otherwise. This mirrors pdf.go's
// "prefer the local CLI, degrade gracefully" shape and keeps convert a pure
// function: it shells to a present-or-absent local tool, never the network.
func imageToMarkdown(data []byte, opt Options) (Result, error) {
	bin, _ := exec.LookPath("tesseract")
	return imageToMarkdownWith(data, opt, bin)
}

// imageToMarkdownWith is the testable core: bin == "" forces the fallback path.
func imageToMarkdownWith(data []byte, opt Options, bin string) (Result, error) {
	if bin == "" {
		return Result{
			Markdown:  fmt.Sprintf("(image file, %d bytes — no text was extracted; OCR is not available)\n", len(data)),
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindImage,
			Extractor: "none",
			Warnings:  []string{"image content is not searchable: no OCR"},
		}, nil
	}

	text, err := runTesseract(bin, data)
	if err != nil {
		// A tool failure degrades to the same honest stub rather than erroring
		// the whole import — the file is still worth recording.
		return Result{
			Markdown:  fmt.Sprintf("(image file, %d bytes — OCR failed: %v)\n", len(data), err),
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindImage,
			Extractor: "none",
			Warnings:  []string{"image OCR failed: " + err.Error()},
		}, nil
	}

	res := Result{
		Kind:      KindImage,
		Extractor: "tesseract",
		Title:     titleFromFilename(opt.Filename),
		Markdown:  normalizeText(text),
	}
	if strings.TrimSpace(text) == "" {
		res.Warnings = append(res.Warnings, "OCR found no text in the image (it may be a photo or diagram)")
	}
	return res, nil
}

// runTesseract writes the image to a temp file and runs `tesseract <file> stdout`.
// A temp file (rather than stdin) is the portable invocation across tesseract
// builds.
func runTesseract(bin string, data []byte) (string, error) {
	dir, err := os.MkdirTemp("", "sa-ocr-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "img")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, in, "stdout")
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
```

- [ ] **Step 4: Route the `KindImage` arm to `imageToMarkdown` in `convert.go`**

Replace the inline `case KindImage:` block (the `return Result{... "no text was extracted" ...}` literal) with:

```go
	case KindImage:
		return imageToMarkdown(data, opt)
```

- [ ] **Step 5: Create the OCR fixture (only needed for the self-skipping OCR test)**

If `tesseract` is installed on the dev host, generate a tiny PNG containing the text `HELLO OCR` and save it to `internal/convert/testdata/ocr_sample.png` (any method: an image tool, or a short throwaway Go/Python snippet drawing the text). If `tesseract` is not installed, the OCR test self-skips and the fixture is not exercised — create it later when enabling OCR. The fallback test needs no fixture.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/convert/ -run TestImageToMarkdown -v`
Expected: `TestImageToMarkdown_FallbackWhenNoTesseract` PASS; `TestImageToMarkdown_OCR` PASS if tesseract present, else SKIP.

- [ ] **Step 7: Run the whole convert package to confirm no regression**

Run: `go test ./internal/convert/ -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/convert/image.go internal/convert/convert.go internal/convert/image_test.go internal/convert/testdata/
git commit -m "feat(convert): OCR uploaded images via tesseract when present"
```

---

### Task 5: New chat opens on the first click

**Files:**
- Modify: `web/ui/src/lib/chats.ts` (`useCreateChat.onSuccess` optimistic insert)
- Test: `web/ui/src/pages/chats/chats.test.tsx` (create → window opens without a second click)

**Interfaces:**
- Consumes: `Chat` type, the `["chats"]` query shape `{ chats: Chat[] }`, the `apiCreateChat` response (a `Chat` DTO).
- Produces: updated `useCreateChat` that seeds the cache.

- [ ] **Step 1: Write the failing test**

Add to `web/ui/src/pages/chats/chats.test.tsx` (reuse the file's render harness + a mock POST `/api/v1/chats` returning a chat whose id is NOT yet in the list query, and a `/api/v1/chats` list that initially omits it — reproducing the race):

```tsx
it("opens a newly created chat on the first click", async () => {
  // render ChatsPage; the list query resolves WITHOUT the new chat.
  // click "+ New chat" (POST returns { id: "new1", name: "Chat …", ... }).
  await userEvent.click(screen.getByRole("button", { name: /new chat/i }));
  // The chat window for new1 must be shown immediately — not cleared by the
  // dead-selection guard — before any list refetch lands.
  expect(await screen.findByTestId("chat-window")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/chats/chats.test.tsx`
Expected: FAIL — the window is cleared because the new chat isn't in the cached list.

- [ ] **Step 3: Seed the cache in `useCreateChat.onSuccess`**

Replace:

```ts
export function useCreateChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name?: string) => api.post<Chat>("/api/v1/chats", name ? { name } : undefined),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["chats"] });
    },
  });
}
```

with:

```ts
export function useCreateChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name?: string) => api.post<Chat>("/api/v1/chats", name ? { name } : undefined),
    onSuccess: (chat) => {
      // Insert the created chat into the cached list synchronously so the
      // ChatsPage dead-selection guard sees it immediately and the window
      // opens on the first click. Without this, the list is briefly stale
      // (no new chat), the guard clears the selection, and the user has to
      // click the row after the refetch lands.
      qc.setQueryData<{ chats: Chat[] }>(["chats"], (old) =>
        old ? { ...old, chats: [chat, ...old.chats] } : { chats: [chat] },
      );
      qc.invalidateQueries({ queryKey: ["chats"] });
    },
  });
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/chats/chats.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/lib/chats.ts web/ui/src/pages/chats/chats.test.tsx
git commit -m "fix(web/chat): new chat opens on first click via optimistic cache insert"
```

---

## Final verification

- [ ] `go build ./...` — clean.
- [ ] `go test ./... -count=1 -timeout 120s` — PASS.
- [ ] `cd web/ui && npx vitest run` — PASS; `npm run build` (or `make ui`) succeeds.
- [ ] Manual smoke (optional, on a throwaway data dir per the live-instance-safety rule): start a chat, send one message, confirm the chat renames itself shortly after and the creation timestamp shows small under the title; click "+ New chat" and confirm the window opens on the first click.
