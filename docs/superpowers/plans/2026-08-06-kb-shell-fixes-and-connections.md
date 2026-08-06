# KB List Rendering, Shell Scroll Containment, and Connections Cleanup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix two rendering/layout bugs in the knowledge-base editor and replace the connections page's misleading instance-URL banner with per-card blocking.

**Architecture:** Three independent changes, no shared code. Task 1 adds missing CSS rules. Task 2 adds one class to the app shell root. Tasks 3–5 remove a dead API field and its banner, then teach the service tile to use per-provider preflight data the payload already carries.

**Tech Stack:** React 19 + TypeScript, Tailwind v4, TipTap 3, vitest + @testing-library/react, Go 1.x + Echo v4.

This is plan **A of two** derived from `docs/superpowers/specs/2026-08-06-kb-editor-and-connections-design.md`. It covers spec sections 1, 5 and 6 (build-order steps 1–3). Plan B (`2026-08-06-kb-editor-formatting-and-ai-actions.md`) covers sections 2, 3 and 4. The two are independent; A ships on its own.

## Global Constraints

- **Branch, never commit to `main`.** Work happens on `worktree-kb-editor-brainstorm` in the worktree `/home/rookie/rookery/.claude/worktrees/kb-editor-brainstorm`. Run every command from that directory.
- **Conventional Commits.** `type(scope): summary` — types `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **No new npm or Go dependencies.** Every change uses what is already installed.
- **Frontend commands run from `web/ui/`:** `npm run test`, `npx tsc -b`, `npx oxlint`.
- **Go commands run from the repo root:** `go test ./web/... -count=1`, `gofmt -l .`, `go vet ./...`.
- **jsdom has no layout engine and no scrolling.** A vitest suite can assert that a CSS declaration or className is present; it can never assert the layout behaviour it produces. Behavioural verification of layout belongs in `scripts/verify-kb-layout.py`, which drives a real browser.
- **`node_modules` is not installed in this worktree.** Run `npm ci` in `web/ui/` once before the first frontend task.

---

### Task 0: Install frontend dependencies

**Files:**
- Modify: none (installs `web/ui/node_modules/`)

**Interfaces:**
- Consumes: nothing
- Produces: a working `npm run test` / `npx tsc -b` in `web/ui/`

- [ ] **Step 1: Install**

```bash
cd web/ui && npm ci
```

- [ ] **Step 2: Verify the existing suite is green before changing anything**

Run: `cd web/ui && npm run test`
Expected: PASS. Record the number of passing tests — later tasks must not reduce it.

- [ ] **Step 3: Verify the Go suite is green**

Run (from the repo root): `go test ./web/... -count=1`
Expected: PASS.

Do not commit. `node_modules/` is gitignored.

---

### Task 1: Render bullet and numbered lists

The slash-menu commands and the toolbar button already produce correct `bulletList` / `orderedList` nodes and correct markdown. Tailwind v4's Preflight resets `ul, ol { list-style: none; margin: 0; padding: 0 }`, and `editor.css` never restores it — so a list renders as flat unmarked lines and looks inert. Every other markdown surface in the app (`components/chat/Bubbles.tsx`, `components/designer/SpecPanel.tsx`, `pages/skills/SkillView.tsx`) re-adds `list-disc` / `list-decimal` / `pl-5` explicitly; the editor is the one that does not.

**Files:**
- Modify: `web/ui/src/pages/kb/editor.css`
- Test: `web/ui/src/pages/kb/liststyles.test.ts` (create)

**Interfaces:**
- Consumes: `buildExtensions`, `toMarkdown` from `web/ui/src/pages/kb/editor.ts`
- Produces: nothing other tasks depend on

- [ ] **Step 1: Write the failing tests**

Create `web/ui/src/pages/kb/liststyles.test.ts`:

```ts
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { Editor } from "@tiptap/core";
import { buildExtensions, toMarkdown } from "./editor";

// jsdom has no layout engine, so "does a marker appear" is not observable
// here. What IS observable is (a) that the stylesheet declares the rules at
// all, and (b) that the COMMANDS were never the problem — they always built a
// real list and serialized real markdown. Both halves matter: the second is
// what stops the next person from "fixing" the commands.
const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "editor.css"), "utf8");

function headless(content: string) {
  return new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content,
  });
}

