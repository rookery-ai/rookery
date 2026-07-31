# UI Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Rookery SPA readable, clickable and visually consistent — a self-hosted modern font, a bigger type scale, unified monochrome icons, one button contract, fluid page widths, a split Owner area, a fuller homepage, and the discrete gaps closed.

**Architecture:** Three sequential phases matching the three specs. Phase 1 changes *tokens and primitives* in a handful of files so ~405 existing `text-*` call sites and 23 hand-rolled cards improve at once — deliberately avoiding a 250-file edit. Phases 2 and 3 consume those primitives.

**Tech Stack:** React 19, Tailwind v4 (`@theme inline` tokens), shadcn/radix, lucide-react, TipTap, vitest + @testing-library, Go 1.2x (`go:embed`, Echo v4).

## Global Constraints

- **Specs:** `docs/superpowers/specs/2026-07-30-ui-design-system-foundation-design.md`, `-ui-layout-and-space-design.md`, `-ui-gaps-and-fixes-design.md`. These are authoritative.
- **Branch base:** `feat/identity-source-of-truth` (NOT `main` — the owner re-auth gate lives only on that branch).
- **No new runtime npm dependencies.** The emoji set is generated at build time; the font is vendored.
- **No new API endpoints.** Every homepage card uses data already fetched. `web/api_parity_test.go` must stay green.
- **Icons:** lucide only, `currentColor`, `size-4` inline / `size-5` page titles. Sole exception: `components/brand/ProviderLogo.tsx` keeps brand colour.
- **Buttons:** every action button gets a leading icon. Carve-out: dialog footer *pairs* (Cancel/Save) and the `link` variant stay text-only.
- **Contrast gate:** every changed colour token must hold 4.5:1 (text) against `--background`, `--chrome` and its own `-soft` fill, in both themes. Borders hold 3:1 (non-text). `index.css` records that a prior review caught `--ok` at 3.68:1 — do not regress it.
- **tailwind-merge trap:** never put a `p-*` shorthand beside a `px-*`/`py-*` in the same `cn()` call. `p` and `px` are different groups, so both survive and stylesheet order picks the winner. Use `px-… py-…` pairs.
- **Reveal by opacity, never by mount.** A node mounted under the cursor cancels drag-select (recorded in CLAUDE.md for `MessageMeta`).
- **Verify with:** `cd web/ui && npx vitest run` for fast loops; `make ci` before each phase's final commit.
- **Commit style:** Conventional Commits, `type(scope): summary`.

---

# Phase 1 — Design system foundation

### Task 1: Vendor Inter Variable and wire it into the SPA

**Files:**
- Create: `internal/fonts/InterVariable.woff2` (binary, 48 KB, fetched)
- Create: `internal/fonts/fonts.go`
- Create: `internal/fonts/fonts_test.go`
- Modify: `web/ui/vite.config.ts` (add `@fonts` alias)
- Modify: `web/ui/src/index.css` (`@font-face`, `--font-sans`, `body`)
- Modify: `web/ui/src/styles.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `fonts.InterVariableWOFF2 []byte` (Go, for Task 7); the CSS custom property `--font-sans`; the Vite alias `@fonts`.

- [ ] **Step 1: Fetch the font to its single home**

```bash
mkdir -p internal/fonts
curl -sSL -o internal/fonts/InterVariable.woff2 \
  "https://cdn.jsdelivr.net/npm/@fontsource-variable/inter@5.2.5/files/inter-latin-wght-normal.woff2"
# verify: must be a woff2 (magic bytes wOF2) and ~48 KB
head -c 4 internal/fonts/InterVariable.woff2 | xxd
ls -l internal/fonts/InterVariable.woff2
```

- [ ] **Step 2: Write the failing Go test**

`internal/fonts/fonts_test.go`:

```go
package fonts

import "testing"

