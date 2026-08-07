# UI and delivery fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix eleven reported defects across chat delivery, vault upload folders, the React SPA, and the knowledge-base toggle list.

**Architecture:** Five independent phases. Phase B renames an on-disk vault directory and rewrites note content, so it carries a startup migration and is sequenced alone. Every other phase is additive or a local edit.

**Tech Stack:** Go 1.x (Echo v4, modernc SQLite), React 19 + TypeScript + Tailwind v4 + TipTap/ProseMirror, vitest, Playwright.

## Global Constraints

- **Branch:** `worktree-ui-fixes-batch`. Never commit to `main`.
- **Conventional Commits** on every commit: `type(scope): summary`.
- **Gate:** `make ci` must pass before the PR (gofmt, `go vet`, `go test -race`, six-way cross-compile, `tsc -b`, oxlint, vitest, vite build).
- **No hardcoded pixel font sizes.** `web/ui/src/density.test.ts` fails the build on any `text-[<n>px]`.
- **Colour tokens only.** `--color-line` and `--color-warning` do not exist; the real tokens are `--color-border` and `--color-warn`.
- **Every action button carries a leading lucide icon.** Exceptions: dialog footer pairs, `link` variant.
- **Icons are lucide only, `currentColor`.** No emoji in button labels.
- **Neutral CommonMark** on the chat path — never platform-specific markup; adapters render on send.
- **Do not re-glue `<details><summary>`.** Separate lines is the canonical serialized form (a prior attempt was reverted).
- Working dir: `/home/rookie/rookery/.claude/worktrees/ui-fixes-batch`.

---

## Phase A — chat delivery

### Task A1: Agent identity prefix on chat notifications

**Files:**
- Create: `internal/gateway/identity.go`
- Create: `internal/gateway/identity_test.go`
- Modify: `cmd/rookery/main.go:473` (chat `/run`), `internal/scheduler/scheduler.go:121` (cron), `web/run_tracker.go:66` (web Run Now)
- Modify: `internal/agentrunner/runner.go:491` (drop the now-redundant name)

**Interfaces:**
- Produces: `gateway.AgentPrefixed(agentName, text string) string`

- [ ] **Step 1: Write the failing test** in `internal/gateway/identity_test.go`

```go
func TestAgentPrefixed(t *testing.T) {
	got := AgentPrefixed("weather", "25°C, clear sky")
	want := "🤖 **weather**\n\n25°C, clear sky"
	if got != want {
		t.Fatalf("AgentPrefixed = %q, want %q", got, want)
	}
	if AgentPrefixed("", "body") != "body" {
		t.Fatal("an empty agent name must not add a prefix")
	}
	if got := AgentPrefixed("a", "  "); got != "  " {
		t.Fatalf("blank body must pass through, got %q", got)
	}
	// Must not double-prefix when handed already-prefixed text.
	once := AgentPrefixed("weather", "body")
	if AgentPrefixed("weather", once) != once {
		t.Fatal("double prefix")
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/gateway/ -run TestAgentPrefixed`
Expected: FAIL — undefined: AgentPrefixed

- [ ] **Step 3: Implement**

```go
package gateway

import "strings"

// agentPrefixMark is the emoji every agent-authored chat message leads with.
// It is FIXED, not per-agent: db.Agent has no icon column, and the thing the
// user needs to tell two agents apart is the name. Composed as neutral
// CommonMark upstream of render.For(platform) — emitting MarkdownV2 or mrkdwn
// here would break Telegram escaping.
const agentPrefixMark = "🤖"

// AgentPrefixed labels a message with the agent that produced it.
//
// Applied at the three sites where SendOutput is the real chat sender, NOT
// inside the runner: runner.go reuses SendOutput as a collector for
// child-agent recursion, and that text is fed into the PARENT's LLM prompt.
// Prefixing there would inject chat chrome into model input.
func AgentPrefixed(agentName, text string) string {
	name := strings.TrimSpace(agentName)
	if name == "" || strings.TrimSpace(text) == "" {
		return text
	}
	header := agentPrefixMark + " **" + name + "**"
	if strings.HasPrefix(text, header) {
		return text
	}
	return header + "\n\n" + text
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gateway/ -run TestAgentPrefixed`
Expected: PASS

