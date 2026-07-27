# Chat message UX + KB new-note flow — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Opening a stopped chat resumes it and puts the caret in the composer; every chat bubble gains a hover-revealed timestamp + copy button and the Send button gains an icon; creating a note from the ⌘K palette opens it in the rich text editor instead of leaving an unrelated (sometimes broken) note on screen.

**Architecture:** All changes are in the React SPA (`web/ui/src`) except one additive field on the existing session endpoint (`web/api_auth.go`), which carries the workspace profile's timezone to the browser so message times render in the user's configured zone. A new focused component `components/chat/MessageMeta.tsx` owns the per-message footer; `lib/utils.ts` gains one pure formatter; the KB fixes are three small, independent edits in `pages/kb/KBPage.tsx` and `pages/kb/FileTree.tsx`.

**Tech Stack:** Go 1.x + Echo v4 (API), React 19 + TypeScript + Vite + TanStack Query + react-router v7 + Tailwind + lucide-react (SPA), Vitest + Testing Library (frontend tests), `go test` (backend tests).

## Global Constraints

- Frontend tests run with `cd web/ui && npx vitest run <file>`; the whole suite is `cd web/ui && npm test`. Backend: `go test ./web/... -count=1`.
- The Send button's **accessible name must stay exactly `"Send"`** — `chat.test.tsx`, `globalchat.test.tsx`, `attachments.test.tsx` and the designer suites all query `getByRole("button", { name: "Send" })`.
- `ChatMessageBubble` is shared with `components/designer/DesignerSurface.tsx`. Any change to it must keep `designer.test.tsx` and `specpanel.test.tsx` green.
- The per-message footer must be **always mounted** and hover-revealed by opacity only. Never conditionally render it, never put `select-none` on the message body — that breaks drag-select, which the user called out explicitly.
- `Intl.DateTimeFormat(locale, { timeZone })` throws `RangeError` for `""`, `"CEST"`, `"UTC+2"`. Every use must be inside a try/catch with a browser-local fallback.
- Conventional Commits (`feat(web/chat): …`, `fix(web/kb): …`). Commit after each task.
- Do not run `make deploy` — this branch is not the production path.

---

### Task 1: `formatMessageTime` — timezone-safe "Day HH:MM"

**Files:**
- Modify: `web/ui/src/lib/utils.ts` (append after `formatShortDate`)
- Create: `web/ui/src/lib/utils.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `formatMessageTime(iso: string, timeZone?: string): string` — returns `""` for an unparseable date; renders `"Sun, 21:00"` style (short weekday + 24-hour clock); falls back to browser-local when `timeZone` is empty or not a valid IANA zone.

- [ ] **Step 1: Write the failing test**

Create `web/ui/src/lib/utils.test.ts`:

```ts
import { formatMessageTime } from "./utils";

// 2026-07-26T12:00:00Z is a Sunday at noon UTC.
const ISO = "2026-07-26T12:00:00Z";

test("formats as short weekday + 24-hour time in the given IANA zone", () => {
  // Tokyo is UTC+9 → 21:00 the same Sunday.
  const out = formatMessageTime(ISO, "Asia/Tokyo");
  expect(out).toContain("Sun");
  expect(out).toContain("21:00");
});

test("a different zone yields a different clock time", () => {
  expect(formatMessageTime(ISO, "UTC")).toContain("12:00");
});

// profile.Timezone is a free-text settings field, so "CEST"/"UTC+2"/"" are all
// reachable. Intl throws RangeError on those; a throw during render would blank
// every bubble in the chat, so the formatter must absorb it.
test("an invalid zone falls back to browser-local instead of throwing", () => {
  const local = formatMessageTime(ISO);
  expect(() => formatMessageTime(ISO, "CEST")).not.toThrow();
  expect(formatMessageTime(ISO, "CEST")).toBe(local);
  expect(formatMessageTime(ISO, "")).toBe(local);
});