test("the stylesheet gives bullet lists a marker and an indent", () => {
  // Tailwind Preflight zeroes list-style/padding on ul. Without these rules a
  // bullet list is visually indistinguishable from consecutive paragraphs.
  expect(css).toMatch(/\.tiptap ul\b[^{]*\{[^}]*list-style:\s*disc/);
  expect(css).toMatch(/\.tiptap ul[\s\S]{0,200}padding-left/);
});

test("the stylesheet gives numbered lists a marker", () => {
  expect(css).toMatch(/\.tiptap ol\b[^{]*\{[^}]*list-style:\s*decimal/);
});

test("task lists stay unmarkered", () => {
  // The taskList rule is more specific than the new ul rule and must keep
  // winning — a checkbox list with a bullet next to every checkbox is wrong.
  expect(css).toMatch(
    /\.tiptap ul\[data-type="taskList"\][^{]*\{[^}]*list-style:\s*none/,
  );
});

test("toggleBulletList always produced real markdown — the bug was never the command", () => {
  const editor = headless("<p>alpha</p>");
  editor.commands.selectAll();
  editor.commands.toggleBulletList();
  expect(toMarkdown(editor)).toContain("- alpha");
  editor.destroy();
});

test("toggleOrderedList always produced real markdown", () => {
  const editor = headless("<p>alpha</p>");
  editor.commands.selectAll();
  editor.commands.toggleOrderedList();
  expect(toMarkdown(editor)).toContain("1. alpha");
  editor.destroy();
});
```

- [ ] **Step 2: Run the tests to verify the CSS ones fail**

Run: `cd web/ui && npx vitest run src/pages/kb/liststyles.test.ts`
Expected: the three stylesheet tests FAIL (no `list-style: disc` / `decimal` in `editor.css`). The two markdown tests PASS — that is the point: they prove the commands were always fine.

- [ ] **Step 3: Add the list rules**

In `web/ui/src/pages/kb/editor.css`, insert immediately **before** the existing
`.note-editor-content .tiptap ul[data-type="taskList"]` line (currently line 41), so
the more specific task-list override still reads as an override:

```css
/* Tailwind v4's Preflight resets `ul, ol { list-style: none; padding: 0 }`.
   Without restoring it here a bullet or numbered list renders as flat
   unmarked lines — which is what made the slash menu's list items look
   broken even though the commands and the saved markdown were always
   correct. The taskList rule below is more specific (it adds an attribute
   selector) and keeps winning, so checkbox lists stay unmarkered. */
.note-editor-content .tiptap ul,
.note-editor-content .tiptap ol {
  padding-left: 1.5em;
}
.note-editor-content .tiptap ul { list-style: disc; }
.note-editor-content .tiptap ol { list-style: decimal; }
.note-editor-content .tiptap ul ul { list-style: circle; }
.note-editor-content .tiptap ul ul ul { list-style: square; }
.note-editor-content .tiptap ol ol { list-style: lower-alpha; }
.note-editor-content .tiptap ol ol ol { list-style: lower-roman; }
/* `.tiptap > * + *` puts 0.75em above every sibling block; a list item's
   inner <p> would inherit that and double-space the list. */
.note-editor-content .tiptap li > p { margin-top: 0; }
.note-editor-content .tiptap li + li { margin-top: 0.25em; }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/kb/liststyles.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Run the full frontend gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS, with no fewer passing tests than Task 0 recorded.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/kb/editor.css web/ui/src/pages/kb/liststyles.test.ts
git commit -m "fix(kb): render bullet and numbered lists in the editor

Tailwind v4 Preflight resets list-style and padding on ul/ol, and
editor.css never restored them, so lists built by the slash menu and the
toolbar rendered as flat unmarked lines. The commands and the saved
markdown were always correct — tests pin both halves."
```

---

### Task 2: Contain the shell's scroll

With a long note, wheeling with the pointer over the icon rail or the context pane scrolls the whole page — the rail and file tree travel out of view by the note's full content height. Measured against a live instance: every container from the editor pane up to `<body>` is 900/900 and clips correctly, but `<html>` reports `clientHeight 900` against `scrollHeight 13425`, and `documentElement.scrollTop` moves 0 → 3200 while `main`, the `aside` and the shell all stay at 0.

The shell's `overflow: hidden` does not stop it because the shell is `position: static`, so it is absent from the escaping content's containing-block chain. Adding `relative` makes the existing `overflow: hidden` authoritative and drops `documentElement.scrollHeight` to 900.

Six candidates were measured. `html/body { overflow: hidden }` stops the user scrolling but leaves the document 13425px tall — a mask that still breaks `scrollIntoView` and anchor navigation. `#root { overflow: hidden }`, `overflow: clip` on the shell, and `main { min-height: 0 }` do not fix it at all. Only `position: relative` removes the overflow.

**Files:**
- Modify: `web/ui/src/components/shell/AppShell.tsx:84`
- Modify: `web/ui/src/pages/kb/scrollcontainment.test.tsx`
- Modify: `scripts/verify-kb-layout.py`

**Interfaces:**
- Consumes: nothing
- Produces: nothing other tasks depend on

- [ ] **Step 1: Write the failing test**

In `web/ui/src/pages/kb/scrollcontainment.test.tsx`, append:

```tsx
test("the app shell root is a containing block", () => {
  // `relative` on a flex container looks like a no-op and is exactly the kind
  // of class a cleanup removes. It is load-bearing: `overflow` only clips a
  // descendant when the clipping element is in that descendant's
  // containing-block chain, and the shell is otherwise position:static — so
  // the editor's overflowing content escaped it entirely and landed in the
  // ROOT element's scroll box. Measured before the fix: <html> clientHeight
  // 900 vs scrollHeight 13425, and wheeling over the icon rail moved
  // documentElement.scrollTop 0 -> 3200 with the rail's top 0 -> -3200.
  // With the fix, scrollHeight is 900 and nothing scrolls but the note.
  expect(src("../../components/shell/AppShell.tsx")).toMatch(
    /className="relative h-screen overflow-hidden/,
  );
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/scrollcontainment.test.tsx`
Expected: the new test FAILS; the two existing tests PASS.

- [ ] **Step 3: Add the class**

In `web/ui/src/components/shell/AppShell.tsx`, change line 84 from:

```tsx
          <div className="h-screen overflow-hidden flex flex-col md:flex-row bg-background">
```

to:

```tsx
          <div className="relative h-screen overflow-hidden flex flex-col md:flex-row bg-background">
```

Then extend the comment directly above it (currently starting `{/* overflow-hidden: the shell is a fixed-height frame`) by appending this paragraph inside the same comment block:

```
              relative is what makes that overflow-hidden actually bite. An
              overflow value clips a descendant only when the clipping element
              is in that descendant's containing-block chain; a static shell is
              not, so a long note's overflowing content escaped to the initial
              containing block and became the ROOT element's scroll box —
              measured at <html> clientHeight 900 against scrollHeight 13425.
              The visible symptom was wheeling over the icon rail scrolling the
              whole page. Setting overflow:hidden on html/body instead only
              suppresses the user's scroll and leaves the oversized document in
              place.
```

Placing `relative` FIRST in the class string keeps the existing test's regex
(`/h-screen overflow-hidden flex flex-col md:flex-row bg-background/`) matching as a
substring, so that assertion needs no edit.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/kb/scrollcontainment.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Add the browser-level case to the verification harness**

In `scripts/verify-kb-layout.py`, add this check alongside the existing ones (it uses
the same `check(name, ok, detail)` helper and the same long-note fixture the file
already opens):

```python
# 3. The root element must have nothing to scroll. jsdom cannot see this at
#    all: every container from the editor pane up to <body> measured 900/900
#    and clipped correctly, while <html> reported scrollHeight 13425 against
#    clientHeight 900. Wheeling with the pointer over the icon rail then
#    scrolled the document and carried the rail out of view.
root = page.evaluate(
    "() => ({ scrollH: document.documentElement.scrollHeight,"
    "         clientH: document.documentElement.clientHeight })"
)
check(
    "root element has no scrollable overflow",
    root["scrollH"] <= root["clientH"] + 1,
    f"documentElement scrollHeight={root['scrollH']} clientHeight={root['clientH']}",
)

rail = page.locator('nav[aria-label="Primary"]').first
box = rail.bounding_box()
page.mouse.move(box["x"] + box["width"] / 2, box["y"] + 200)
for _ in range(8):
    page.mouse.wheel(0, 400)
    page.wait_for_timeout(60)
after = page.evaluate(
    "() => ({ top: document.documentElement.scrollTop,"
    "         rail: Math.round(document.querySelector('nav[aria-label=\\\"Primary\\\"]')"
    "                 .getBoundingClientRect().top) })"
)
check(
    "wheeling over the icon rail does not scroll the shell",
    after["top"] == 0 and after["rail"] == 0,
    f"documentElement.scrollTop={after['top']} railTop={after['rail']}",
)
```

- [ ] **Step 6: Run the full frontend gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/components/shell/AppShell.tsx \
        web/ui/src/pages/kb/scrollcontainment.test.tsx \
        scripts/verify-kb-layout.py
git commit -m "fix(web): stop a long note scrolling the app shell

The shell was position:static, so its overflow:hidden never clipped the
editor's overflowing content — it escaped to the initial containing block
and became the root element's scroll box (<html> clientHeight 900 vs
scrollHeight 13425). Wheeling over the icon rail then scrolled the whole
page. Making the shell a containing block drops scrollHeight to 900."
```

**Manual verification (not CI).** Against a throwaway instance with a note longer than
the viewport: `python3 scripts/verify-kb-layout.py <sa_session-cookie> <base-url>`.
Never point it at real data.

---

### Task 3: Remove the instance-URL summary banner

The banner reads "`<base_url>` works with N of M sign-in services." M counts only OAuth-kind providers — around 34 of the 91 in `internal/connectors/providers/` — so without that qualifier it reads as though the whole catalogue is 34 services and most of it is unavailable. The majority are API-key or keyless and entirely unaffected by the instance URL. Per-provider preflight (Task 4) reports the specific problem on the specific card, which is where it is actionable.

Removing the banner leaves `summary` with no consumer, so the DTO goes with it.

**Files:**
- Modify: `web/ui/src/pages/connections/ConnectionsPage.tsx` (remove `summary` at :409 and the banner at :643-658)
- Modify: `web/ui/src/lib/connections.ts` (remove `PublicURLSummary`, drop it from `useServices`)
- Modify: `web/api_services.go` (remove `apiPublicURLSummary`, the `Summary` field, and the two counters)
- Modify: `web/api_services_preflight_test.go` (remove the summary assertions)

**Interfaces:**
- Consumes: nothing
- Produces: `useServices()` now returns `{ providers: ServiceProvider[] }` — Task 4 reads `providers` only.

- [ ] **Step 1: Write the failing test**

In `web/api_services_preflight_test.go`, add:

```go
// The summary tally was removed: it counted only OAuth providers while
// reading as a count of the whole catalogue, and per-provider preflight
// already reports the actionable problem on the card itself.
func TestServicesListHasNoSummaryField(t *testing.T) {
	s, cookies := newServicesTestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"summary"`) {
		t.Errorf("response still carries a summary field:\n%s", rec.Body.String())
	}
}
```

If `newServicesTestServer` is not the helper name used in that file, reuse whatever
helper the existing tests there call to build a server and cookies — do not add a
second one.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/ -run TestServicesListHasNoSummaryField -count=1 -v`
Expected: FAIL — the response still contains `"summary"`.

- [ ] **Step 3: Remove the Go DTO**

In `web/api_services.go`:

1. Delete the `apiPublicURLSummary` type (currently lines 50–57, including its doc comment).
2. Delete the `Summary apiPublicURLSummary \`json:"summary"\`` field from `apiServicesListResponse` (currently line 134).
3. In the list handler, delete the `oauthProviders++` and `cleanProviders++` statements (currently lines 279–281) and the declarations of both counters. Keep the surrounding `if kind == "oauth"` block — it still computes `redirectURI` and `preflight`, both of which Task 4 depends on. After the edit that block reads:

```go
		// Only OAuth providers have a redirect URI; an api_key provider never
		// leaves the browser, so emitting one would be a false instruction.
		redirectURI, preflight := "", []apiPreflightProblem{}
		if kind == "oauth" {
			// Scoped to the OAuth APPLICATION, not the service: an aliased child
			// (google_calendar → google) authenticates through the parent's app, so
			// the parent's URI is the one that must be registered. See oauthAppName.
			redirectURI = base + "/dashboard/connectors/services/callback/" + credsProvider
			preflight = toAPIPreflight(publicurl.Check(base, s.connectors.RedirectPolicy(provider)))
		}
```

4. Change the final return to:

```go
	return c.JSON(http.StatusOK, apiServicesListResponse{Providers: out})
```

5. If `base` is now unused outside the `if kind == "oauth"` block, leave it — it is still used to build `redirectURI`. If `go vet` reports any newly-unused variable, delete that variable.

- [ ] **Step 4: Remove the old summary assertions**

In `web/api_services_preflight_test.go`, delete the anonymous struct fields
`OAuthProviders int \`json:"oauth_providers"\`` and
`CleanProviders int \`json:"clean_providers"\`` (currently lines 35–36) and every
assertion that reads them. Leave every per-provider `preflight` assertion untouched.

- [ ] **Step 5: Run the Go tests**

Run: `go test ./web/... -count=1`
Expected: PASS, including `TestServicesListHasNoSummaryField`.

- [ ] **Step 6: Remove the SPA type and the banner**

In `web/ui/src/lib/connections.ts`:

1. Delete the `PublicURLSummary` type and its comment (currently lines 166–171).
2. Change `useServices` to:

```ts
export function useServices() {
  return useQuery({
    queryKey: ["services"],
    queryFn: () => api.get<{ providers: ServiceProvider[] }>("/api/v1/services"),
  });
}
```

In `web/ui/src/pages/connections/ConnectionsPage.tsx`:

3. Delete `const summary = servicesQuery.data?.summary;` (line 409).
4. Delete the whole banner block (lines 643–658) — the `{summary && …}` JSX expression
   together with the `{/* The remedy tier: … */}` comment directly above it.
5. If `Link` from `react-router` is now unused, remove it from the import on line 2.
   Check first: it may still be used elsewhere in the file.

- [ ] **Step 7: Run the frontend gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS. `tsc -b` is what catches a missed `summary` reference or a now-unused import.

- [ ] **Step 8: Commit**

```bash
git add web/api_services.go web/api_services_preflight_test.go \
        web/ui/src/lib/connections.ts \
        web/ui/src/pages/connections/ConnectionsPage.tsx
git commit -m "fix(web/connections): drop the misleading instance-URL summary

The banner counted only OAuth providers (~34 of 91) while reading as a
count of the whole catalogue, so it implied most services were
unavailable when the majority are API-key or keyless and unaffected by
the instance URL. Per-provider preflight reports the actionable problem
on the card itself. Removes the now-unconsumed summary DTO with it."
```

---

### Task 4: Block the service tiles that cannot work

`Preflight []apiPreflightProblem` already ships on every provider in the list payload, and `ServiceWizard.tsx:201` already derives `hardBlocked` from it to disable the Connect button. The tile ignores it, so a user learns a service cannot work only after picking it. This task moves that signal onto the tile.

Three rules, each load-bearing:

- **Only `severity === "hard"` blocks.** `SeverityHard` (`internal/publicurl/policy.go:26`) is produced only for a provably fatal condition — a raw IP, an RFC-reserved host suffix, plain `http` on a public domain — and only when a provider's policy is marked `verified: true`. `SeveritySoft` (including `unverified_host`, which a PSL-private host like `github.io` produces) must stay a warning.
- **A provider with existing connections is never blocked.** Those connections still work and the user must reach the wizard to inspect or delete them.
- **`aria-disabled`, not `disabled`.** A `disabled` button fires no click event, and the click is the entire feature.

**Files:**
- Modify: `web/ui/src/pages/connections/ConnectionsPage.tsx` (`ServiceTile` at :173-204, plus the render site at :692 and new dialog state)
- Test: `web/ui/src/pages/connections/connections.test.tsx`

**Interfaces:**
- Consumes: `ServiceProvider.preflight: PreflightProblem[]` and `ServiceProvider.connections` from `web/ui/src/lib/connections.ts`
- Produces: `isServiceBlocked(provider: ServiceProvider): boolean` — exported from `ConnectionsPage.tsx` for direct unit testing.

- [ ] **Step 1: Write the failing tests**

In `web/ui/src/pages/connections/connections.test.tsx`, add. The file already has
`providers` as a mutable fixture and a render helper; reuse them rather than adding
new ones, and match the existing fixture shape for `ServiceProvider`.

```tsx
import { isServiceBlocked } from "./ConnectionsPage";

const HARD = { severity: "hard", code: "scheme_not_https",
  message: "Plain http is rejected by this provider.",
  fix: "Serve the instance over https." };
const SOFT = { severity: "soft", code: "unverified_host",
  message: "This host has not been verified.", fix: "" };

test("a hard preflight problem blocks a provider with no connections", () => {
  expect(isServiceBlocked({ preflight: [HARD], connections: [] } as never)).toBe(true);
});

test("a soft preflight problem never blocks", () => {
  // unverified_host is soft precisely so a stale policy cannot lock anyone out.
  expect(isServiceBlocked({ preflight: [SOFT], connections: [] } as never)).toBe(false);
});

test("a provider with existing connections is never blocked", () => {
  // Those connections still work; the wizard is the only way to inspect or
  // delete them, so the tile has to stay reachable.
  expect(
    isServiceBlocked({ preflight: [HARD], connections: [{ id: "c1" }] } as never),
  ).toBe(false);
});

test("a blocked tile explains itself instead of opening the wizard", async () => {
  const user = userEvent.setup();
  providers = [{ ...baseProvider, name: "google", label: "Google",
    kind: "oauth", preflight: [HARD], connections: [] }];
  renderConnectionsPage();

  const tile = await screen.findByRole("button", { name: /Google/ });
  expect(tile).toHaveAttribute("aria-disabled", "true");
  await user.click(tile);

  // The dialog quotes the API's own strings — one wording, not two.
  expect(await screen.findByText(/Plain http is rejected/)).toBeInTheDocument();
  expect(screen.getByText(/Serve the instance over https/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /Change the instance URL/ })).toBeInTheDocument();
});

test("Open anyway reaches the wizard from a blocked tile", async () => {
  // The hard block predicts a third party's rules rather than an invariant we
  // own, so a stale redirect_policy entry must never become a lockout.
  const user = userEvent.setup();
  providers = [{ ...baseProvider, name: "google", label: "Google",
    kind: "oauth", preflight: [HARD], connections: [] }];
  renderConnectionsPage();

  await user.click(await screen.findByRole("button", { name: /Google/ }));
  await user.click(await screen.findByRole("button", { name: /Open anyway/ }));
  expect(await screen.findByText(/Connect Google|Google/)).toBeInTheDocument();
});

test("an unblocked tile opens the wizard directly", async () => {
  const user = userEvent.setup();
  providers = [{ ...baseProvider, name: "github", label: "GitHub",
    kind: "oauth", preflight: [], connections: [] }];
  renderConnectionsPage();

  await user.click(await screen.findByRole("button", { name: /GitHub/ }));
  expect(screen.queryByRole("button", { name: /Open anyway/ })).not.toBeInTheDocument();
});
```

Adapt `baseProvider` / `renderConnectionsPage` to the names the existing file already
uses. If the file builds its provider fixtures inline, follow that instead — do not
introduce a competing fixture style.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/connections/connections.test.tsx`
Expected: FAIL — `isServiceBlocked` is not exported.

- [ ] **Step 3: Add the predicate and the blocked tile**

In `web/ui/src/pages/connections/ConnectionsPage.tsx`, add above `ServiceTile`:

```tsx
// A provider is blocked when the instance URL provably cannot complete its
// OAuth flow. Only `hard` counts: `soft` (e.g. unverified_host, which any
// PSL-private host like github.io produces) is a warning, and treating it as
// fatal would lock users out over a policy we only PREDICT.
//
// A provider with existing connections is never blocked — those connections
// still work, and the wizard is the only way to inspect or delete them.
//
// Exported for direct unit testing: this predicate is the whole contract
// between the preflight payload and whether a tile is reachable.
export function isServiceBlocked(provider: ServiceProvider): boolean {
  if (provider.connections.length > 0) return false;
  return provider.preflight.some((p) => p.severity === "hard");
}
```

Replace `ServiceTile` (lines 173–204) with:

```tsx
function ServiceTile({
  provider,
  onOpen,
  onBlocked,
}: {
  provider: ServiceProvider;
  onOpen: (provider: ServiceProvider) => void;
  onBlocked: (provider: ServiceProvider) => void;
}) {
  const count = provider.connections.length;
  const needsReauth = provider.connections.some((c) => c.status !== "ACTIVE");
  const blocked = isServiceBlocked(provider);

  return (
    <button
      type="button"
      // aria-disabled, NOT disabled: a disabled button fires no click, and the
      // click is what explains why the tile is blocked.
      aria-disabled={blocked || undefined}
      onClick={() => (blocked ? onBlocked(provider) : onOpen(provider))}
      className={cn(
        "flex flex-col items-center gap-1.5 rounded-lg border border-border bg-background p-3 text-center transition-colors",
        blocked
          ? "opacity-60 hover:border-warn/40"
          : "hover:border-primary/40 hover:shadow-sm",
      )}
    >
      <ProviderLogo name={provider.name} size={30} />
      <div className="w-full truncate text-xs font-semibold">
        {provider.label}
      </div>
      {blocked ? (
        <div className="text-xs text-warn">Needs a public URL</div>
      ) : count === 0 ? (
        <div className="text-xs text-muted-2">Connect</div>
      ) : needsReauth ? (
        <div className="text-xs text-warn">reconnect needed</div>
      ) : (
        <div className="text-xs text-ok">
          ● {count} account{count > 1 ? "s" : ""}
        </div>
      )}
    </button>
  );
}
```

- [ ] **Step 4: Add the explain dialog**

Still in `ConnectionsPage.tsx`, add this component below `ServiceTile`:

```tsx
// Shown instead of the wizard when a tile is blocked. It quotes the API's own
// message/fix strings rather than restating them, so the tile and the wizard
// cannot drift into two different explanations of the same problem.
function BlockedServiceDialog({
  provider,
  onClose,
  onOpenAnyway,
}: {
  provider: ServiceProvider | null;
  onClose: () => void;
  onOpenAnyway: (provider: ServiceProvider) => void;
}) {
  const problem = provider?.preflight.find((p) => p.severity === "hard");
  return (
    <Dialog open={provider !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{provider?.label} needs a public instance URL</DialogTitle>
        </DialogHeader>
        <div className="space-y-3 px-6 pb-6 text-sm">
          {problem && <p className="text-foreground">{problem.message}</p>}
          {problem?.fix && <p className="text-muted-2">{problem.fix}</p>}
          <div className="flex items-center justify-end gap-2 pt-2">
            {/* Open anyway is not politeness. The hard block predicts a third
                party's rules rather than expressing an invariant we own, so a
                stale redirect_policy entry in a YAML file must never become a
                lockout with no override. Connect stays disabled inside the
                wizard regardless. */}
            <Button
              variant="ghost"
              onClick={() => provider && onOpenAnyway(provider)}
            >
              Open anyway
            </Button>
            <Button asChild>
              <Link to="/settings">
                <Settings className="size-4" />
                Change the instance URL
              </Link>
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
```

Add the imports this needs at the top of the file (merge into the existing import
blocks rather than adding duplicates):

```tsx
import { Link } from "react-router";
import { Settings } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
```

`Link` may already be imported — Task 3 step 6 removes it only if it became unused.
Re-add it here if so.

- [ ] **Step 5: Wire it into the page**

In the `ConnectionsPage` component body, next to the other `useState` calls
(around line 370):

```tsx
const [blockedProvider, setBlockedProvider] = useState<ServiceProvider | null>(null);
```

At the `ServiceTile` render site (currently line 692), add the new prop:

```tsx
                          onOpen={openServiceWizard}
                          onBlocked={setBlockedProvider}
```

And render the dialog once, as a sibling of the two sections (immediately before the
closing element of the page body):

```tsx
        <BlockedServiceDialog
          provider={blockedProvider}
          onClose={() => setBlockedProvider(null)}
          onOpenAnyway={(p) => {
            setBlockedProvider(null);
            openServiceWizard(p);
          }}
        />
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/connections/connections.test.tsx`
Expected: PASS.

- [ ] **Step 7: Run the full frontend gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/ui/src/pages/connections/ConnectionsPage.tsx \
        web/ui/src/pages/connections/connections.test.tsx
git commit -m "feat(web/connections): block service tiles the instance URL cannot serve

The list payload already carries per-provider preflight and the wizard
already disables Connect on a hard problem; the tile ignored it, so a
user only learned a service could not work after picking it. Blocked
tiles now explain themselves on click, with Open anyway preserved so a
stale redirect_policy cannot become a lockout."
```

---

### Task 5: Guard the payload contract

Task 4's behaviour depends on `preflight` being populated on the list payload for a hard-blocked provider. Until now that field was informational, so nothing failed if it went empty. This pins it.

**Files:**
- Test: `web/api_services_preflight_test.go`

**Interfaces:**
- Consumes: the `/api/v1/services` payload shape
- Produces: nothing

- [ ] **Step 1: Write the test**

```go
// The SPA now DISABLES a provider tile on a hard preflight problem, so an
// empty preflight array is no longer merely uninformative — it silently
// re-enables a tile whose OAuth flow provably cannot complete.
func TestOAuthProviderCarriesHardPreflightOnNonPublicURL(t *testing.T) {
	s, cookies := newServicesTestServer(t)
	setInstanceURL(t, s, "http://192.168.1.194:8080") // raw private IP → SeverityHard

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Providers []struct {
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			Preflight []struct {
				Severity string `json:"severity"`
			} `json:"preflight"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var checked int
	for _, p := range got.Providers {
		if p.Kind != "oauth" {
			continue
		}
		for _, pf := range p.Preflight {
			if pf.Severity == "hard" {
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no OAuth provider reported a hard preflight problem on a raw-IP " +
			"instance URL; the SPA's tile blocking depends on this field")
	}
}
```

Use whatever helpers the file already provides for building the server and setting the
instance URL. If there is no existing helper for the instance URL, set it the same way
the file's existing preflight tests do — read them first and follow that path exactly
rather than inventing a new one.

- [ ] **Step 2: Run it**

Run: `go test ./web/ -run TestOAuthProviderCarriesHardPreflightOnNonPublicURL -count=1 -v`
Expected: PASS (this guards existing behaviour, so it should pass immediately — if it
fails, the tile blocking in Task 4 is broken and must be investigated before continuing).

- [ ] **Step 3: Run the full Go suite**

Run: `go test ./web/... -count=1 && gofmt -l . && go vet ./...`
Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add web/api_services_preflight_test.go
git commit -m "test(web): pin preflight presence now that tiles depend on it"
```

---

### Task 6: Full local gate

**Files:** none

- [ ] **Step 1: Run the complete PR gate**

Run (from the repo root): `make ci`
Expected: PASS — this mirrors the merge gate exactly, including gofmt, `go vet`,
`go test -race`, the six-target cross-compile, and the frontend build.

- [ ] **Step 2: If `make ci` fails on something this plan did not touch**

Record it and report it. Do NOT fix unrelated failures inside this branch — that
widens the change beyond its scope and puts an unrelated review on its critical path.

- [ ] **Step 3: Push and open a draft PR**

```bash
git push -u origin worktree-kb-editor-brainstorm
gh pr create --draft \
  --title "fix(web): KB list rendering, shell scroll containment, connections cleanup" \
  --body "$(cat <<'EOF'
Implements sections 1, 5 and 6 of
`docs/superpowers/specs/2026-08-06-kb-editor-and-connections-design.md`.

- **KB lists render again.** Tailwind Preflight zeroes `list-style`/padding on
  `ul`/`ol` and `editor.css` never restored them, so slash-menu lists looked
  inert. The commands and the saved markdown were always correct.
- **A long note no longer scrolls the app shell.** The shell was
  `position: static`, so its `overflow: hidden` never clipped the editor's
  overflowing content — it escaped to the initial containing block and became
  the root element's scroll box (`<html>` clientHeight 900 vs scrollHeight
  13425). Measured against a live instance; `position: relative` is the only
  one of six candidates that removes the overflow rather than masking it.
- **Connections.** Dropped the instance-URL summary banner (it counted only
  OAuth providers while reading as a count of all 91) and instead disabled the
  tiles whose OAuth flow provably cannot complete, with an overridable
  explain-on-click.

Manual verification for the scroll fix (needs a throwaway instance):
`python3 scripts/verify-kb-layout.py <cookie> <base-url>`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

**Note:** the repo's `gh` token lacks the `workflow` scope. This branch touches no
file under `.github/workflows/`, so merging is unaffected.

---

## Self-Review

**Spec coverage.** Section 1 (list rendering) → Task 1. Section 5 (connections: banner
removal → Task 3, tile blocking → Task 4, payload guard → Task 5). Section 6 (scroll
containment) → Task 2. Sections 2, 3 and 4 are deliberately out of scope; they are
Plan B. No gaps.

**Placeholders.** None. Every step names exact files and line numbers, and every code
step carries the real code. The two places that say "adapt to the existing helper"
(Task 4 step 1 fixtures, Task 5 step 1 helpers) name what to look for and forbid adding
a competing helper — they exist because inventing a second fixture style in a file that
already has one is the worse outcome.

**Type consistency.** `isServiceBlocked(provider: ServiceProvider): boolean` is defined
in Task 4 step 3 and consumed under that exact name in Task 4 step 1's tests and in
`ServiceTile`. `BlockedServiceDialog`'s props (`provider`, `onClose`, `onOpenAnyway`)
match its render site in step 5. `ServiceTile`'s new `onBlocked` prop matches the
`setBlockedProvider` setter passed at the render site. `PreflightProblem.severity` is
the existing `"hard" | "soft"` string from `lib/connections.ts`, matching
`ServiceWizard.tsx:201`.

**Ordering.** Task 3 removes `summary` before Task 4 adds the dialog, so `Link` is
touched once in each direction — Task 3 step 6.5 and Task 4 step 4 both call this out.