- [ ] **Step 5: Wire the three send sites**

In `cmd/rookery/main.go`, wrap the handler's `send` before it reaches the runner. In `internal/scheduler/scheduler.go` and `web/run_tracker.go`, wrap the `sendFn`/`send` closure the same way, using the agent name already in scope. Each becomes `gateway.AgentPrefixed(agentName, msg)` inside the closure.

- [ ] **Step 6: Drop the redundant name** at `internal/agentrunner/runner.go:491`

Change `"⚠️ %s ran but produced no notification — see the run log."` to `"⚠️ Ran but produced no notification — see the run log."` (inbox rows carry `AgentName` as a column, so nothing is lost there).

- [ ] **Step 7: Add the child-collector regression test** in `internal/agentrunner/`

Assert the collector at `runner.go:653` receives text with **no** `🤖` prefix — this is the regression that is invisible in chat.

- [ ] **Step 8: Run the package tests**

Run: `go test ./internal/gateway/... ./internal/agentrunner/... ./internal/scheduler/... ./web/...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git commit -am "feat(gateway): label agent chat notifications with the agent name"
```

---

### Task A2: Silent runs report completion; failed runs stop double-posting

**Files:**
- Modify: `internal/gateway/router.go:178` (dispatch), `:539-566` (`handleRun`)
- Test: `internal/gateway/` (new cases)

- [ ] **Step 1: Write failing tests**