test("an unparseable date renders nothing rather than 'Invalid Date'", () => {
  expect(formatMessageTime("not a date")).toBe("");
  expect(formatMessageTime("")).toBe("");
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/lib/utils.test.ts`
Expected: FAIL — `formatMessageTime` is not exported from `./utils`.

- [ ] **Step 3: Write minimal implementation**

Append to `web/ui/src/lib/utils.ts`:

```ts
// formatMessageTime renders a chat message's timestamp as "Sun, 21:00" — a
// short weekday plus a 24-hour clock, no seconds and no date, which is what a
// per-message footer needs (the day is enough context inside one conversation).
//
// `timeZone` is the workspace profile's timezone, a FREE-TEXT settings field:
// it can legitimately hold "", "CEST" or "UTC+2", none of which Intl accepts —
// it throws RangeError. This runs during render for every bubble, so a throw
// would blank the whole conversation. Hence the try/catch fallback to the
// browser's own zone, mirroring Go's profile.LoadLocation, which likewise
// degrades (to UTC) rather than failing on an unparseable zone.
export function formatMessageTime(iso: string, timeZone?: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ""
  const opts: Intl.DateTimeFormatOptions = {
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }
  if (timeZone) {
    try {
      return d.toLocaleString(undefined, { ...opts, timeZone })
    } catch {
      // Not a valid IANA zone — fall through to browser-local below.
    }
  }
  return d.toLocaleString(undefined, opts)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web/ui && npx vitest run src/lib/utils.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/lib/utils.ts web/ui/src/lib/utils.test.ts
git commit -m "feat(web/chat): add timezone-safe formatMessageTime helper"
```

---

### Task 2: Carry the workspace timezone on the session payload

**Files:**
- Modify: `web/api_auth.go` (`apiAuthSession`, around lines 40-65)
- Modify: `web/api_auth_test.go` (append a new test)
- Modify: `web/ui/src/lib/session.ts` (`Session` type + new hook)

**Interfaces:**
- Consumes: `profile.Load(g profile.Getter, workspaceID string) profile.Profile` (from `internal/profile`), `s.activeWorkspace(c)`.
- Produces:
  - JSON: `GET /api/v1/auth/session` → `{..., "timezone": "Europe/Skopje"}` (empty string when no workspace is entered or no timezone is configured).
  - TS: `Session.timezone?: string` and `useDisplayTimeZone(): string | undefined` in `lib/session.ts`.

- [ ] **Step 1: Write the failing test**

Append to `web/api_auth_test.go`:

```go
// The SPA renders per-message chat timestamps in the workspace profile's
// timezone. The session payload is the carrier: it is already fetched once and
// cached by the SPA, unlike /api/v1/settings which re-probes the filesystem for
// installed coders on every call.
func TestAPISessionCarriesWorkspaceTimezone(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// No timezone configured yet → present but empty, never absent.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"timezone":""`) {
		t.Fatalf("default session timezone: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/profile",
		map[string]string{"timezone": "Europe/Skopje"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save profile: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if !contains(rec.Body.String(), `"timezone":"Europe/Skopje"`) {
		t.Fatalf("session timezone after save: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/... -run TestAPISessionCarriesWorkspaceTimezone -count=1`
Expected: FAIL on the first assertion — the payload has no `timezone` key.

- [ ] **Step 3: Write minimal implementation**

In `web/api_auth.go`, ensure `"github.com/ilijad1/simple-agents/internal/profile"` is imported (match the module path already used by `web/api_chats.go`'s profile import), then inside `apiAuthSession` extend the active-workspace branch:

```go
	// Timezone travels with the session (not /api/v1/settings) because the SPA
	// already loads and caches this payload once, and the settings endpoint
	// re-probes the host filesystem for installed coders on every call. Always
	// present as a key — the SPA treats "" as "use the browser's own zone".
	out["timezone"] = ""
	if w, ok := s.activeWorkspace(c); ok {
		out["workspace"] = toAPIWorkspace(w)
		out["timezone"] = profile.Load(s.db, w.ID).Timezone
	}
```

(Replace the existing `if w, ok := s.activeWorkspace(c); ok { out["workspace"] = toAPIWorkspace(w) }` block — do not add a second one.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web/... -run TestAPISession -count=1`
Expected: PASS.

- [ ] **Step 5: Mirror the field in the TS session type**

In `web/ui/src/lib/session.ts`, add to the `Session` type and export a hook:

```ts
export type Session = {
  authenticated: boolean;
  owner?: { id: string; username: string; must_change_password: boolean };
  workspace?: Workspace | null;
  workspaces?: Workspace[];
  // Server-side screen lock. The workspace stays entered while locked; every
  // guarded API route answers 423 until the master password is re-entered.
  locked?: boolean;
  // The active workspace's profile timezone (an IANA name, or "" when unset).
  // Free text server-side, so consumers must tolerate a bogus value — see
  // formatMessageTime.
  timezone?: string;
};
```

```ts
// The zone chat timestamps render in. Undefined (no workspace, no configured
// timezone, or the session not loaded yet) means "use the browser's own zone",
// which is what formatMessageTime does with an absent/empty value.
export function useDisplayTimeZone(): string | undefined {
  return useSession().data?.timezone || undefined;
}
```

- [ ] **Step 6: Typecheck**

Run: `cd web/ui && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add web/api_auth.go web/api_auth_test.go web/ui/src/lib/session.ts
git commit -m "feat(web/api): expose the workspace profile timezone on the session payload"
```

---

### Task 3: `MessageMeta` — hover-revealed timestamp + copy button

**Files:**
- Create: `web/ui/src/components/chat/MessageMeta.tsx`
- Create: `web/ui/src/components/chat/messagemeta.test.tsx`
- Modify: `web/ui/src/components/chat/Bubbles.tsx`

**Interfaces:**
- Consumes: `formatMessageTime` (Task 1), `useDisplayTimeZone` (Task 2), `cn` from `@/lib/utils`.
- Produces:
  - `MessageMeta({ content, createdAt }: { content: string; createdAt?: string })` — the footer row.
  - `ChatMessageBubble` gains an optional third prop `createdAt?: string`.

- [ ] **Step 1: Write the failing test**

Create `web/ui/src/components/chat/messagemeta.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatMessageBubble } from "./Bubbles";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

function renderBubble(ui: React.ReactElement, timezone = "Asia/Tokyo") {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/auth/session") {
        return Promise.resolve(jsonResponse({ authenticated: true, timezone }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

// 2026-07-26T12:00:00Z is Sunday noon UTC → 21:00 in Tokyo.
const ISO = "2026-07-26T12:00:00Z";

test("the footer renders the time in the workspace timezone", async () => {
  renderBubble(<ChatMessageBubble role="assistant" content="hi" createdAt={ISO} />);
  expect(await screen.findByText(/Sun.*21:00/)).toBeInTheDocument();
});

// The footer must exist in the DOM at all times and be revealed with opacity.
// Mounting it on hover reflows the bubble under the cursor and kills an
// in-progress drag-select, which is the behaviour the user explicitly asked to
// preserve.
test("the footer is always mounted and hover-revealed by opacity only", () => {
  const { container } = renderBubble(<ChatMessageBubble role="user" content="hi" createdAt={ISO} />);
  const footer = container.querySelector('[data-testid="message-meta"]')!;
  expect(footer).toBeInTheDocument();
  expect(footer.className).toContain("opacity-0");
  expect(footer.className).toContain("group-hover:opacity-100");
});

test("the message body is selectable (select-none lives only on the footer)", () => {
  const { container } = renderBubble(<ChatMessageBubble role="user" content="selectable" createdAt={ISO} />);
  const body = container.querySelector('[data-testid="message-body"]')!;
  expect(body.className).not.toContain("select-none");
  expect(container.querySelector('[data-testid="message-meta"]')!.className).toContain("select-none");
});

test("copy writes the raw message text to the clipboard", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
  renderBubble(<ChatMessageBubble role="assistant" content="**bold** text" createdAt={ISO} />);
  await userEvent.click(screen.getByRole("button", { name: /copy message/i }));
  expect(writeText).toHaveBeenCalledWith("**bold** text");
  // Feedback: the control flips to a "Copied" state.
  await waitFor(() => expect(screen.getByRole("button", { name: /copied/i })).toBeInTheDocument());
});

// DesignerSurface renders bubbles with no timestamps — the footer must still
// give them a copy button rather than disappearing or rendering "Invalid Date".
test("with no createdAt the copy button remains and no time is shown", () => {
  renderBubble(<ChatMessageBubble role="assistant" content="hi" />);
  expect(screen.getByRole("button", { name: /copy message/i })).toBeInTheDocument();
  expect(screen.queryByTestId("message-time")).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/components/chat/messagemeta.test.tsx`
Expected: FAIL — no `message-meta` element, `createdAt` is not a prop.

- [ ] **Step 3: Write the component**

Create `web/ui/src/components/chat/MessageMeta.tsx`:

```tsx
import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn, formatMessageTime } from "@/lib/utils";
import { useDisplayTimeZone } from "@/lib/session";

const COPIED_MS = 1500;

// The per-message footer: a small timestamp and a copy button under each
// bubble, revealed on hover.
//
// It is ALWAYS mounted and only its opacity changes. Rendering it on hover
// instead would insert a node under the cursor mid-gesture, reflowing the
// bubble and cancelling an in-progress drag-select — the exact thing hover
// affordances are expected not to do. `select-none` is scoped to this row so
// the chrome never joins a selection of the message text, and never applied to
// the message body itself.
//
// focus-within/focus-visible keep the button reachable by keyboard: tabbing to
// it makes the row visible even with no pointer anywhere near it.
export function MessageMeta({ content, createdAt }: { content: string; createdAt?: string }) {
  const timeZone = useDisplayTimeZone();
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timerRef.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(content);
    } catch {
      // No clipboard permission (or a non-secure context). Nothing actionable
      // for the user here and no state to corrupt — a silent no-op beats a
      // toast that fires on every denied attempt.
      return;
    }
    setCopied(true);
    window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => setCopied(false), COPIED_MS);
  }

  const label = copied ? "Copied" : "Copy message";
  return (
    <div
      data-testid="message-meta"
      className={cn(
        "mt-0.5 flex items-center gap-1.5 px-1 text-[10px] leading-none text-muted-2 select-none",
        "opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100",
      )}
    >
      {createdAt && <span data-testid="message-time">{formatMessageTime(createdAt, timeZone)}</span>}
      <button
        type="button"
        aria-label={label}
        title={label}
        onClick={() => void copy()}
        className="rounded p-0.5 text-muted-2 hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      >
        {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
      </button>
    </div>
  );
}
```

- [ ] **Step 4: Wire it into the bubble**

Rewrite `web/ui/src/components/chat/Bubbles.tsx`'s `ChatMessageBubble` — keep the markdown block byte-identical, wrap the bubble in a `group` column and append the footer:

```tsx
type ChatMessageBubbleProps = Pick<ChatMessage, "role" | "content"> & { createdAt?: string };

// Markdown renderer shared by every assistant/user bubble. Deliberately no
// rehype-raw — raw HTML in a message must render as inert text, not markup.
//
// `createdAt` is optional because DesignerSurface renders design-conversation
// turns through this same component and has no per-message timestamps; the
// footer then shows only the copy control.
export function ChatMessageBubble({ role, content, createdAt }: ChatMessageBubbleProps) {
  const isUser = role === "user";
  return (
    <div
      data-testid="bubble-row"
      className={cn("group flex w-full", isUser ? "justify-end" : "justify-start")}
    >
      <div className={cn("flex max-w-[75%] flex-col", isUser ? "items-end" : "items-start")}>
        <div
          className={cn(
            "rounded-2xl px-4 py-2 text-sm leading-relaxed break-words",
            isUser
              ? "bg-foreground text-background"
              : "bg-chrome text-foreground border border-border",
          )}
        >
          <div
            data-testid="message-body"
            className={cn(
              "max-w-none",
              "[&_p]:my-1 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0",
              "[&_pre]:my-2 [&_pre]:overflow-x-auto [&_code]:break-words",
              "[&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-5",
              "[&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-5",
              "[&_strong]:font-semibold [&_a]:underline",
              isUser ? "[&_a]:text-background" : "[&_a]:text-accent",
            )}
          >
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                a: ({ node: _node, ...props }) => (
                  <a {...props} target="_blank" rel="noreferrer noopener" />
                ),
              }}
            >
              {content}
            </ReactMarkdown>
          </div>
        </div>
        <MessageMeta content={content} createdAt={createdAt} />
      </div>
    </div>
  );
}
```

Add the import: `import { MessageMeta } from "./MessageMeta";`

- [ ] **Step 5: Run the new test plus every suite that renders a bubble**

Run: `cd web/ui && npx vitest run src/components/chat src/components/designer src/pages/chats`
Expected: PASS. If a designer test now finds two buttons where it expected one, narrow that test's query rather than removing the footer — the copy button is intended everywhere bubbles render.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/components/chat/MessageMeta.tsx web/ui/src/components/chat/messagemeta.test.tsx web/ui/src/components/chat/Bubbles.tsx
git commit -m "feat(web/chat): hover-revealed timestamp and copy button under each message"
```

---

### Task 4: Send button gets a send icon

**Files:**
- Modify: `web/ui/src/components/chat/Composer.tsx` (the `<button>` at the end)
- Modify: `web/ui/src/components/chat/chat.test.tsx` (append one test)

**Interfaces:**
- Consumes: nothing new.
- Produces: no API change — the button keeps the accessible name `"Send"`.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/components/chat/chat.test.tsx`:

```tsx
test("Composer: the send control is an icon button still named 'Send'", () => {
  render(<Composer onSend={vi.fn()} />);
  const btn = screen.getByRole("button", { name: "Send" });
  // An icon, not the word — but the accessible name is unchanged so every
  // existing getByRole("button", { name: "Send" }) keeps matching.
  expect(btn.textContent).toBe("");
  expect(btn.querySelector("svg")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/components/chat/chat.test.tsx`
Expected: FAIL — `btn.textContent` is `"Send"` and there is no `svg`.

- [ ] **Step 3: Write minimal implementation**

In `web/ui/src/components/chat/Composer.tsx` add `import { Send } from "lucide-react";` and replace the button:

```tsx
      <button
        type="button"
        onClick={send}
        aria-label="Send"
        title="Send"
        disabled={busy || !value.trim()}
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-lg bg-foreground text-background",
          "disabled:cursor-not-allowed disabled:opacity-40",
        )}
      >
        <Send className="size-4" />
      </button>
```

- [ ] **Step 4: Run the chat + global-chat + attachment suites**

Run: `cd web/ui && npx vitest run src/components/chat src/pages/chats`
Expected: PASS — every `getByRole("button", { name: "Send" })` still resolves via the `aria-label`.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/components/chat/Composer.tsx web/ui/src/components/chat/chat.test.tsx
git commit -m "feat(web/chat): icon send button"
```

---

### Task 5: Open a chat → auto-resume if stopped, focus the composer, stamp optimistic messages

**Files:**
- Modify: `web/ui/src/pages/chats/ChatWindow.tsx` (props/doc comment, `sendTurn`, a new effect, the bubble render)
- Modify: `web/ui/src/pages/chats/ChatsPage.tsx` (drop `createdId`, always focus)
- Modify: `web/ui/src/pages/chats/chats.test.tsx` (append tests)

**Interfaces:**
- Consumes: `useChatAction()` (already imported in `ChatWindow`), `ChatMessageBubble`'s `createdAt` prop (Task 3).
- Produces: `ChatWindow` keeps its `{ chatId, initialText?, autoFocus? }` signature — `autoFocus` stays opt-in for `GlobalChatPanel`; `ChatsPage` now always passes it.

- [ ] **Step 1: Write the failing tests**

Append to `web/ui/src/pages/chats/chats.test.tsx` (the file's `mockFetch` already dispatches `POST /api/v1/chats/:id/:action` — verify it does; if it does not, add a branch capturing the action into an exported `actionCalls: string[]` array reset in `resetFixtures`, and flip `chats` `active` accordingly):

```tsx
test("opening a stopped chat resumes it once and focuses the composer", async () => {
  mockFetch();
  const user = userEvent.setup();
  renderPage();

  await user.click(await screen.findByText("Chat Two")); // c2, active: false

  await waitFor(() => expect(actionCalls).toEqual(["c2/resume"]));
  await waitFor(() => expect(screen.getByRole("textbox", { name: /message/i })).toHaveFocus());
});

test("opening an already-active chat resumes nothing", async () => {
  mockFetch();
  const user = userEvent.setup();
  renderPage();

  await user.click(await screen.findByText("Chat One")); // c1, active: true
  await screen.findByText("hello there");

  expect(actionCalls).toEqual([]);
});

// The auto-resume is a per-open gesture, not a policy that a chat must be
// active: pressing Stop afterwards has to stick.
test("stopping a chat after an auto-resume does not re-resume it", async () => {
  mockFetch();
  const user = userEvent.setup();
  renderPage();

  await user.click(await screen.findByText("Chat Two"));
  await waitFor(() => expect(actionCalls).toEqual(["c2/resume"]));

  await user.click(await screen.findByRole("button", { name: "Stop" }));
  await waitFor(() => expect(actionCalls).toEqual(["c2/resume", "c2/stop"]));
  // No third call.
  await new Promise((r) => setTimeout(r, 50));
  expect(actionCalls).toEqual(["c2/resume", "c2/stop"]);
});
```

Use whatever `renderPage()` helper `chats.test.tsx` already defines (it renders `ChatsPage` inside `AppShell` + `MemoryRouter`); do not invent a second one. The composer textarea has no label today — if `getByRole("textbox", { name: /message/i })` does not match, query it as `screen.getByPlaceholderText("Message…")` instead.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/chats/chats.test.tsx`
Expected: FAIL — no resume call is made and the textarea is not focused.

- [ ] **Step 3: Auto-resume + timestamps in `ChatWindow`**

Replace the `autoFocus` doc comment above `ChatWindow` with:

```tsx
// autoFocus: put the caret in the composer as soon as it mounts. Opt-in rather
// than automatic because the two surfaces differ: ChatsPage passes it for every
// selection (clicking a chat there IS "I want to type"), while
// GlobalChatPanel withholds it when the slide-over merely re-opens on the last
// conversation, which is not a typing gesture and would pop the on-screen
// keyboard on a touch device.
```

Add the auto-resume, immediately after the `const [dragOver, setDragOver] = useState(false);` line:

```tsx
  // Opening a stopped chat resumes it — the "Stopped" chip is presentational
  // (the send endpoint never checks it), so making the user find and press
  // Resume was pure friction.
  //
  // The ref, not `chat.active`, is what makes this once-per-open: ChatWindow is
  // keyed by chatId at every call site, so the ref resets only when a DIFFERENT
  // chat is opened. Pressing Stop afterwards therefore sticks instead of being
  // instantly undone. GlobalChatPanel is unaffected — it only ever mounts this
  // for a chat it already filtered as active, or one it just created.
  const autoResumedRef = useRef(false);
  useEffect(() => {
    if (autoResumedRef.current) return;
    if (!data || data.chat.active) return;
    autoResumedRef.current = true;
    action.mutate({ id: chatId, action: "resume" });
    // `action` is a stable mutation object; re-running on its identity would
    // risk a second fire, and the ref above is the real guard either way.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, chatId]);
```

In `sendTurn`, stamp both optimistic pushes so a just-sent bubble shows a time immediately instead of waiting for the refetch (`reconcilePending` keys on `role::content` only, so dedupe is unaffected):

```tsx
    setPending((p) => [...p, { role: "user", content: text, created_at: new Date().toISOString() }]);
```

```tsx
      setPending((p) => [...p, { role: "assistant", content: response, created_at: new Date().toISOString() }]);
```

And pass the timestamp through to the bubble:

```tsx
        {allMessages.map((m, i) => (
          <ChatMessageBubble key={i} role={m.role} content={m.content} createdAt={m.created_at} />
        ))}
```

- [ ] **Step 4: Always focus on selection in `ChatsPage`**

Remove the `createdId` state, its comment, and the `handleNew` line that sets it; render the window as:

```tsx
      {selected ? (
        // key={selected}: ChatWindow remounts per chat, which is what lets the
        // mount-time autoFocus fire — and what scopes ChatWindow's
        // once-per-open auto-resume to a single chat.
        //
        // autoFocus is unconditional here: on this page every way of arriving
        // at a chat (clicking the list, a ⌘K search hit, a deep link) means the
        // user came to type. An earlier version withheld it while merely
        // browsing history; that distinction was removed deliberately.
        <ChatWindow chatId={selected} key={selected} autoFocus />
      ) : (
        <ChatsEmptyState />
      )}
```

`handleNew` becomes:

```tsx
  async function handleNew() {
    const chat = await createChat.mutateAsync(undefined);
    setParams({ chat: chat.id });
  }
```

Drop the now-unused `useState` import only if nothing else in the file uses it.

- [ ] **Step 5: Run the chat page suites**

Run: `cd web/ui && npx vitest run src/pages/chats src/components/chat`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/chats/ChatWindow.tsx web/ui/src/pages/chats/ChatsPage.tsx web/ui/src/pages/chats/chats.test.tsx
git commit -m "feat(web/chat): auto-resume a stopped chat on open and focus the composer"
```

---

### Task 6: KB — the new-note intent suppresses the resume-last-note auto-open

**Files:**
- Modify: `web/ui/src/pages/kb/KBPage.tsx` (the two effects, lines ~184-219)
- Modify: `web/ui/src/pages/kb/kbpage.test.tsx` (append a test)

**Interfaces:**
- Consumes: nothing new.
- Produces: no exported API change.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/kbpage.test.tsx`:

```tsx
// The real timing: useRecentFiles waits for the session query's workspace id,
// so the recents list arrives a tick AFTER first render — which is why the
// auto-open used to win the race against the ?new=note intent and open an
// unrelated note behind the dialog. When that recents entry was stale the
// editor rendered "Couldn't load this note.", the reported symptom.
test("landing on /kb?new=note does not auto-open the last recent note", async () => {
  localStorage.setItem(
    "sa.kb.recent.w1",
    JSON.stringify([{ path: "notes/stale.md", title: "stale" }]),
  );
  const noteFetches: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/auth/session") {
        return Promise.resolve(
          jsonResponse({
            authenticated: true,
            owner: { id: "o1", username: "admin", must_change_password: false },
            workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
            workspaces: [],
          }),
        );
      }
      if (url.startsWith("/api/v1/kb/note")) {
        noteFetches.push(url);
        return Promise.resolve(
          new Response(JSON.stringify({ error: { code: "not_found", message: "gone" } }), { status: 404 }),
        );
      }
      if (url.startsWith("/api/v1/kb/tree")) return Promise.resolve(jsonResponse({ path: "", nodes: [], order: [] }));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderInShell("/kb?new=note");

  expect(await screen.findByRole("dialog")).toBeInTheDocument();
  // Give the recents effect every chance to fire.
  await new Promise((r) => setTimeout(r, 50));
  expect(noteFetches).toHaveLength(0);
  expect(screen.queryByText(/couldn't load this note/i)).not.toBeInTheDocument();

  localStorage.clear();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/kbpage.test.tsx`
Expected: FAIL — one `/api/v1/kb/note?path=notes/stale.md` fetch and the error screen.

- [ ] **Step 3: Write minimal implementation**

In `web/ui/src/pages/kb/KBPage.tsx`, add the latch ref next to the other hooks and consume it in both effects:

```tsx
  // Latched, not read from the URL: the effect below strips `new` from the
  // query string on the very next tick, but the recents list (which the
  // auto-open depends on) only arrives AFTER that — a `params.get("new")`
  // check would stop suppressing exactly one render too early. Creating a note
  // is not resuming one, so once the create intent is seen, this visit never
  // auto-opens.
  const suppressResumeRef = useRef(false);
```

```tsx
  const wantsNewNote = params.get("new") === "note";
  useEffect(() => {
    if (!wantsNewNote) return;
    suppressResumeRef.current = true;
    setNewNoteOpen(true);
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("new");
        return next;
      },
      { replace: true },
    );
  }, [wantsNewNote, setParams]);
```

```tsx
  const topRecent = recent.length > 0 ? recent[0] : null;
  useEffect(() => {
    if (suppressResumeRef.current) return;
    if (path === null && topRecent) {
      setParams({ path: topRecent.path }, { replace: true });
    }
  }, [path, topRecent, setParams]);
```

`useRef` is already imported in this file.

- [ ] **Step 4: Run the KB page suite**

Run: `cd web/ui && npx vitest run src/pages/kb/kbpage.test.tsx`
Expected: PASS, including the pre-existing "landing on /kb?new=note opens the new-note dialog" test.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/kb/KBPage.tsx web/ui/src/pages/kb/kbpage.test.tsx
git commit -m "fix(web/kb): don't auto-open the last recent note when arriving to create one"
```

---

### Task 7: KB — the new-entry dialog stops wiping the typed name

**Files:**
- Modify: `web/ui/src/pages/kb/FileTree.tsx` (`NewEntryDialog`, lines ~208-247)
- Modify: `web/ui/src/pages/kb/tree.test.tsx` (append a test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `NewEntryDialog`'s props are unchanged in this task (`onCreated` arrives in Task 8).

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/tree.test.tsx` (reuse the file's existing render helper and fetch mock; if it has none suitable, render `NewEntryDialog` directly inside a `QueryClientProvider` — it needs no router):

```tsx
// The reset effect used to be keyed on [open, dirPath], and dirPath is derived
// from the open note's path — which changes underneath an already-open dialog.
// The user's typed name was cleared mid-typing and Create then silently no-oped
// on `if (!n) return`.
test("NewEntryDialog keeps the typed name when dirPath changes while it is open", async () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { rerender } = render(
    <QueryClientProvider client={qc}>
      <NewEntryDialog dirPath="" kind="note" open onOpenChange={() => {}} pickLocation />
    </QueryClientProvider>,
  );
  await userEvent.type(screen.getByLabelText("Name"), "ideas");

  rerender(
    <QueryClientProvider client={qc}>
      <NewEntryDialog dirPath="notes" kind="note" open onOpenChange={() => {}} pickLocation />
    </QueryClientProvider>,
  );

  expect(screen.getByLabelText("Name")).toHaveValue("ideas");
});
```

Import `NewEntryDialog` from `./FileTree` and `QueryClient`/`QueryClientProvider` from `@tanstack/react-query` if the file does not already.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/tree.test.tsx`
Expected: FAIL — the field is `""` after the rerender.

- [ ] **Step 3: Write minimal implementation**

In `NewEntryDialog`, replace the reset effect with a transition-gated one:

```tsx
  // Reset on the OPEN TRANSITION only. Keying this on `dirPath` too (as it was)
  // meant any change to the caller's current directory while the dialog was
  // open wiped the half-typed name — and KBPage's `currentDir` does change
  // underneath an open dialog, because it is derived from the open note's path.
  // The user then pressed Create on an empty field and nothing happened.
  const wasOpenRef = useRef(false);
  useEffect(() => {
    if (open && !wasOpenRef.current) {
      setName("");
      setLocation(dirPath);
      setError("");
    }
    wasOpenRef.current = open;
  }, [open, dirPath]);
```

`useRef` must be in this file's React import.

- [ ] **Step 4: Run the tree suite**

Run: `cd web/ui && npx vitest run src/pages/kb/tree.test.tsx src/pages/kb/kbpage.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/kb/FileTree.tsx web/ui/src/pages/kb/tree.test.tsx
git commit -m "fix(web/kb): stop the new-entry dialog wiping the typed name mid-edit"
```

---

### Task 8: KB — creating a note opens it in the rich text editor

**Files:**
- Modify: `web/ui/src/pages/kb/FileTree.tsx` (`NewEntryDialog` props + `submit`; the two tree-row call sites at ~642-649)
- Modify: `web/ui/src/pages/kb/KBPage.tsx` (`KBPaneHeader` props + the pane-header dialog)
- Modify: `web/ui/src/pages/kb/kbpage.test.tsx` (append a test)

**Interfaces:**
- Consumes: `KBPage`'s existing `openPath(p: string, isDir: boolean, displayName?: string)`.
- Produces: `NewEntryDialog` gains `onCreated?: (path: string, isDir: boolean) => void`, called with the exact path created, after the mutation resolves and before the dialog closes.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/kbpage.test.tsx`:

```tsx
test("creating a note from the palette flow opens it in the rich text editor", async () => {
  const created: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/v1/auth/session") {
        return Promise.resolve(
          jsonResponse({
            authenticated: true,
            owner: { id: "o1", username: "admin", must_change_password: false },
            workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
            workspaces: [],
          }),
        );
      }
      if (url === "/api/v1/kb/new" && init?.method === "POST") {
        created.push((JSON.parse(String(init.body)) as { path: string }).path);
        return Promise.resolve(jsonResponse({ ok: true }, 201));
      }
      if (url.startsWith("/api/v1/kb/note")) {
        const p = new URL(url, "http://localhost").searchParams.get("path")!;
        return Promise.resolve(jsonResponse({ path: p, content: "# ideas\n\n", html: "", backlinks: [], kind: "markdown" }));
      }
      if (url.startsWith("/api/v1/kb/tree")) return Promise.resolve(jsonResponse({ path: "", nodes: [], order: [] }));
      if (url.startsWith("/api/v1/kb/folders")) return Promise.resolve(jsonResponse({ folders: [""] }));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup();
  renderInShell("/kb?new=note");

  await screen.findByRole("dialog");
  await user.type(screen.getByLabelText("Name"), "ideas");
  await user.click(screen.getByRole("button", { name: "Create" }));

  // The client appends .md; the server would too, so the computed path is the
  // created path and needs no extra round trip to discover.
  await waitFor(() => expect(created).toEqual(["ideas.md"]));
  // NoteEditor's title input carries the filename minus ".md".
  expect(await screen.findByDisplayValue("ideas")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/kbpage.test.tsx`
Expected: FAIL — the POST fires but no editor mounts (the empty state stays).

- [ ] **Step 3: Add the callback to `NewEntryDialog`**

In `web/ui/src/pages/kb/FileTree.tsx`:

```tsx
export function NewEntryDialog({
  dirPath,
  kind,
  open,
  onOpenChange,
  pickLocation = false,
  onCreated,
}: {
  dirPath: string;
  kind: NewKind;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pickLocation?: boolean;
  // Called with the path that was actually created, so the caller can navigate
  // to it. Optional so a call site that only wants the file to exist stays
  // unchanged.
  onCreated?: (path: string, isDir: boolean) => void;
}) {
```

and in `submit`, after a successful mutation:

```tsx
    try {
      await newNote.mutateAsync({ path, is_dir: kind === "folder" });
      // `path` IS the created path, no round trip needed: the client appends
      // ".md" when the name lacks it, and the server (apiNewKBNote) only
      // appends when the basename has no dot at all — so for a plain name both
      // produce the same result, and for a dotted name the client already
      // supplied the extension and the server writes it verbatim.
      onCreated?.(path, kind === "folder");
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
```

- [ ] **Step 4: Wire the tree-row call sites**

Still in `FileTree.tsx`, the two dialogs rendered for a folder row already sit in a component that receives the tree's `onSelect`. Pass it through:

```tsx
          <NewEntryDialog
            dirPath={node.path} kind="note"
            open={dialog === "new-note"} onOpenChange={(o) => setDialog(o ? "new-note" : null)}
            onCreated={(p, isDir) => onSelect(p, isDir)}
          />
          <NewEntryDialog
            dirPath={node.path} kind="folder"
            open={dialog === "new-folder"} onOpenChange={(o) => setDialog(o ? "new-folder" : null)}
            onCreated={(p, isDir) => onSelect(p, isDir)}
          />
```

If the enclosing component does not already have `onSelect` in scope, thread it down from `TreeLevel` (which does) as a prop named `onSelect` with the signature `(path: string, isDir: boolean, displayName?: string) => void`.

- [ ] **Step 5: Wire the pane-header call site**

In `web/ui/src/pages/kb/KBPage.tsx`, add `onCreated` to `KBPaneHeader`'s props and forward it:

```tsx
function KBPaneHeader({
  onPickFiles,
  currentDir,
  newOpen,
  setNewOpen,
  onCreated,
}: {
  onPickFiles: (files: File[]) => void;
  currentDir: string;
  newOpen: boolean;
  setNewOpen: (open: boolean) => void;
  // Navigate to whatever the dialog just created — creating a note and being
  // left on the previous screen was the whole complaint.
  onCreated: (path: string, isDir: boolean) => void;
}) {
```

```tsx
          <NewEntryDialog
            dirPath={currentDir}
            kind="note"
            open={newOpen}
            onOpenChange={setNewOpen}
            pickLocation
            onCreated={onCreated}
          />
```

and at the call site inside `KBPage`:

```tsx
          <KBPaneHeader
            onPickFiles={(files) => void importFiles(files, upload, toast, currentDir)}
            currentDir={currentDir}
            newOpen={newNoteOpen}
            setNewOpen={setNewNoteOpen}
            onCreated={openPath}
          />
```

`openPath(p, isDir)` already sets `?path=<p>` (plus `dir=1` for a folder) and records non-directories in recents, so a new `.md` lands in `NoteEditor` — the rich text editor — via `KBPage`'s existing `isMarkdown` branch.

- [ ] **Step 6: Run the KB suites and typecheck**

Run: `cd web/ui && npx vitest run src/pages/kb && npx tsc --noEmit`
Expected: PASS, no type errors.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/pages/kb/FileTree.tsx web/ui/src/pages/kb/KBPage.tsx web/ui/src/pages/kb/kbpage.test.tsx
git commit -m "feat(web/kb): open a newly created note in the editor"
```

---

### Task 9: Full verification, docs, PR

**Files:**
- Modify: `CLAUDE.md` (the Web UI routes / shell-primitives area — one sentence each for the chat footer and the session `timezone` field)

- [ ] **Step 1: Run every frontend test**

Run: `cd web/ui && npm test -- --run`
Expected: PASS. Fix any suite that broke on the shared `ChatMessageBubble` or the Send button by narrowing that suite's query, never by reverting the feature.

- [ ] **Step 2: Run every backend test**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 3: Build the SPA and the binary**

Run: `make ui && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: both succeed (this is the artifact gate — a Vite build catches things `tsc --noEmit` alone can miss in this repo's setup).

- [ ] **Step 4: Document the two contract changes**

In `CLAUDE.md`, under the web-UI section, record:

- the session payload's new `timezone` field and why it lives there rather than on `/api/v1/settings`;
- `ChatMessageBubble`'s always-mounted, opacity-revealed `MessageMeta` footer and the drag-select constraint that dictates it.

- [ ] **Step 5: Commit and open the PR**

```bash
git add CLAUDE.md
git commit -m "docs: record the session timezone field and the chat message footer"
git push -u origin worktree-chat-ux-and-kb-new-note
gh pr create --draft --title "Chat message UX + KB new-note flow" --body "$(cat <<'EOF'
## Summary
- Opening a stopped chat resumes it once and focuses the composer
- Send button is now an icon (accessible name unchanged)
- Every message bubble gets a hover-revealed timestamp (workspace timezone) + copy button, without breaking text selection
- Creating a note from the ⌘K palette now opens it in the rich text editor; fixes the stale-recent "Couldn't load this note." it used to land on, and the dialog wiping the typed name

Spec: `docs/superpowers/specs/2026-07-27-chat-ux-and-kb-new-note-design.md`
Plan: `docs/superpowers/plans/2026-07-27-chat-ux-and-kb-new-note.md`

## Test plan
- `go test ./... -count=1`
- `cd web/ui && npm test -- --run`
- `make ui && go build ./cmd/simple-agents`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review

**Spec coverage.** A1 → Task 5. A2 → Task 4. A3 → Tasks 1 (formatter), 2 (timezone transport), 3 (footer), 5 (optimistic stamps). B1 → Task 6. B2 → Task 7. B3 → Task 8. Testing section → the per-task tests plus Task 9.

**Placeholders.** None — every code step carries the literal code.

**Type consistency.** `formatMessageTime(iso, timeZone?)` is defined in Task 1 and used with that exact signature in Task 3. `useDisplayTimeZone()` is defined in Task 2 and consumed in Task 3. `MessageMeta({ content, createdAt })` is defined in Task 3 and rendered there. `ChatMessageBubble`'s `createdAt?: string` is added in Task 3 and passed in Task 5. `onCreated?: (path: string, isDir: boolean) => void` is defined and consumed in Task 8, matching `openPath(p: string, isDir: boolean, displayName?: string)`'s first two parameters.