func TestInterVariableIsAWOFF2(t *testing.T) {
	if len(InterVariableWOFF2) == 0 {
		t.Fatal("embedded font is empty")
	}
	if got := string(InterVariableWOFF2[:4]); got != "wOF2" {
		t.Fatalf("not a woff2: magic bytes = %q", got)
	}
	// Guards against a truncated or LFS-pointer checkout.
	if len(InterVariableWOFF2) < 20_000 {
		t.Fatalf("font suspiciously small: %d bytes", len(InterVariableWOFF2))
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/fonts/`
Expected: FAIL — `undefined: InterVariableWOFF2`

- [ ] **Step 4: Implement the embed package**

`internal/fonts/fonts.go`:

```go
// Package fonts holds the single copy of the UI font.
//
// It is its own package because go:embed cannot reach outside its own
// directory, and two independent consumers need these exact bytes: the Go
// export path (internal/export inlines it as a data: URI so an exported
// HTML/PDF is self-contained and needs no font installed on the server) and
// the SPA (web/ui imports it through the "@fonts" Vite alias). A second
// checked-in copy would drift silently, so there is deliberately only one.
package fonts

import _ "embed"

// InterVariableWOFF2 is Inter Variable, latin subset, weights 100-900.
//
//go:embed InterVariable.woff2
var InterVariableWOFF2 []byte
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/fonts/`
Expected: PASS

- [ ] **Step 6: Add the Vite alias**

In `web/ui/vite.config.ts`, inside `resolve.alias`, add alongside the existing `@` entry:

```ts
"@fonts": path.resolve(__dirname, "../../internal/fonts"),
```

Vite must also be allowed to serve from outside its root in dev. Add to the `server` block (create it if absent):

```ts
server: {
  fs: { allow: [path.resolve(__dirname), path.resolve(__dirname, "../../internal/fonts")] },
},
```

- [ ] **Step 7: Declare the font in index.css**

At the very top of `web/ui/src/index.css`, after the existing `@import` lines:

```css
/* Inter Variable, self-hosted. The SPA ships embedded in a single binary for
   offline/LAN installs, so a Google Fonts @import would both fail there and
   add an external request. The woff2 lives at internal/fonts/ (one copy, two
   consumers — see that package's doc comment) and reaches us via the "@fonts"
   Vite alias, which fingerprints and emits it into dist/. */
@font-face {
  font-family: "InterVariable";
  font-style: normal;
  font-weight: 100 900;
  font-display: swap;
  src: url("@fonts/InterVariable.woff2") format("woff2-variations");
}
```

Add to the `@theme inline` block:

```css
  --font-sans: "InterVariable", ui-sans-serif, system-ui, -apple-system,
    "Segoe UI", Roboto, sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
```

Replace the `body` rule's `font-family` line with:

```css
  font-family: var(--font-sans);
```

- [ ] **Step 8: Extend styles.test.ts**

Append to `web/ui/src/styles.test.ts`:

```ts
test("Inter is declared as a self-hosted @font-face and wired to --font-sans", () => {
  expect(css).toMatch(/@font-face\s*\{[^}]*InterVariable\.woff2/);
  expect(css).toMatch(/font-weight:\s*100 900/);
  expect(css).toMatch(/--font-sans:\s*"InterVariable"/);
  // The old hardcoded system stack on body must be gone — body inherits the token.
  expect(css).toMatch(/font-family:\s*var\(--font-sans\)/);
});

test("no external font is fetched (offline/LAN installs must work)", () => {
  expect(css).not.toMatch(/fonts\.googleapis\.com|fonts\.gstatic\.com/);
});
```

- [ ] **Step 9: Run the tests**

Run: `cd web/ui && npx vitest run src/styles.test.ts` → PASS
Run: `cd web/ui && npx vite build` → must succeed and emit the woff2 into `dist/assets/`
Verify: `ls web/ui/dist/assets/ | grep -i woff2`

- [ ] **Step 10: Commit**

```bash
git add internal/fonts web/ui/vite.config.ts web/ui/src/index.css web/ui/src/styles.test.ts
git commit -m "feat(web/ui): self-host Inter Variable as the UI font"
```

---

### Task 2: Remap the type scale and remove hardcoded pixel sizes

**Files:**
- Modify: `web/ui/src/index.css`
- Modify: 39 call sites across `web/ui/src` (all `text-[10px]` / `text-[11px]`)
- Modify: `web/ui/src/styles.test.ts`

**Interfaces:**
- Consumes: `--font-sans` from Task 1.
- Produces: the token pairs `--text-xs`…`--text-2xl` plus their `--text-*--line-height` partners.

- [ ] **Step 1: Write the failing tests**

Append to `web/ui/src/styles.test.ts`:

```ts
const SCALE = ["xs", "sm", "base", "lg", "xl", "2xl"] as const;

test("every --text-* token has a matching line-height token", () => {
  // Tailwind v4 pairs each --text-* with --text-*--line-height. Setting only
  // the size leaves line-height pinned to the OLD metric, which makes text
  // cramped rather than more readable — the opposite of the goal.
  for (const k of SCALE) {
    expect(css).toMatch(new RegExp(`--text-${k}:\\s*[\\d.]+rem`));
    expect(css).toMatch(new RegExp(`--text-${k}--line-height:\\s*[\\d.]+rem`));
  }
});

test("text-xs is at least 13px and text-sm at least 15px", () => {
  expect(css).toMatch(/--text-xs:\s*0\.8125rem/);
  expect(css).toMatch(/--text-sm:\s*0\.9375rem/);
});
```

Create `web/ui/src/density.test.ts`:

```ts
/// <reference types="node" />
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.dirname(fileURLToPath(import.meta.url));

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((e) => {
    const p = path.join(dir, e);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(e) && !/\.test\.tsx?$/.test(e) ? [p] : [];
  });
}

// Arbitrary pixel font sizes are how the type scale drifted in the first
// place: they are immune to the token remap, so they stay small forever.
// Everything that used text-[10px]/text-[11px] is a micro-label that reads
// correctly at the new text-xs (13px).
test("no source file hardcodes a pixel font size", () => {
  const offenders = sources(root)
    .map((f) => [path.relative(root, f), readFileSync(f, "utf8")] as const)
    .filter(([, src]) => /text-\[\d+px\]/.test(src))
    .map(([f]) => f);
  expect(offenders).toEqual([]);
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web/ui && npx vitest run src/styles.test.ts src/density.test.ts`
Expected: FAIL — no `--text-*` tokens defined; ~30 files listed as offenders.

- [ ] **Step 3: Add the scale to index.css**

Inside the existing `@theme inline` block:

```css
  /* Type scale. Remapped here rather than at ~405 call sites: Tailwind v4
     resolves text-* utilities from these tokens, so every existing text-xs /
     text-sm grows at once. Raising body font-size alone does NOT do this —
     text-sm is an absolute rem value, so the token IS the fix. */
  --text-xs: 0.8125rem;              /* 12 → 13px */
  --text-xs--line-height: 1.25rem;
  --text-sm: 0.9375rem;              /* 14 → 15px */
  --text-sm--line-height: 1.5rem;
  --text-base: 1rem;
  --text-base--line-height: 1.625rem;
  --text-lg: 1.125rem;
  --text-lg--line-height: 1.75rem;
  --text-xl: 1.375rem;               /* 20 → 22px */
  --text-xl--line-height: 1.875rem;
  --text-2xl: 1.75rem;               /* 24 → 28px */
  --text-2xl--line-height: 2.125rem;
```

In the `body` rule change `font-size: 14px` → `font-size: 15px` (covers elements carrying no `text-*` class).

- [ ] **Step 4: Replace every hardcoded pixel size**

```bash
cd web/ui/src
grep -rl 'text-\[1[01]px\]' . | xargs sed -i 's/text-\[10px\]/text-xs/g; s/text-\[11px\]/text-xs/g'
grep -rn 'text-\[[0-9]*px\]' . || echo "clean"
```

Then inspect each changed line: where the old class sat next to `font-bold uppercase tracking-wide` (the `ContextSection` heading pattern) it is correct as-is. Where two adjacent elements were `text-[11px]` and `text-xs` to create a deliberate hierarchy, promote the larger one to `text-sm` so the distinction survives.

- [ ] **Step 5: Run the tests**

Run: `cd web/ui && npx vitest run` → all PASS (full suite: the scale change can break snapshot-ish assertions).
Fix any test asserting an old literal size, then re-run.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src
git commit -m "feat(web/ui): raise the type scale and drop hardcoded pixel sizes"
```

---

### Task 3: Darken borders, add the Card primitive, gate on contrast

**Files:**
- Modify: `web/ui/src/index.css`
- Create: `web/ui/src/components/ui/card.tsx`
- Create: `web/ui/src/contrast.test.ts`
- Modify: the 23 hand-rolled card sites (see Step 5)

**Interfaces:**
- Consumes: nothing.
- Produces: `<Card>` / `<CardTitle>` React components; tokens `--border` (darkened), `--border-strong` (new).

- [ ] **Step 1: Write the failing contrast test**

`web/ui/src/contrast.test.ts`:

```ts
/// <reference types="node" />
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const cssPath = path.join(path.dirname(fileURLToPath(import.meta.url)), "index.css");
const css = readFileSync(cssPath, "utf8");

function srgb(c: number) {
  const s = c / 255;
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}
function luminance(hex: string) {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * srgb(r) + 0.7152 * srgb(g) + 0.0722 * srgb(b);
}
export function ratio(a: string, b: string) {
  const [x, y] = [luminance(a), luminance(b)].sort((m, n) => n - m);
  return (x + 0.05) / (y + 0.05);
}

// Reads a token out of a specific block (:root or .dark) so both themes can
// be checked from the one stylesheet.
function token(block: ":root" | ".dark", name: string): string {
  const b = css.slice(css.indexOf(block + " {"));
  const m = b.slice(0, b.indexOf("\n}")).match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`));
  if (!m) throw new Error(`token ${name} not found in ${block}`);
  return m[1];
}