Three cases, using the existing gateway test harness (see `placeholder_test.go` for a fake that implements `TypingGateway`):
1. a run that emits nothing and returns nil → the "Running agent" notice is replaced by a completion line containing the agent name and the words `no notification`
2. a run that returns an error → exactly **one** message reaches the user (today there are two: the runner's friendly error, then `An error occurred: …`)
3. a run that produces output → no stray "Running agent" message is left behind

- [ ] **Step 2: Run and confirm they fail**

Run: `go test ./internal/gateway/ -run TestRun`

- [ ] **Step 3: Implement**

Change the dispatch at `router.go:178` to `return r.handleRun(ctx, msg, arg, send, sendProgress)` and extend `handleRun`'s signature. Replace the tail of `handleRun`:

```go
	// The running notice goes through sendProgress, not send, so it creates a
	// placeholder whose id is retained — letting the completion state EDIT it
	// rather than leaving "Running agent…" stranded above the answer.
	sendProgress(fmt.Sprintf("Running agent **%s**...", name))

	delivered := false
	track := func(text string) {
		delivered = true
		send(text)
	}

	if err := r.onAgentRun(ctx, msg.WorkspaceID, name, track); err != nil {
		// The runner already delivered FriendlyRunError through SendOutput.
		// Returning the error would make GatewayManager.dispatch append
		// "An error occurred: …" — the same failure, posted twice.
		if delivered {
			return nil
		}
		return err
	}
	if !delivered {
		// A [SILENT] run never reaches a SendOutput site, so without this the
		// user is left on "Running agent…" forever.
		sendProgress(fmt.Sprintf("✅ **%s** finished — no notification.", name))
	}
	return nil
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gateway/...`
Expected: PASS. Update any fake gateway in the test harness that does not implement `TypingGateway`.

- [ ] **Step 5: Commit**

```bash
git commit -am "fix(gateway): report silent agent completions and stop double-posting run errors"
```

---

## Phase B — one uploads folder (migration; sequenced alone)

### Task B1: Rename `files/` to `uploads/` with a content-rewriting migration

**Files:**
- Modify: `internal/vault/import.go:17` (`FilesDir = "uploads"`)
- Modify: `internal/vault/migrate.go` (add `MigrateFilesToUploads`)
- Modify: `cmd/rookery/main.go` (call it beside the other startup migrations, ~line 210-219)
- Test: `internal/vault/migrate_test.go`

- [ ] **Step 1: Write the failing test**

Cover, in order: the directory is renamed; `original_file: "files/x.pdf"` becomes `uploads/`; a body link `[x.pdf](files/x.pdf)` becomes `uploads/`; **a note whose prose contains the word `files/` outside those two patterns is byte-identical afterwards**; running twice changes nothing; a vault that never had `files/` is a no-op; a pre-existing `uploads/` is drained into without clobbering.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/vault/ -run TestMigrateFilesToUploads`

- [ ] **Step 3: Implement**

Model on `MigrateSessionsToChats` (`migrate.go:172`) — same `os.ReadDir(v.VaultsDir())` / per-workspace / `slog.Warn`-and-continue / idempotent shape, reusing `drainInto` (`migrate.go:221`) for the collision case. The content rewrite walks `.md` files and replaces **only** these two exact patterns:

```go
	updated := strings.ReplaceAll(string(b), `original_file: "files/`, `original_file: "uploads/`)
	updated = strings.ReplaceAll(updated, "](files/", "](uploads/")
```

A blind `files/` → `uploads/` replace would corrupt user prose and is forbidden.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/vault/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(vault): rename files/ to uploads/ and migrate existing notes"
```

---

### Task B2: Route editor asset uploads into `uploads/`, hide `assets/`

**Files:**
- Modify: `web/api_kb.go:180` (write to `uploads/`), `:566` (`apiKBTree`), `:347` (`apiKBFolders`)
- Test: `web/api_kb_test.go` (or the nearest existing KB handler test file)

- [ ] **Step 1: Write the failing test**

Assert: root-level `assets` is absent from `GET /api/v1/kb/tree` and from the folder list; **`skills/<id>/assets` is still present** (the hide is root-level only — `skillstore.go:138` creates nested asset dirs); `uploads` is present in both.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./web/ -run TestKBTree`

- [ ] **Step 3: Implement**

Change `path.Join("assets", assetName(...))` to `path.Join(vault.FilesDir, assetName(...))` at `api_kb.go:180`. `assetName` already appends 4 random bytes, so it cannot collide with `ImportFile`'s `uniquePath` originals.

Filter root-level `assets` in the `apiKBTree` node loop using the existing `isRoot := rel == ""` idiom (`api_kb.go:582`), and in `apiKBFolders` (`:347`) so it also stops being a move/create destination.

Existing `assets/` files are **not** moved: notes reference them as `![](assets/foo.png)` and those resolve through `/kb/raw` → `vault.Resolve`, never the tree.

- [ ] **Step 4: Run tests**

Run: `go test ./web/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(kb): store editor uploads in uploads/ and hide the legacy assets folder"
```

---

### Task B3: Display "Uploads" with a capital U

**Files:**
- Modify: `web/handlers_kb.go:39` (`kbSystemFolderLabels`)
- Test: `web/handlers_kb_title_test.go`

- [ ] **Step 1: Write the failing test**

Assert the root node for `uploads` carries `DisplayName: "Uploads"` while `Name` and `Path` stay lowercase (presentation only — `DisplayName` never feeds navigation).

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./web/ -run TestKBDisplay`

- [ ] **Step 3: Implement** — add `"uploads": "Uploads",` to the map.

Do **not** add `uploads` to `kbSystemDirs`: that sets `System: true` and would drag-lock the folder, a behaviour change nobody asked for.

- [ ] **Step 4: Run tests + commit**

```bash
go test ./web/...
git commit -am "feat(kb): label the uploads folder Uploads"
```

---

## Phase C — SPA fixes

### Task C1: Unmute Agents/Chats/Skills in the file tree

**Files:**
- Modify: `web/ui/src/pages/kb/FileTree.tsx:675`
- Test: `web/ui/src/pages/kb/tree.test.tsx`

- [ ] **Step 1: Write the failing test** — no root folder row carries `text-muted-2`, and dragging onto `agents/` is still refused.
- [ ] **Step 2: Run** `npx vitest run src/pages/kb/tree.test.tsx` — confirm failure.
- [ ] **Step 3: Implement** — delete **only** the `isEffectivelySystem(node) && "text-muted-2",` line at `:675`.

Do not touch the predicate: it also gates drag-reorder (`:509`), move-into (`:512`) and reorder persistence (`:844`).

- [ ] **Step 4: Run tests + commit**

```bash
git commit -am "fix(kb): render system folders in the same colour as Memory and Notes"
```

---

### Task C2: Remove duplicate icons from buttons

**Files:**
- Modify: `web/ui/src/pages/agents/AgentNewPage.tsx:33-34`, `pages/agents/AgentEditPage.tsx:21-22`, `pages/skills/SkillNewPage.tsx:30-31` (label constants)
- Modify: `web/ui/src/pages/setup/SetupWizard.tsx:171`, `:255`, `:418`, `:95` (BackBar)
- Modify: `web/ui/src/pages/connections/ServiceWizard.tsx:377`, `:568`
- Modify: `web/ui/src/pages/agents/edit.test.tsx:111`, `components/designer/designer.test.tsx:71`
- Create: `web/ui/src/buttonlabels.test.ts`

- [ ] **Step 1: Write the failing guard test** in `buttonlabels.test.ts` — scan SPA sources and fail on any button label string starting with an emoji or containing a trailing `→`.
- [ ] **Step 2: Run** `npx vitest run src/buttonlabels.test.ts` — confirm failure listing the known sites.
- [ ] **Step 3: Implement** — strip `🔨 `, `✅ ` and ` →` from the label strings, keeping the lucide element. Convert `BackBar`'s raw `<button>` to `Button variant="link"` so `[&_svg]:size-4` applies (today `<ArrowLeft />` renders at 24px beside 13px text).
- [ ] **Step 4: Update the two accname assertions** to `"Build it"` — the lucide `<svg>` contributes nothing to the accessible name.
- [ ] **Step 5: Run tests + commit**

```bash
npx vitest run
git commit -am "fix(ui): remove duplicate emoji icons from action buttons"
```

---

### Task C3: Keep the setup stepper on one line

**Files:**
- Modify: `web/ui/src/pages/setup/SetupWizard.tsx:821` (`max-w-xl` → `max-w-2xl`), `:54-61` (`CHIP_LABELS`)
- Test: `web/ui/src/pages/setup/SetupWizard.test.tsx`

- [ ] **Step 1: Write the failing test** — assert the label set's total character count fits the computed budget. jsdom cannot measure layout, so assert numerically: content width 608px (`max-w-2xl` 672 − `p-8` 64) minus 266px chrome (5 circles ×20, gaps 54, 4 connectors ×24, list gaps 16) leaves ≥ 300px for labels.
- [ ] **Step 2: Run** — confirm failure at `max-w-xl` (512 − 266 = 246px).
- [ ] **Step 3: Implement** — widen to `max-w-2xl` and shorten `"Master password"` to `"Password"`.
- [ ] **Step 4: Run tests + commit**

```bash
git commit -am "fix(setup): keep the wizard step tracker on a single line"
```

---

### Task C4: Fix the Congratulations buttons' navigation

**Files:**
- Modify: `web/ui/src/pages/setup/SetupWizard.tsx:805-817` (`finish`)
- Test: `web/ui/src/pages/setup/SetupWizard.test.tsx`

- [ ] **Step 1: Write the failing test**

The existing tests at `:463` and `:482` assert the right destinations and **pass against the bug**, for two independent reasons: `wrap()` mounts `SetupWizard` directly so `RequireSetupWorkspace` is absent from the tree, and `SESSION_FIXTURE` hardcodes `needs_setup: true` so nothing flips. The new test must mount the real `router` from `router.tsx` with a fixture that flips `needs_setup` to `false` after `POST {step:7}`, then assert the agent-new route renders — not home.

- [ ] **Step 2: Run** — confirm it fails by landing on home.
- [ ] **Step 3: Implement** — navigate before invalidating:

```js
await api.post("/api/v1/setup", { step: 7 });
nav(target);
void qc.invalidateQueries({ queryKey: ["session"] });
```

Awaiting the invalidation resolves only after the session refetch lands, so `needs_setup` is already `false` while `/setup` is still matched and `router.tsx:82`'s `<Navigate to="/" replace />` fires first.

- [ ] **Step 4: Run tests + commit**

```bash
git commit -am "fix(setup): navigate to the chosen destination after finishing setup"
```

---

## Phase D — owner settings

### Task D1: Typography and primitives across the five owner sections

**Files:**
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx` (all four sections), `pages/settings/BackupSection.tsx`
- Modify: `web/ui/src/pages/settings/CoderSection.tsx:379` (missing icon)
- Test: `web/ui/src/pages/settings/OwnerSections.test.tsx`, new `deadtokens.test.ts`

- [ ] **Step 1: Write the failing guard test** in `deadtokens.test.ts` — no SPA source may reference `border-line`, `divide-line`, `text-warning` or `bg-warning-soft`. These tokens do not exist; `BackupSection` is the only file using them, which is why its selects render borderless and its pending-restore banner renders as plain text.
- [ ] **Step 2: Run** — confirm failure listing `BackupSection.tsx:208`, `:267`, `:281`, `:296`, `:312`, `:495`, `:571`.
- [ ] **Step 3: Implement** — replace with `border-border`, `divide-border`, `text-warn`, `bg-warn-soft`.
- [ ] **Step 4: Adopt the shared primitives** — `PageTitle` for each section heading (these are full pages now, so the hand-rolled `OwnerIcon` + `h2` block becomes redundant, including the **four** verbatim copies at `BackupSection.tsx:151-193`); `text-sm` body copy instead of `text-xs` (owner pages currently render two points smaller than every other settings page); `Label`, `Card`, `Dialog` and `Button` in place of raw elements; a leading lucide icon on every action button, including `CoderSection.tsx:379`'s "Test".
- [ ] **Step 5: Run tests + commit**

```bash
npx vitest run && npx tsc -b
git commit -am "fix(settings): align owner sections with the design system"
```

---

### Task D2: Workspaces gains Enter and a primary action

**Files:**
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx:128-160` (`WorkspacesSection`), `:44-126` (`WorkspaceCard`)
- Test: `web/ui/src/pages/settings/OwnerSections.test.tsx`

- [ ] **Step 1: Write the failing test** — each workspace card exposes an **Enter** action, and the section renders at least one `variant="default"` button.
- [ ] **Step 2: Run** — confirm failure.
- [ ] **Step 3: Implement** — reuse the existing `EnterWorkspaceDialog` flow from `pages/Workspaces.tsx:252-263` (`POST /api/v1/workspaces/:id/enter`, with `directEnter` for `needs_setup`). Make the primary action `variant="default"`.

Do **not** add rename or permissions: no such endpoint exists and `OwnerSections.test.tsx:212` pins their absence.

- [ ] **Step 4: Run tests + commit**

```bash
git commit -am "feat(settings): let the owner enter a workspace from settings"
```

---

### Task D3: System status shows the full health report

**Files:**
- Modify: `web/api_workspaces.go:247` (`apiLoadAdminSettings`), `web/handlers_admin.go:30`
- Modify: `web/ui/src/lib/settings.ts:153-162` (types), `pages/settings/OwnerSections.tsx:170-220`
- Test: `web/api_workspaces_test.go`, `OwnerSections.test.tsx`

- [ ] **Step 1: Write the failing test** — the endpoint returns version, commit, Landlock ABI, coder mode, the four host-tool booleans and `warnings`; the section renders each, and renders a warning when `python3` is missing.
- [ ] **Step 2: Run** — confirm failure (today it returns only `sandbox_on` and `landlock_ready`, which is why the page appears to show only Landlock).
- [ ] **Step 3: Implement** — widen the response to carry `health.Report` (`internal/health/health.go:35`), including `Warnings()` (`:71`), whose prose already covers the security-relevant case: *"python3 not found — the agent-tool AST guardrail is INACTIVE"*. Render booleans only — never paths.
- [ ] **Step 4: Run tests + commit**

```bash
go test ./web/... && npx vitest run
git commit -am "feat(settings): report full system health to the owner"
```

---

## Phase E — the toggle list

### Task E1: Make the toggle collapse, with a bulleted body

**Files:**
- Modify: `web/ui/src/pages/kb/nodes/toggle.ts` (add `addNodeView`, change `setToggle`)
- Modify: `web/ui/src/pages/kb/editor.css:150-172`
- Modify: `web/ui/src/pages/kb/editor.test.ts` (new fidelity fixtures), `slash.test.ts:120`
- Modify: `scripts/verify-kb-layout.py`

- [ ] **Step 1: Write the fidelity fixtures FIRST**

Before touching `setToggle`. A body that is not a fixed point makes the first save open the note **read-only**. These five forms were verified against the real `checkFidelity` during design and all pass, so they are pinned as permanent tests:

```ts
test("a toggle with a bulleted body round-trips", () => {
  expect(checkFidelity("<details>\n<summary>Toggle</summary>\n\n-\n\n</details>\n")).toBe(true);
  expect(checkFidelity("<details>\n<summary>Toggle</summary>\n\n- First item\n\n</details>\n")).toBe(true);
  expect(checkFidelity("<details>\n<summary>Toggle</summary>\n\n- One\n- Two\n\n</details>\n")).toBe(true);
  expect(checkFidelity("<details>\n<summary>Toggle</summary>\n\n- One\n\nAfter.\n\n</details>\n")).toBe(true);
});
```

- [ ] **Step 2: Run them** — `npx vitest run src/pages/kb/editor.test.ts`. Expected: PASS immediately (they pin existing serializer behaviour).

- [ ] **Step 3: Change `setToggle`** (`toggle.ts:117`) to insert a `bulletList` with one empty `listItem > paragraph` instead of a bare paragraph. Schema-legal: `content` is `toggleSummary block+`.

- [ ] **Step 4: Add the NodeView**

Define the arrow hit-zone constant at the top of `toggle.ts`, beside the node:

```ts
// Width of the disclosure arrow's clickable zone, measured from the summary's
// left edge. Must match the `summary::before` box in editor.css: too wide and
// clicking the first character of the title collapses the toggle instead of
// placing a caret.
const ARROW_HIT_PX = 22;
```

```ts
  addNodeView() {
    return ({ editor }) => {
      const dom = document.createElement("details");
      // Editor-only state. `open` is deliberately NOT a node attribute and NOT
      // in renderHTML: tiptap-markdown's HTML fallback wrote `open=""` back
      // into the saved note, force-expanding every toggle forever. Keeping it
      // on the DOM alone means the serializer never sees it, so fidelity is
      // untouched and no transaction (and no autosave) fires on a toggle.
      dom.open = true;
      dom.addEventListener("click", (event) => {
        if (!editor.isEditable) return;
        const summary = (event.target as HTMLElement)?.closest?.("summary");
        if (!summary || !dom.contains(summary)) return;
        // Only the arrow region toggles. Clicking the title must still place a
        // caret — otherwise the summary becomes uneditable.
        const x = event.clientX - summary.getBoundingClientRect().left;
        if (x > ARROW_HIT_PX) return;
        event.preventDefault();
        dom.open = !dom.open;
      });
      return {
        dom,
        contentDOM: dom,
        // Without this, ProseMirror's DOMObserver sees the `open` attribute
        // change, marks the node dirty and re-renders it from doc state —
        // wiping `open` on every click.
        ignoreMutation: (m: MutationRecord) => m.type === "attributes",
      };
    };
  },
```

`contentDOM` must be the `<details>` itself so `<summary>` stays a direct child: the serializer stringifies the summary's DOM (`state.render(node.firstChild, …)`), so any invented wrapper markup would break every fidelity test.

- [ ] **Step 5: Update the CSS** (`editor.css:150-172`) — remove `details > *:not(summary) { display: block }`, hide the native marker (`summary::-webkit-details-marker { display: none }`, `summary { list-style: none }`) and draw the arrow with `summary::before`, rotated when `[open]`. Keep `ARROW_HIT_PX` in sync with the arrow's rendered width.

- [ ] **Step 6: Extend `slash.test.ts:120`** to assert a `bulletList` is inserted.

- [ ] **Step 7: Run the suite** — `npx vitest run src/pages/kb/`. Expected: PASS, including the five pre-existing toggle fidelity tests at `editor.test.ts:386-451`.

- [ ] **Step 8: Extend the browser harness**

jsdom has no layout engine and no `<details>` semantics — `editor.css:163` already records that nothing in vitest can assert collapse. Add to `scripts/verify-kb-layout.py`: insert a toggle, click the arrow, assert the body's height goes to 0; click again, assert it returns. This is the only check that can observe the reported bug.

- [ ] **Step 9: Commit**

```bash
git commit -am "fix(kb): make toggle lists collapsible with a bulleted body"
```

---

## Final verification

- [ ] `make ci` passes end to end.
- [ ] Deploy the branch on a non-default port and verify by hand what CI cannot: the toggle collapsing, the wizard buttons navigating, the stepper on one line, and the chat prefix rendering on a real platform.
- [ ] Report which items were verified how — automated, browser-driven, or manual.