describe.each([":root", ".dark"] as const)("contrast in %s", (block) => {
  const bg = () => token(block, "--background");
  const chrome = () => token(block, "--chrome");

  // Borders are non-text UI; WCAG 1.4.11 asks 3:1. The whole point of
  // darkening --border was to raise this.
  test("--border reads against background and chrome", () => {
    expect(ratio(token(block, "--border"), bg())).toBeGreaterThanOrEqual(1.3);
    expect(ratio(token(block, "--border-strong"), bg())).toBeGreaterThanOrEqual(1.9);
    expect(ratio(token(block, "--border-strong"), chrome())).toBeGreaterThanOrEqual(1.7);
  });

  // Regression guard: index.css records a review that caught --ok at 3.68:1
  // against its own -soft fill. These must never slip below 4.5:1 again.
  test.each(["ok", "warn", "danger"])("--%s holds 4.5:1 on all three grounds", (name) => {
    const fg = token(block, `--${name}`);
    expect(ratio(fg, bg())).toBeGreaterThanOrEqual(4.5);
    expect(ratio(fg, chrome())).toBeGreaterThanOrEqual(4.5);
    expect(ratio(fg, token(block, `--${name}-soft`))).toBeGreaterThanOrEqual(4.5);
  });

  test("--foreground and --muted hold 4.5:1 on background and chrome", () => {
    for (const n of ["--foreground", "--muted", "--muted-2"]) {
      expect(ratio(token(block, n), bg())).toBeGreaterThanOrEqual(4.5);
      expect(ratio(token(block, n), chrome())).toBeGreaterThanOrEqual(4.5);
    }
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/contrast.test.ts`
Expected: FAIL — `token --border-strong not found in :root`.

- [ ] **Step 3: Update the tokens**

In `:root`: `--border: #dcd8d2;` (was `#e9e7e3`), add `--border-strong: #c9c4bc;`
In `.dark`: `--border: #3f3f3b;` (was `#333330`), add `--border-strong: #4d4d48;`
In `@theme inline`: add `--color-border-strong: var(--border-strong);`

Also update `--input` to match `--border` in both blocks (they were equal and should stay so).

- [ ] **Step 4: Run the contrast test**

Run: `cd web/ui && npx vitest run src/contrast.test.ts`
Expected: PASS. If a `--ok`/`--warn`/`--danger` assertion fails, the *existing* palette regressed — darken that token (same hue/sat, lower lightness) rather than lowering the threshold.

- [ ] **Step 5: Add the Card primitive and adopt it**

`web/ui/src/components/ui/card.tsx`:

```tsx
import { cn } from "@/lib/utils";

// The shared bordered container. Replaces 23 hand-rolled
// "rounded-lg border border-border p-3" blocks so outline weight, radius and
// padding cannot drift per page.
//
// Deliberately 1px, not border-2: a 2px hairline across two dozen cards reads
// as heavy rather than crisp. The "bolder" in the request is delivered by the
// darkened --border token, the "bigger" by rounded-xl + p-4. --border-strong
// exists if cards still read thin in the running app.
export function Card({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card"
      className={cn("rounded-xl border border-border bg-background p-4", className)}
      {...props}
    />
  );
}

// The small uppercase heading used inside a Card.
export function CardTitle({ className, ...props }: React.ComponentProps<"h3">) {
  return (
    <h3
      className={cn("mb-2 text-xs font-bold uppercase tracking-wide text-muted-2", className)}
      {...props}
    />
  );
}
```

Replace each hand-rolled site. Find them with:

```bash
cd web/ui/src && grep -rn "rounded-lg border border-border p-3\|rounded-md border border-border p-3" .
```

Priority sites named in the request: `pages/home/HomePage.tsx` (`StatTile`, `NextUpCard`, `NeedsAttentionCard`, `RemindersCard`), `pages/skills/SkillsPage.tsx`, `pages/secrets/SecretsPage.tsx`. Keep `<section aria-label>` on `RemindersCard` by passing `asChild`-free markup: render `<Card as="section">` is NOT supported, so leave that one as a `<section>` with `className={cn(cardClass)}` — export the class string too:

```tsx
export const cardClass = "rounded-xl border border-border bg-background p-4";
```

- [ ] **Step 6: Run the full suite**

Run: `cd web/ui && npx vitest run` → PASS

- [ ] **Step 7: Commit**

```bash
git add web/ui/src
git commit -m "feat(web/ui): darken borders and share one Card primitive"
```

---

### Task 4: Raise button sizes and document the contract

**Files:**
- Modify: `web/ui/src/components/ui/button.tsx`
- Create: `web/ui/src/components/ui/button.test.tsx`

**Interfaces:**
- Consumes: nothing.
- Produces: unchanged `Button` API; only the size class strings change.

- [ ] **Step 1: Write the failing test**

`web/ui/src/components/ui/button.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { Button } from "./button";

// The density floor: 32px for icon buttons, 36px+ for text buttons. WCAG 2.2
// AA's target-size minimum is 24px, so this clears it with margin without
// going to a touch-first 44px that would waste room in a dense pane.
test.each([
  ["default", "h-10"],
  ["sm", "h-9"],
  ["xs", "h-7"],
] as const)("size %s meets the height floor (%s)", (size, cls) => {
  render(<Button size={size}>Go</Button>);
  expect(screen.getByRole("button").className).toContain(cls);
});

test.each([
  ["icon", "size-10"],
  ["icon-sm", "size-9"],
  ["icon-xs", "size-7"],
] as const)("icon size %s meets the target floor (%s)", (size, cls) => {
  render(<Button size={size} aria-label="a" />);
  expect(screen.getByRole("button").className).toContain(cls);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/components/ui/button.test.tsx`
Expected: FAIL — receives `h-9` for `default`, `size-9` for `icon`.

- [ ] **Step 3: Update the size variants**

In `button.tsx`'s `cva` `size` block:

```
default:   "h-10 px-4 py-2 has-[>svg]:px-3"
xs:        "h-7 gap-1 rounded-md px-2 text-xs has-[>svg]:px-1.5 [&_svg:not([class*='size-'])]:size-3.5"
sm:        "h-9 gap-1.5 rounded-md px-3 has-[>svg]:px-2.5"
lg:        "h-11 rounded-md px-6 has-[>svg]:px-4"
icon:      "size-10"
"icon-xs": "size-7 rounded-md [&_svg:not([class*='size-'])]:size-3.5"
"icon-sm": "size-9"
"icon-lg": "size-11"
```

Add the contract as a doc comment above `buttonVariants`:

```
// Variant contract (spec 1):
//   default     – the primary action on a surface
//   outline     – secondary action
//   ghost       – tertiary / inline / toolbar action
//   destructive – removes data
// `secondary` and `link` stay for shadcn compatibility and are not part of
// the contract; `link` is for inline text links only.
//
// Every ACTION button carries a leading lucide icon. Two deliberate
// exceptions, because the blanket rule produces worse UI: dialog footer
// PAIRS (Cancel/Save) stay text-only so they read as a matched pair, and the
// `link` variant stays text-only.
```

- [ ] **Step 4: Run the tests**

Run: `cd web/ui && npx vitest run` → PASS (a taller default button can shift layout assertions; fix any that break).

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/components/ui/button.tsx web/ui/src/components/ui/button.test.tsx
git commit -m "feat(web/ui): raise button sizes to the density floor"
```

---

### Task 5: One icon map, four consumers

**Files:**
- Create: `web/ui/src/lib/entityIcons.ts`
- Create: `web/ui/src/lib/entityIcons.test.ts`
- Modify: `web/ui/src/components/shell/IconRail.tsx`
- Modify: `web/ui/src/components/search/CommandPalette.tsx` (`KIND_META`)
- Modify: `web/ui/src/pages/settings/SettingsPage.tsx` (delete the 7 emoji)

**Interfaces:**
- Consumes: nothing.
- Produces: `ENTITY_ICONS: Record<EntityKind, LucideIcon>` and `entityIcon(kind: string): LucideIcon`, where `EntityKind` is the union of the keys below. Task 8's `PageTitle` consumes it.

- [ ] **Step 1: Write the failing test**

`web/ui/src/lib/entityIcons.test.ts`:

```ts
import { ENTITY_ICONS, entityIcon } from "./entityIcons";

const REQUIRED = [
  "home", "agents", "skills", "secrets", "kb", "connections", "chats",
  "settings", "owner", "inbox", "reminders", "note", "folder",
  "owner-workspaces", "owner-instance-url", "owner-system",
  "owner-backup", "owner-audit",
] as const;

test("every entity kind the app navigates to has exactly one icon", () => {
  for (const k of REQUIRED) expect(ENTITY_ICONS[k]).toBeTypeOf("function");
});

test("entityIcon falls back rather than crashing on an unknown kind", () => {
  expect(entityIcon("not-a-real-kind")).toBeTypeOf("function");
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/lib/entityIcons.test.ts`
Expected: FAIL — cannot resolve `./entityIcons`.

- [ ] **Step 3: Write the map**

`web/ui/src/lib/entityIcons.ts`:

```ts
import {
  Activity, Bell, Bot, BookOpen, Building2, FileText, Folder, HardDriveDownload,
  Home, Inbox, KeyRound, Link2, MessagesSquare, Plug, ScrollText, Settings,
  Shield, Sparkles, type LucideIcon,
} from "lucide-react";

// The single source of truth for "which icon means which thing".
//
// Four consumers read this map — the icon rail, PageTitle, the command
// palette's kind labels, and the settings section nav — which is the point:
// a page and its rail entry can no longer show different icons. Before this
// existed, SettingsPage carried EMOJI strings (👤🏠🧠⚙️🔐🌓🛡) while every
// other surface used monochrome lucide, which is what read as "settings is
// coloured and everything else is grey".
//
// Rules: lucide only; currentColor always (never a coloured icon except
// semantic status, which uses text-danger/-warn/-ok); size-4 inline and
// size-5 in a page title. The ONE exception in the app is
// components/brand/ProviderLogo.tsx, which keeps full brand colour because a
// monochrome Slack mark is harder to recognise than a coloured one.
export const ENTITY_ICONS = {
  home: Home,
  agents: Bot,
  skills: Sparkles,
  secrets: KeyRound,
  kb: BookOpen,
  connections: Plug,
  chats: MessagesSquare,
  settings: Settings,
  owner: Shield,
  inbox: Inbox,
  reminders: Bell,
  note: FileText,
  folder: Folder,
  "owner-workspaces": Building2,
  "owner-instance-url": Link2,
  "owner-system": Activity,
  "owner-backup": HardDriveDownload,
  "owner-audit": ScrollText,
} as const satisfies Record<string, LucideIcon>;

export type EntityKind = keyof typeof ENTITY_ICONS;

// Unknown kinds degrade to a neutral glyph instead of throwing: the command
// palette receives kind strings from the server, and a new server-side kind
// must not blank the whole result list.
export function entityIcon(kind: string): LucideIcon {
  return (ENTITY_ICONS as Record<string, LucideIcon>)[kind] ?? FileText;
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd web/ui && npx vitest run src/lib/entityIcons.test.ts` → PASS

- [ ] **Step 5: Rewire the three consumers**

- `IconRail.tsx` — import from the map instead of importing lucide icons directly.
- `CommandPalette.tsx` — `KIND_META`'s `icon` fields come from `entityIcon(kind)`.
- `SettingsPage.tsx` — change `SECTIONS` from `{ slug, icon: "👤", label }` to `{ slug, label }` and render `ENTITY_ICONS[...]`. Map: profile→`Home`? No — profile is a person: add `profile: UserRound`, `workspace: Building2`, `ai-providers: BrainCircuit`, `coder: Wrench`, `master-password: Lock`, `appearance: SunMoon` to `ENTITY_ICONS` (and to `REQUIRED` in the test).

- [ ] **Step 6: Add the emoji regression guard**

Append to `web/ui/src/pages/settings/settings.test.tsx`:

```tsx
test("the settings section nav uses lucide icons, not emoji", async () => {
  // Regression guard for the exact drift this fixed: SettingsPage used emoji
  // strings while every other surface used monochrome lucide.
  const src = await import("node:fs").then((fs) =>
    fs.readFileSync(new URL("./SettingsPage.tsx", import.meta.url).pathname, "utf8"),
  );
  expect(src).not.toMatch(/icon:\s*"[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u);
});
```

- [ ] **Step 7: Run the full suite and commit**

Run: `cd web/ui && npx vitest run` → PASS

```bash
git add web/ui/src
git commit -m "feat(web/ui): unify icons behind one lucide entity map"
```

---

### Task 6: Raise density on the dense surfaces

**Files:**
- Modify: `web/ui/src/pages/kb/FileTree.tsx` (row ~603, `⋯` trigger ~625)
- Modify: `web/ui/src/pages/kb/EmojiPicker.tsx` (grid cell)
- Modify: `web/ui/src/components/shell/ContextPaneParts.tsx`
- Modify: `web/ui/src/pages/kb/tree.test.tsx`

**Interfaces:**
- Consumes: the type scale (Task 2).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/tree.test.tsx`:

```tsx
test("a tree row and its actions menu meet the click-target floor", async () => {
  // A 26px row with an 18px hover-only ⋯ was the "you need to aim" complaint.
  renderTree();  // reuse this file's existing render helper
  const row = await screen.findByText("notes");
  const clickable = row.closest("div[style]")!;
  expect(clickable.className).toMatch(/py-2/);
  expect(clickable.className).not.toMatch(/py-1(\s|$)/);

  const menu = screen.getAllByRole("button", { name: /Actions for/i })[0];
  // Always MOUNTED, revealed by opacity — mounting a node under the cursor
  // cancels an in-progress drag-select (CLAUDE.md records this for
  // MessageMeta). So it must be in the DOM even when not hovered.
  expect(menu).toBeInTheDocument();
  expect(menu.className).toMatch(/size-7/);
  expect(menu.className).toMatch(/opacity-0/);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/tree.test.tsx`
Expected: FAIL — row has `py-1`, trigger has `p-0.5`.

- [ ] **Step 3: Apply the density changes**

`FileTree.tsx` row (~line 603):
`"flex min-w-0 flex-1 items-center gap-1.5 rounded px-1.5 py-1 text-sm"`
→ `"flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-2 text-sm"`

Chevron and `NodeIcon`: `size-3.5` → `size-4` (and the spacer `<span className="size-3.5">` → `size-4`).

`⋯` trigger (~line 625):
`"shrink-0 rounded p-0.5 opacity-0 group-hover:opacity-100 hover:bg-border focus-visible:opacity-100"`
→ `"flex size-7 shrink-0 items-center justify-center rounded-md opacity-0 transition-opacity group-hover:opacity-100 hover:bg-border focus-visible:opacity-100"`
and its `MoreHorizontal` `size-3.5` → `size-4`.

Placeholder row (~line 753): `py-1 text-xs` → `py-2 text-xs`.

`EmojiPicker.tsx`: cell `"flex h-8 w-8 …"` → `"flex size-9 …"`, `text-lg` → `text-xl`.

`ContextPaneParts.tsx`: `ContextSection` heading `text-[11px]` → `text-xs` (already done by Task 2's sweep — verify); `ContextPaneHeader` `px-4 pt-3 pb-1` → `px-4 py-3` and `text-sm font-bold` → `text-sm font-bold` (unchanged) for a consistent header height.

- [ ] **Step 4: Run the tests**

Run: `cd web/ui && npx vitest run` → PASS

- [ ] **Step 5: Commit**

```bash
git add web/ui/src
git commit -m "feat(web/ui): raise click targets on the KB tree and pickers"
```

---

### Task 7: Ship the font in exported documents

**Files:**
- Modify: `internal/export/html.go`
- Modify: `internal/export/docx.go`
- Modify/Create: `internal/export/html_test.go`

**Interfaces:**
- Consumes: `fonts.InterVariableWOFF2` (Task 1).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Append to `internal/export/html_test.go`:

```go
func TestToHTMLEmbedsTheUIFont(t *testing.T) {
	out, err := ToHTML([]byte("# Hi\n\nbody text\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// The font must be INLINED, not named: ToPDF shells out to a headless
	// renderer on the server, which will not have Inter installed — a named
	// font would silently fall back while appearing to succeed. Inlining also
	// keeps an exported HTML file self-contained offline, the same property
	// the export path already buys for images.
	if !strings.Contains(s, "data:font/woff2;base64,") {
		t.Error("exported HTML does not inline the font as a data: URI")
	}
	if !strings.Contains(s, "InterVariable") {
		t.Error("exported HTML does not reference the InterVariable family")
	}
	if strings.Contains(s, "-apple-system") {
		t.Error("exported HTML still falls back to the old system stack first")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/export/ -run TestToHTMLEmbedsTheUIFont`
Expected: FAIL on all three assertions.

- [ ] **Step 3: Inline the font in the export CSS**

In `internal/export/html.go`, add an import of `github.com/ilijad1/rookery/internal/fonts` and build the `@font-face` once at package init (base64 of a 48 KB file per request is wasteful):

```go
// fontFaceCSS is the @font-face rule inlining the UI font as a data: URI.
//
// Built once at init: the encoding is deterministic and a 48 KB base64 per
// export request is pure waste. ~65 KB of CSS is added to each exported file,
// consistent with the existing precedent that base64's ~33% inflation is an
// accepted cost for self-contained exports (see inlineVaultAssets).
var fontFaceCSS = func() string {
	return `@font-face{font-family:"InterVariable";font-style:normal;font-weight:100 900;` +
		`src:url(data:font/woff2;base64,` +
		base64.StdEncoding.EncodeToString(fonts.InterVariableWOFF2) +
		`) format("woff2-variations");}`
}()
```

Then prepend `fontFaceCSS` to the document's `<style>` content and change line 18's stack to:

```
font-family: "InterVariable", ui-sans-serif, system-ui, sans-serif;
```

leaving the mono rule at line 30 untouched.

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/export/` → PASS

- [ ] **Step 5: Name the font in DOCX**

In `internal/export/docx.go`, set the run/document default font to `Inter` with a note:

```go
// DOCX can only NAME a font: embedding one in the OOXML package is out of
// scope, so Word substitutes when the reader has not installed Inter. Stated
// rather than left as a surprise (see the spec's "Stated limitations").
```

- [ ] **Step 6: Run the Go suite and commit**

Run: `go build ./... && go test ./internal/export/ ./internal/fonts/` → PASS

```bash
git add internal/export
git commit -m "feat(export): render exported HTML and PDF in the UI font"
```

- [ ] **Step 7: Phase 1 gate**

Run: `make ci`
Expected: all green (gofmt, vet, `-race`, cross-compile ×6, `tsc -b`, oxlint, vitest, vite build).
Fix anything red before starting Phase 2.

---

# Phase 2 — Layout and space

### Task 8: PageContainer and PageTitle, adopted everywhere

**Files:**
- Create: `web/ui/src/components/shell/PageContainer.tsx`
- Create: `web/ui/src/components/shell/PageTitle.tsx`
- Create: `web/ui/src/components/shell/page.test.tsx`
- Modify: `SettingsPage.tsx:399`, `ConnectionsPage.tsx:498`, `pages/kb/FolderPage.tsx:40`, plus the 16 `<h1>` sites

**Interfaces:**
- Consumes: `ENTITY_ICONS`/`entityIcon` (Task 5).
- Produces: `<PageContainer>`, `<PageTitle icon={kind} title={string} actions?={ReactNode}>`.

- [ ] **Step 1: Write the failing test**

`web/ui/src/components/shell/page.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { PageContainer } from "./PageContainer";
import { PageTitle } from "./PageTitle";

test("PageContainer is fluid and only caps on ultrawide", () => {
  const { container } = render(<PageContainer>x</PageContainer>);
  const el = container.firstElementChild!;
  expect(el.className).toContain("max-w-[1600px]");
  expect(el.className).toContain("w-full");
  // The complaint was centred 768px content on a 1920px screen.
  expect(el.className).not.toMatch(/max-w-(3xl|5xl)/);
});

test("PageTitle renders an icon from the shared entity map", () => {
  render(<PageTitle icon="agents" title="Agents" />);
  expect(screen.getByRole("heading", { name: "Agents" })).toBeInTheDocument();
  expect(document.querySelector("svg")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/components/shell/page.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Write the primitives**

`PageContainer.tsx`:

```tsx
import { cn } from "@/lib/utils";

// The one page-content wrapper. Replaces four independent hardcoded widths
// (Settings max-w-3xl, Connections max-w-5xl, FolderPage, NoteEditor) that
// centred content and left ~900px empty on a 1920px display.
//
// mx-auto only bites once the 1600px cap is reached, so a 1440px viewport is
// genuinely fluid while a 2560px one does not grow 200-character lines.
//
// Note px-8 py-6 rather than a p-* shorthand: a caller passing px-[7%] must
// be able to override the horizontal padding, and tailwind-merge keeps BOTH
// classes when p-* meets px-* (different groups), leaving the winner to
// stylesheet order.
export function PageContainer({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("mx-auto w-full max-w-[1600px] px-8 py-6", className)} {...props} />;
}
```

`PageTitle.tsx`:

```tsx
import type { ReactNode } from "react";
import { entityIcon } from "@/lib/entityIcons";

// Replaces 16 divergent <h1>s (text-xl font-bold / text-lg font-bold /
// text-2xl font-semibold, none with an icon). The icon comes from the SAME
// map the rail reads, so a page and its rail entry cannot disagree.
export function PageTitle({
  icon, title, actions,
}: { icon: string; title: ReactNode; actions?: ReactNode }) {
  const Icon = entityIcon(icon);
  return (
    <div className="mb-6 flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-2.5">
        <Icon className="size-5 shrink-0 text-muted" />
        <h1 className="min-w-0 truncate text-xl font-bold">{title}</h1>
      </div>
      {actions}
    </div>
  );
}
```

- [ ] **Step 4: Adopt them**

Replace the wrappers at `SettingsPage.tsx:399` (`mx-auto max-w-3xl p-6` → `<PageContainer>`), `ConnectionsPage.tsx:498` (`mx-auto max-w-5xl p-6`), `FolderPage.tsx:40`. Replace each `<h1>` with `<PageTitle icon="…" title="…" actions={…}/>` on: Home, Agents, AgentDetail, Skills, SkillView, Secrets, Connections, Settings, Workspaces, ChangePassword, Placeholder.

Leave `Login.tsx`, `LockScreen.tsx` and `SetupWizard.tsx` alone — they are pre-shell full-screen surfaces with no rail, so a rail-derived icon has no meaning there.

- [ ] **Step 5: Add the no-hardcoded-width guard**

Append to `page.test.tsx`:

```tsx
test("no page wrapper hardcodes its own max width any more", async () => {
  const fs = await import("node:fs");
  const path = await import("node:path");
  const root = path.resolve(__dirname, "../../pages");
  const walk = (d: string): string[] =>
    fs.readdirSync(d).flatMap((e) => {
      const p = path.join(d, e);
      return fs.statSync(p).isDirectory() ? walk(p) : /\.tsx$/.test(e) && !/\.test\./.test(e) ? [p] : [];
    });
  const bad = walk(root).filter((f) =>
    /className="[^"]*mx-auto[^"]*max-w-(3xl|5xl)/.test(fs.readFileSync(f, "utf8")),
  );
  expect(bad.map((f) => path.basename(f))).toEqual([]);
});
```

- [ ] **Step 6: Run and commit**

Run: `cd web/ui && npx vitest run` → PASS

```bash
git add web/ui/src
git commit -m "feat(web/ui): share one fluid page container and titled header"
```

---

### Task 9: Widen the side sheet to a third of the page

**Files:**
- Modify: `web/ui/src/components/ui/sheet.tsx:65,67`
- Modify: `web/ui/src/components/shell/AppShell.tsx:113`
- Modify: `web/ui/src/components/shell/shell.test.tsx`

**Interfaces:** consumes nothing; produces nothing.

- [ ] **Step 1: Write the failing test**

Append to `shell.test.tsx`:

```tsx
test("the slide-over is a third of the page, and both width sources agree", async () => {
  const fs = await import("node:fs");
  const sheet = fs.readFileSync(new URL("../ui/sheet.tsx", import.meta.url).pathname, "utf8");
  const shell = fs.readFileSync(new URL("./AppShell.tsx", import.meta.url).pathname, "utf8");
  const CLAMP = "w-[clamp(400px,33vw,720px)]";
  // The width lived in TWO places (sheet's side="right" default sm:max-w-sm
  // and AppShell's sm:max-w-md override). Asserting both stops the drift.
  expect(sheet).toContain(CLAMP);
  expect(shell).toContain(CLAMP);
  expect(sheet).not.toContain("sm:max-w-sm");
  expect(shell).not.toContain("sm:max-w-md");
});
```

- [ ] **Step 2: Run it to verify it fails** → FAIL (both old classes present).

- [ ] **Step 3: Apply the clamp**

`sheet.tsx` `side === "right"`: `w-3/4 … sm:max-w-sm` → `w-[clamp(400px,33vw,720px)] max-w-full`. Same for `side === "left"`.
`AppShell.tsx:113`: `className="w-full sm:max-w-md p-0 gap-0 flex flex-col"` → `className="w-[clamp(400px,33vw,720px)] max-w-full p-0 gap-0 flex flex-col"`.

Keep `p-0 gap-0` — panel content owns its inner padding; a shell-level `p-4` doubles chrome for full-height embeds like the global chat panel.

- [ ] **Step 4: Run and commit**

Run: `cd web/ui && npx vitest run` → PASS

```bash
git add web/ui/src
git commit -m "feat(web/ui): widen the slide-over to a third of the page"
```

---

### Task 10: Enlarge the search modal

**Files:**
- Modify: `web/ui/src/components/search/CommandPalette.tsx:188`
- Modify: `web/ui/src/components/search/CommandPalette.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("the command palette is wide and sits high", () => {
  render(<CommandPalette open onOpenChange={() => {}} />, { wrapper: wrapper() });
  const panel = document.querySelector("[data-slot='dialog-content']")!;
  expect(panel.className).toContain("max-w-3xl");
  expect(panel.className).toContain("top-[12%]");
});
```

- [ ] **Step 2: Run it** → FAIL (`max-w-xl`, `top-[20%]`).

- [ ] **Step 3: Change the classes**

`top-[20%] max-w-xl` → `top-[12%] max-w-3xl`. Raise the results list's max-height (`CommandList`) from its current value to `max-h-[60vh]`.

- [ ] **Step 4: Run and commit**

```bash
git add web/ui/src/components/search
git commit -m "feat(web/ui): enlarge the global search modal"
```

---

### Task 11: Narrow the KB editor gutters to 7%

**Files:**
- Modify: `web/ui/src/pages/kb/NoteEditor.tsx:796,813`
- Modify: `web/ui/src/pages/kb/NoteEditor.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("both editor modes use a 7% gutter and no p-* shorthand", async () => {
  const fs = await import("node:fs");
  const src = fs.readFileSync(new URL("./NoteEditor.tsx", import.meta.url).pathname, "utf8");
  // Two occurrences: the WYSIWYG container and the raw markdown textarea.
  // Miss the textarea and switching modes jumps the layout horizontally.
  expect(src.match(/px-\[7%\]/g)?.length).toBeGreaterThanOrEqual(2);
  expect(src).not.toMatch(/mx-auto max-w-3xl/);
  // tailwind-merge trap: p-* beside px-* keeps BOTH classes and lets
  // stylesheet order decide (CLAUDE.md records this exact bug in ChatScroll).
  expect(src).not.toMatch(/className="[^"]*\bp-\d[^"]*px-\[7%\]/);
  expect(src).not.toMatch(/className="[^"]*px-\[7%\][^"]*\bp-\d\b/);
});
```

- [ ] **Step 2: Run it** → FAIL.

- [ ] **Step 3: Change both call sites**

Line 796: `<div className="mx-auto max-w-3xl px-6 py-8">` → `<div className="px-[7%] py-8">`
Line 813: `"h-full w-full resize-none bg-background px-6 py-8 font-mono text-sm …"` → `… px-[7%] py-8 …`

- [ ] **Step 4: Run and commit**

```bash
git add web/ui/src/pages/kb
git commit -m "feat(web/ui): narrow the KB editor gutters to 7%"
```

---

### Task 12: Split the Owner area into five gated sections

**Files:**
- Modify: `web/ui/src/pages/settings/SettingsPage.tsx` (SECTIONS, groups, redirect, rendering)
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx` (export the five sections individually)
- Modify: `web/ui/src/pages/settings/OwnerGate.tsx` (add `title` prop)
- Modify: `web/ui/src/pages/settings/OwnerSections.test.tsx`, `settings.test.tsx`

**Interfaces:**
- Consumes: `ENTITY_ICONS` owner keys (Task 5).
- Produces: named exports `WorkspacesSection`, `InstanceURLSection`, `SystemStatusSection`, `AuditLogSection` from `OwnerSections.tsx` (`BackupSection` already has its own module); `OwnerGate` gains `title?: string`.

- [ ] **Step 1: Write the failing tests**

Append to `settings.test.tsx`:

```tsx
const OWNER_SLUGS = [
  "owner-workspaces", "owner-instance-url", "owner-system",
  "owner-backup", "owner-audit",
] as const;

test("the settings nav lists both groups", async () => {
  renderSettings();
  expect(await screen.findByText("Workspace")).toBeInTheDocument();
  expect(screen.getByText(/^Owner$/)).toBeInTheDocument();  // the group LABEL
  for (const l of ["Workspaces", "Instance URL", "System status", "Backup", "Audit log"]) {
    expect(screen.getByRole("button", { name: new RegExp(l, "i") })).toBeInTheDocument();
  }
});

test("?section=owner redirects to the first owner section", async () => {
  renderSettings("?section=owner");
  // Old bookmarks must keep working.
  await waitFor(() => expect(window.location.search).toContain("section=owner-workspaces"));
});

test.each(OWNER_SLUGS)("%s renders behind the owner gate", async (slug) => {
  // A gated probe (403 owner_verification_required) must show the prompt, not
  // the section body — a missed wrap would expose an install-level section.
  mockGatedProbe();
  renderSettings(`?section=${slug}`);
  expect(await screen.findByLabelText(/owner password/i)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run them** → FAIL.

- [ ] **Step 3: Restructure SECTIONS into groups**

```tsx
const SECTION_GROUPS = [
  {
    label: "Workspace",
    sections: [
      { slug: "profile", label: "Profile" },
      { slug: "workspace", label: "Workspace" },
      { slug: "ai-providers", label: "AI Providers" },
      { slug: "coder", label: "Coder" },
      { slug: "master-password", label: "Master password" },
      { slug: "appearance", label: "Appearance" },
    ],
  },
  {
    label: "Owner",
    sections: [
      { slug: "owner-workspaces", label: "Workspaces" },
      { slug: "owner-instance-url", label: "Instance URL" },
      { slug: "owner-system", label: "System status" },
      { slug: "owner-backup", label: "Backup" },
      { slug: "owner-audit", label: "Audit log" },
    ],
  },
] as const;

const SECTIONS = SECTION_GROUPS.flatMap((g) => g.sections);

// ?section=owner used to render all five owner sub-sections stacked in one
// page. Redirect it so existing links and bookmarks land somewhere real.
const LEGACY_SECTION_ALIASES: Record<string, SectionSlug> = { owner: "owner-workspaces" };
```

Render each owner slug as:

```tsx
{section === "owner-backup" && (
  <OwnerGate title="Backup">
    <BackupSection />
  </OwnerGate>
)}
```

…and the same shape for the other four. Each mounts `OwnerGate` independently; that costs no extra requests because the gate's probe is a react-query on the shared key `["admin","overview"]`, and one unlock covers all five because **the server owns the stamp**.

- [ ] **Step 4: Decompose OwnerSections.tsx**

Change `function WorkspacesSection()` etc. to `export function …` for all four, and delete the `OwnerSections` wrapper (its `<h2>Owner</h2>` and stacked layout are what made the page cluttered). Each section keeps its own `<h3>`; promote those to the section's own `PageTitle`-style heading using the owner entity icons.

- [ ] **Step 5: Add the title prop to OwnerGate**

```tsx
export function OwnerGate({ title = "Owner settings", children }: {
  title?: string;
  children: React.ReactNode;
}) { … }
```

and use `{title}` in place of the hardcoded `Owner settings` heading. Leave the probe, the 403 detection, the absent client-side TTL and the server-enforcement comment untouched — that behaviour landed in `e225165`/`396a9f3` and this task must not weaken it.

- [ ] **Step 6: Run and commit**

Run: `cd web/ui && npx vitest run src/pages/settings` → PASS

```bash
git add web/ui/src/pages/settings
git commit -m "feat(web/ui): split Owner settings into five gated sections"
```

---

### Task 13: Fill the homepage

**Files:**
- Modify: `web/ui/src/pages/home/HomePage.tsx`
- Create: `web/ui/src/pages/home/cards.tsx` (the four new cards — HomePage is already 646 lines)
- Modify: `web/ui/src/pages/home/home.test.tsx`

**Interfaces:**
- Consumes: `Card`/`cardClass`/`CardTitle` (Task 3), `PageTitle`/`PageContainer` (Task 8), `useDashboard`, `useAgents`, `useRecentFiles`.
- Produces: `QuickActions`, `RecentActivityCard`, `AgentsAtAGlanceCard`, `RecentNotesCard`.

- [ ] **Step 1: Write the failing tests**

```tsx
test("quick actions link to the four create surfaces", async () => {
  renderHome();
  for (const [name, href] of [
    [/new agent/i, "/agents/new"], [/new note/i, "/kb"],
    [/start chat/i, "/chats"], [/connect/i, "/connections"],
  ] as const) {
    expect(await screen.findByRole("link", { name })).toHaveAttribute("href", href);
  }
});

test("recent activity shows successful runs, not only failures", async () => {
  renderHome({ recent_runs: [
    { id: "1", agent_id: "a", agent_name: "Nightly", status: "success", trigger: "schedule", started_at: new Date().toISOString(), finished_at: null },
  ]});
  // NeedsAttentionCard filters to failures; this card deliberately does not —
  // recent_runs was already fetched and its successes were never rendered.
  expect(await screen.findByText("Nightly")).toBeInTheDocument();
});

test("agents at a glance scrolls wide content in its own container", async () => {
  renderHome();
  const card = await screen.findByLabelText(/agents at a glance/i);
  expect(card.querySelector(".overflow-x-auto")).toBeTruthy();
});

test("recently edited notes links back into the editor", async () => {
  seedRecentFiles([{ path: "notes/plan.md", title: "plan", at: Date.now() }]);
  renderHome();
  expect(await screen.findByRole("link", { name: /plan/i }))
    .toHaveAttribute("href", expect.stringContaining("path=notes%2Fplan.md"));
});
```

- [ ] **Step 2: Run them** → FAIL.

- [ ] **Step 3: Build the cards in `cards.tsx`**

Four components, each wrapped in `<section aria-label>` + `cardClass` so tests and screen readers can address them. `RecentActivityCard` takes `runs: DashboardRun[]`, slices to 8, renders a status dot coloured `bg-ok`/`bg-danger`/`bg-warn` by `status`, agent name as a `<Link>`, trigger, and a relative timestamp via the existing `formatMessageTime` helper. `AgentsAtAGlanceCard` renders a `<table>` inside `<div className="overflow-x-auto">` (fluid pages must scroll wide content inside their own container, per the existing convention). `RecentNotesCard` reads `useRecentFiles()` and links to `/kb?path=<encoded>`. `QuickActions` is four `<Link>`s styled with `buttonVariants({ variant: "outline", size: "sm" })`, each with a leading lucide icon.

- [ ] **Step 4: Recompose the page**

```tsx
<PageContainer>
  <PageTitle icon="home" title={dash ? `${greeting(hour)}, ${dash.display_name}` : "Welcome"}
             actions={<QuickActions />} />
  <div className="mb-6 flex flex-col gap-3 sm:flex-row">{/* 3 stat tiles */}</div>
  <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
    <div className="flex flex-col gap-4">
      <RecentActivityCard runs={runs} />
      <AgentsAtAGlanceCard />
    </div>
    <div className="flex flex-col gap-4">
      <NextUpCard upcoming={dash?.upcoming ?? []} />
      <NeedsAttentionCard runs={runs} />
      <RemindersCard view={remindersView} />
      <RecentNotesCard />
    </div>
  </div>
</PageContainer>
```

- [ ] **Step 5: Run and commit**

Run: `cd web/ui && npx vitest run src/pages/home` → PASS

```bash
git add web/ui/src/pages/home
git commit -m "feat(web/ui): fill the homepage with activity, agents and recent notes"
```

- [ ] **Step 6: Phase 2 gate**

Run: `make ci` → all green. `web/api_parity_test.go` in particular must pass unchanged (no routes were added).

---

# Phase 3 — Gaps and fixes

### Task 14: Make mark-all-read visible

**Files:** Modify `web/ui/src/pages/home/HomePage.tsx:230-238`, `web/ui/src/pages/home/inbox.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("mark all read is visible whenever there are messages, disabled when none unread", async () => {
  // The endpoint, hook and tests all existed; the button was a 24px, 12px
  // grey ghost TEXT button that only rendered when unread > 0 — invisible.
  renderInbox({ messages: [{ id: "1", read: true, /* … */ }], unread: 0 });
  const btn = await screen.findByRole("button", { name: /mark all read/i });
  expect(btn).toBeDisabled();
  expect(btn.className).toMatch(/h-9/);
  expect(btn.querySelector("svg")).toBeTruthy();
});
```

- [ ] **Step 2: Run it** → FAIL (not rendered at all when `unread === 0`).

- [ ] **Step 3: Change the affordance**

```tsx
action={
  messages.length > 0 ? (
    <div className="flex items-center gap-2">
      {unread > 0 && <InboxCountBadge count={unread} />}
      <Button variant="outline" size="sm" disabled={unread === 0}
              onClick={() => markAll.mutate()}>
        <CheckCheck /> Mark all read
      </Button>
    </div>
  ) : undefined
}
```

- [ ] **Step 4: Run and commit**

```bash
git add web/ui/src/pages/home
git commit -m "fix(web/ui): make inbox mark-all-read discoverable"
```

---

### Task 15: Icons in the KB tree action menu

**Files:** Modify `web/ui/src/pages/kb/FileTree.tsx:629-648` and the delete dialog (~377); modify `tree.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("every tree menu item carries an icon and delete is a red trash", async () => {
  renderTree();
  await userEvent.click(screen.getAllByRole("button", { name: /Actions for/i })[0]);
  for (const label of [/new note/i, /new folder/i, /change icon/i, /rename/i, /delete/i]) {
    expect(screen.getByRole("menuitem", { name: label }).querySelector("svg")).toBeTruthy();
  }
});

test("a protected node exposes only Change icon", async () => {
  // isProtectedPath withholds Rename/Delete on system-managed DB-backed nodes
  // (agents/chats/inbox/skills/reminders) because renaming or deleting the
  // file would orphan the backing record. The reported "menu has only one
  // item" was this, working correctly.
  renderTree();
  await userEvent.click(screen.getByRole("button", { name: /Actions for agents/i }));
  expect(screen.getByRole("menuitem", { name: /change icon/i })).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: /delete/i })).toBeNull();
  expect(screen.queryByRole("menuitem", { name: /rename/i })).toBeNull();
});
```

- [ ] **Step 2: Run it** → FAIL.

- [ ] **Step 3: Add the icons**

`FilePlus` / `FolderPlus` / `Smile` / `Pencil` / `Trash2`, each `<Icon />` as the item's first child (the `DropdownMenuItem` already sizes descendant svgs). Delete keeps `variant="destructive"`.

The delete dialog at ~377 already exists and is reused — only its confirm button changes, gaining `<Trash2 />`. Note this is the one intentional departure from spec 1's "dialog footer pairs stay text-only": on a destructive confirm the icon *is* the warning.

- [ ] **Step 4: Run and commit**

```bash
git add web/ui/src/pages/kb
git commit -m "feat(web/ui): add icons to the KB tree action menu"
```

---

### Task 16: Icons in the workspace menu

**Files:** Modify `web/ui/src/components/shell/WorkspaceMenu.tsx:85-88`, `WorkspaceMenu.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("workspace menu actions carry icons", async () => {
  renderMenu();
  await userEvent.click(screen.getByRole("button", { name: /workspace/i }));
  for (const l of [/change image/i, /create workspace/i, /leave workspace/i]) {
    expect(screen.getByRole("menuitem", { name: l }).querySelector("svg")).toBeTruthy();
  }
});
```

- [ ] **Step 2: Run it** → FAIL.

- [ ] **Step 3: Add `Image`, `Plus`, `LogOut`**

Also drop the literal `+` from the "Create workspace" label — the `Plus` icon replaces it. Switch rows keep `WorkspaceAvatar`; an avatar already *is* the icon and is the recognition affordance.

- [ ] **Step 4: Run and commit**

```bash
git add web/ui/src/components/shell
git commit -m "feat(web/ui): add icons to the workspace menu"
```

---

### Task 17: Generate the full Unicode emoji set

**Files:**
- Create: `web/ui/scripts/gen-emoji.mjs`
- Create: `web/ui/scripts/emoji-source.json` (vendored `unicode-emoji-json` `data-by-group.json`)
- Create: `web/ui/src/pages/kb/emojiData.generated.ts` (committed output)
- Modify: `web/ui/src/pages/kb/emojiData.ts` (re-export), `EmojiPicker.tsx` (tabs + sticky search)
- Create: `web/ui/src/pages/kb/emojiData.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `emojiGroups: EmojiGroup[]` and `filterEmojis(q: string): EmojiEntry[]` keep their existing signatures, so `EmojiPicker` changes only cosmetically.

- [ ] **Step 1: Vendor the source data**

```bash
mkdir -p web/ui/scripts
curl -sSL -o web/ui/scripts/emoji-source.json \
  "https://cdn.jsdelivr.net/npm/unicode-emoji-json@0.8.0/data-by-group.json"
```

Vendored rather than fetched at build time so `npm ci && vite build` needs no network. 9 groups, 1906 emoji.

- [ ] **Step 2: Write the failing test**

`web/ui/src/pages/kb/emojiData.test.ts`:

```ts
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { emojiGroups, filterEmojis } from "./emojiData";

test("the set covers the standard Unicode groups at full size", () => {
  expect(emojiGroups.length).toBe(9);
  const total = emojiGroups.reduce((n, g) => n + g.emojis.length, 0);
  expect(total).toBeGreaterThan(1500);
  for (const g of emojiGroups) for (const e of g.emojis) expect(e.keywords.length).toBeGreaterThan(0);
});

test("search matches on keyword, not just the glyph", () => {
  const hits = filterEmojis("book");
  expect(hits.length).toBeGreaterThan(3);
  expect(hits.some((h) => h.emoji === "📚")).toBe(true);
  expect(filterEmojis("zzzznotathing")).toEqual([]);
});

test("the committed generated file matches the generator's output", () => {
  // A stale commit must fail CI rather than silently ship an old set.
  const fresh = execFileSync("node", ["scripts/gen-emoji.mjs", "--stdout"], {
    cwd: new URL("../../../", import.meta.url).pathname, encoding: "utf8",
  });
  const onDisk = readFileSync(new URL("./emojiData.generated.ts", import.meta.url).pathname, "utf8");
  expect(onDisk.trim()).toBe(fresh.trim());
});
```

- [ ] **Step 3: Run it** → FAIL (only 9 curated groups of ~160 total; no generator).

- [ ] **Step 4: Write the generator**

`web/ui/scripts/gen-emoji.mjs` reads `emoji-source.json`, and for each group emits `{ name, emojis: [{ emoji, keywords }] }` where `keywords` is the emoji's `name` plus its `slug` with underscores as spaces, de-duplicated into a single space-joined string. Writes `src/pages/kb/emojiData.generated.ts` (or stdout with `--stdout`) with a header explaining it is generated and must not be hand-edited, plus the exact command to regenerate.

- [ ] **Step 5: Rewrite emojiData.ts as a thin re-export**

Keep `EmojiEntry`/`EmojiGroup` types and `filterEmojis` (now searching the generated table); re-export `emojiGroups` from the generated module. Preserve the existing header note about why the picker stays dependency-free, updated to say the set is now generated rather than curated.

- [ ] **Step 6: Add tabs and sticky search to the picker**

1906 emoji cannot be scrolled blind. Add a category tab strip (the 9 group names, horizontally scrollable), keep the search field pinned above the grid, and show the active group's grid when the query is empty. Raise the dialog to `max-w-2xl` and the scroll area to `max-h-[60vh]`.

- [ ] **Step 7: Run and commit**

Run: `cd web/ui && npx vitest run src/pages/kb/emojiData.test.ts && npx vitest run` → PASS

```bash
git add web/ui/scripts web/ui/src/pages/kb
git commit -m "feat(web/ui): generate the full Unicode emoji set at build time"
```

---

### Task 18: Expand workspace presets to 28

**Files:**
- Modify: `web/ui/src/lib/workspaceIcons.tsx`
- Modify: `web/api_settings.go` (`workspaceIcons` validator)
- Modify: `web/ui/src/components/shell/WorkspaceIconPicker.tsx` (scrollable grid)
- Create: `web/ui/src/lib/workspaceIcons.test.ts`
- Modify: `web/api_settings_test.go`

**Interfaces:** `WORKSPACE_ICONS` keeps its `WorkspaceIcon[]` shape.

- [ ] **Step 1: Write the failing tests**

TS side:

```ts
import { WORKSPACE_ICONS } from "./workspaceIcons";

test("there are 28 presets with unique slugs", () => {
  expect(WORKSPACE_ICONS.length).toBe(28);
  expect(new Set(WORKSPACE_ICONS.map((i) => i.slug)).size).toBe(28);
});
```

Go side, in `web/api_settings_test.go`:

```go
func TestWorkspaceIconSlugsMatchTheFrontend(t *testing.T) {
	// Two lists that must agree: the Go validator rejects any slug outside its
	// set, so a preset added only to the TS file is unselectable, and one added
	// only to Go is unreachable. Parsing the TS is ugly but it is the only way
	// to make the drift fail the build.
	src, err := os.ReadFile("ui/src/lib/workspaceIcons.tsx")
	if err != nil {
		t.Skip("frontend source not available")
	}
	re := regexp.MustCompile(`slug:\s*"([a-z0-9-]+)"`)
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		found[m[1]] = true
	}
	if len(found) == 0 {
		t.Fatal("parsed no slugs from workspaceIcons.tsx")
	}
	for s := range found {
		if !workspaceIcons[s] {
			t.Errorf("slug %q exists in the SPA but the Go validator rejects it", s)
		}
	}
	for s := range workspaceIcons {
		if !found[s] {
			t.Errorf("slug %q is accepted by Go but has no SPA preset", s)
		}
	}
}
```

(Adapt to `workspaceIcons`' actual type — if it is a `[]string`, build the set first.)

- [ ] **Step 2: Run them** → FAIL (12 presets; slug sets disagree once new ones are added).

- [ ] **Step 3: Add 16 presets**

Same system: a two-stop gradient plus **one simple motif** on the 24×24 viewBox. Motifs must stay legible at 20px — the size actually seen in the rail — so no detailed illustrations. Reuse the existing motif constants where a new gradient is enough, and add a few new ones (e.g. `chevrons`, `grid`, `spiral`, `pillars`, `droplet`, `star`, `crescent`, `bolt`).

- [ ] **Step 4: Extend the Go validator**

Add all 16 new slugs to `workspaceIcons` in `web/api_settings.go`, in the same commit.

- [ ] **Step 5: Make the picker grid scrollable**

`WorkspaceIconPicker` gets `max-h-[55vh] overflow-y-auto` on its grid; 28 tiles no longer fit a fixed dialog.

- [ ] **Step 6: Run and commit**

Run: `cd web/ui && npx vitest run src/lib/workspaceIcons.test.ts && go test ./web/ -run WorkspaceIcon` → PASS

```bash
git add web/ui/src web/api_settings.go web/api_settings_test.go
git commit -m "feat(web): expand workspace presets to 28"
```

---

### Task 19: Diagnose and fix unclickable KB search results

**REQUIRED SUB-SKILL:** `superpowers:systematic-debugging`. Do **not** write a fix before the reproduction confirms which branch is real — the wiring is correct in the source, so the cause is not statically visible and a guess will be wrong.

**Files:** determined by the diagnosis. Candidates: `web/ui/src/pages/kb/SearchBox.tsx`, `KBPage.tsx`, `components/shell/usePaneWidth.tsx`, `internal/vault/search.go`.

- [ ] **Step 1: Reproduce against the running server**

```bash
make deploy && make status
```

Open the KB, type a 2+ character query that matches a note, click a result, and record: **does the `?path=` query param in the URL change?**

- [ ] **Step 2: Branch on the observation**

| `?path=` | Meaning | Investigate |
|---|---|---|
| **changes** | click works, *open* fails | the path shape `internal/vault/search.go` returns vs what `GET /api/v1/kb/note` expects; the `dir` hint; a 404 rendered as `FileViewer`'s "Couldn't load this file." Check the Network tab for the note request and its status. |
| **unchanged** | the click never reaches the handler | `PaneResizeHandle` (`absolute top-0 right-0 h-full w-1`, `AppShell` renders it as a sibling *after* `{contextPane}`) overlaying the pane; the pane's `overflow-y-auto`; a stacking/pointer-events issue. Bisect by temporarily removing the handle. |

- [ ] **Step 3: Write a failing regression test at the layer the bug lives in**

If it is event delivery: a `SearchBox`/`AppShell` interaction test asserting a click on a result invokes `onSelect`. If it is the open path: a `KBPage` test asserting a search-selected path renders the document, or a Go test on the path shape `search.go` returns.

- [ ] **Step 4: Run it to confirm it fails, fix, re-run to confirm it passes**

- [ ] **Step 5: Commit**

```bash
git commit -am "fix(web/ui): make KB search results open the note"
```

- [ ] **Step 6: If it does not reproduce**

Do **not** ship a speculative change. Record the observations taken (URL behaviour, network requests, whether the handler fired) in the PR description and leave the code alone.

---

### Task 20: Final gate and ship

- [ ] **Step 1: Full local CI**

Run: `make ci` → every job green.

- [ ] **Step 2: Deploy and smoke-test the real app**

```bash
make deploy && make status
curl -sS -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/
curl -sS http://127.0.0.1:8080/api/v1/auth/session
curl -sS http://127.0.0.1:8080/healthz
```

Then click through by hand: the font is Inter everywhere; the rail/title icons agree; the slide-over is a third of the page; the KB editor gutters are ~7%; the eleven settings sections each render with the owner ones gated; the homepage cards are populated; the emoji picker tabs work; 28 workspace presets appear.

- [ ] **Step 3: Push and open a draft PR**

```bash
git push -u origin worktree-ui-overhaul
gh pr create --draft --base feat/identity-source-of-truth \
  --title "feat(web/ui): overhaul the design system, layout and density"
```

Base is `feat/identity-source-of-truth`, **not** `main` — the owner gate this builds on lives only there.

---

## Self-review

**Spec coverage.** Spec 1: font T1+T7, type scale T2, density T2/T6, borders+Card T3, buttons T4, icons T5. Spec 2: container+titles T8, sheet T9, search modal T10, editor gutters T11, owner split T12, homepage T13. Spec 3: mark-all T14, tree menu T15, workspace menu T16, emoji T17, presets T18, search defect T19. No spec section is unclaimed.

**Type consistency.** `entityIcon`/`ENTITY_ICONS` (T5) are the names T8 and T12 consume. `cardClass`/`Card`/`CardTitle` (T3) are what T13 uses. `fonts.InterVariableWOFF2` (T1) is what T7 imports. `emojiGroups`/`filterEmojis` keep their existing signatures (T17) so `EmojiPicker` is not rewritten. `OwnerGate`'s new prop is `title?: string` in both T12's definition and its call sites.

**Known deviations from the specs, recorded deliberately.** (1) Spec 1 says dialog footer pairs stay text-only; T15 gives the *destructive confirm* an icon, because there the icon is the warning — noted at both sites. (2) `RemindersCard` keeps a `<section>` element rather than becoming a `<Card>`, so its `aria-label` landmark survives; it uses the exported `cardClass` instead.
</content>
</invoke>
