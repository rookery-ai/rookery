# UI Redesign Sub-plan 3: Knowledge Base Editor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Slite/Tolaria-class knowledge base: a TipTap WYSIWYG markdown editor with autosave, bubble toolbar, slash menu, wikilink pills, backlinks, a real file tree in the context pane, and full-text search — on the existing `/api/v1/kb` endpoints.

**Architecture:** All frontend (`web/ui/src/pages/kb/` + `web/ui/src/lib/kb.ts`) except one tiny Go endpoint (`GET /api/v1/kb/resolve` for wikilink-click resolution, reusing the vault's existing LinkIndex). Notes stay plain `.md` on disk; the editor serializes to markdown on every save. **Fidelity fallback**: any note whose markdown cannot round-trip losslessly (e.g. the memory scaffolds' HTML comments) opens in raw-markdown mode with a banner instead of silently mangling — the vault is agent-shared state, corruption is the one unforgivable failure. Spec: `docs/superpowers/specs/2026-07-16-ui-redesign-design.md` §6.

**Tech Stack:** TipTap (@tiptap/react, starter-kit, task lists, link, placeholder, table, @tiptap/suggestion) + `tiptap-markdown` for serialization; wikilinks via ProseMirror **decorations** (document text stays literal `[[name]]` — zero round-trip risk); TanStack Query for tree/note data.

## Global Constraints

- Branch `ui-redesign`. Frontend: `cd web/ui && npm test -- --run` + `npm run build` green at every commit; Go: `go test ./... -count=1 -timeout 120s` green at every commit.
- KB API DTOs (from `web/api_kb.go`, verbatim): tree `GET /api/v1/kb/tree?path=` → `{path, nodes:[{name,display_name,path,is_dir,system}]}`; note `GET /api/v1/kb/note?path=` → `{path,content,html,backlinks:[string]}`; save `PUT /api/v1/kb/note {path,content}`; create `POST /api/v1/kb/new {path,is_dir}`; delete `DELETE /api/v1/kb/note?path=`; rename `POST /api/v1/kb/rename {from,to}`; search `GET /api/v1/kb/search?q=` → `{hits:[{path,line,snippet}]}`; raw download `GET /api/v1/kb/raw?path=`. Errors: envelope; escapes → 400 `invalid_path`; missing → 404 `not_found`.
- **Never mutate note content the user didn't type**: the editor only PUTs when the user edited in WYSIWYG mode AND the note passed the round-trip fidelity check on load, or the user edited in raw mode. A fidelity-failed note NEVER auto-saves from WYSIWYG.
- Shell APIs from sub-plan 2 (binding): `useSlideOver()`, `useContextPane()` (raw setter; Task 1 adds the declarative `<ContextPane>` wrapper and pages use THAT), `railItems`, `api`/`ApiError` from `@/lib/api`, TanStack Query with key discipline (`["kb-tree", path]`, `["kb-note", path]`, invalidate on mutations).
- KB route: `/kb` with `?path=<vault-relative>` search param (dirs select tree location; `.md` files open the editor).
- react-router is **v8** (`react-router` package); shadcn components added with `npx shadcn@3 add <name>` (CLI pinned — never `@latest`).
- npm packages: latest stable; lockfile committed.
- Deliberate deferrals (documented, not gaps): drag-to-move in the tree (sub-plan 6 polish; move = rename dialog for now); image upload (no backend); Playwright e2e (pre-merge, sub-plan 6).

---

### Task 1: SP2 pickups + KB data layer + ContextPane wrapper

**Files:**
- Create: `web/ui/src/lib/kb.ts`, `web/ui/src/lib/kb.test.ts`
- Modify: `web/ui/src/components/shell/AppShell.tsx` (memoize ShellCtx; add `ContextPane` component), `web/ui/src/App.tsx` (session-key guard in QueryCache onError)
- Delete: `web/ui/src/assets/vite.svg`

**Interfaces:**
- Produces (every later task consumes): 

```ts
// lib/kb.ts
export type KBNode = { name: string; display_name: string; path: string; is_dir: boolean; system: boolean };
export type KBNote = { path: string; content: string; html: string; backlinks: string[] };
export type KBSearchHit = { path: string; line: number; snippet: string };
export function useKBTree(path: string)   // useQuery ["kb-tree", path] → { path, nodes: KBNode[] }
export function useKBNote(path: string | null) // useQuery ["kb-note", path], enabled: !!path → KBNote
export function useSaveNote()   // useMutation PUT /api/v1/kb/note; onSuccess: invalidate ["kb-note", path]
export function useNewNote()    // useMutation POST /api/v1/kb/new; onSuccess: invalidate ["kb-tree"]
export function useDeleteNote() // useMutation DELETE /api/v1/kb/note?path=; invalidates ["kb-tree"] + removes ["kb-note", path]
export function useRenameNote() // useMutation POST /api/v1/kb/rename; invalidates ["kb-tree"] + both note keys
export function useKBSearch(q: string) // useQuery ["kb-search", q], enabled: q.length >= 2
export const rawURL = (path: string) => `/api/v1/kb/raw?path=${encodeURIComponent(path)}`;
```

- Produces: `<ContextPane>{children}</ContextPane>` exported from AppShell — declarative wrapper: `useEffect(() => { setContextPane(children); return () => setContextPane(null); }, [children])`. Pages use it; the raw `useContextPane` stays as escape hatch. ShellCtx: `openPanel`/`closePanel` wrapped in `useCallback`, provider `value` in `useMemo`.
- App.tsx QueryCache onError gains first line: `if (query.queryKey[0] === "session") return;` (self-protection against invalidation loops).

- [ ] **Step 1: Failing tests** — `web/ui/src/lib/kb.test.ts`:

```ts
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useKBTree, useKBNote, rawURL } from "./kb";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

test("useKBTree fetches nodes for a path", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ path: "notes", nodes: [{ name: "a.md", display_name: "a", path: "notes/a.md", is_dir: false, system: false }] }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  )));
  const { result } = renderHook(() => useKBTree("notes"), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.nodes[0].path).toBe("notes/a.md"));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/kb/tree?path=notes");
});

test("useKBNote is disabled for null path", () => {
  vi.stubGlobal("fetch", vi.fn());
  const { result } = renderHook(() => useKBNote(null), { wrapper: wrapper() });
  expect(result.current.fetchStatus).toBe("idle");
  expect(fetch).not.toHaveBeenCalled();
});

test("rawURL encodes the path", () => {
  expect(rawURL("notes/my note.md")).toBe("/api/v1/kb/raw?path=notes%2Fmy%20note.md");
});
```

Run `npm test -- --run` → FAIL (module missing).

- [ ] **Step 2: Implement `lib/kb.ts`** per the Interfaces block (thin wrappers over `api.*` + useQuery/useMutation; query param paths always `encodeURIComponent`ed; mutations take `{path}`-bearing variables so onSuccess can invalidate precisely).

- [ ] **Step 3: AppShell changes** — add inside AppShell.tsx:

```tsx
export function ContextPane({ children }: { children: React.ReactNode }) {
  const setContextPane = useContextPane();
  useEffect(() => {
    setContextPane(children);
    return () => setContextPane(null);
  }, [children, setContextPane]);
  return null;
}
```

Wrap `openPanel`/`closePanel`/`setContextPane` in `useCallback` and the provider `value` in `useMemo`. Extend `shell.test.tsx` with one test: rendering a page that returns `<ContextPane><div>PANE</div></ContextPane>` shows PANE; unmounting removes it.

- [ ] **Step 4: App.tsx guard + vite.svg removal** — add the session-key early-return as the first line of the QueryCache onError; `git rm web/ui/src/assets/vite.svg`.

- [ ] **Step 5: Run + commit** — `npm test -- --run` all green; `npm run build` clean. `git add -A && git commit -m "feat(ui): KB data layer + declarative ContextPane + SP2 review pickups"`

---

### Task 2: KB page + file tree in the context pane

**Files:**
- Create: `web/ui/src/pages/kb/KBPage.tsx`, `web/ui/src/pages/kb/FileTree.tsx`, `web/ui/src/pages/kb/tree.test.tsx`
- Modify: `web/ui/src/router.tsx` (swap /kb placeholder for `<KBPage />`)

**Interfaces:**
- Consumes: `useKBTree`, `useNewNote`, `useDeleteNote`, `useRenameNote`, `<ContextPane>`, shadcn dialog/dropdown-menu, lucide icons (Folder, FolderOpen, FileText, Plus, MoreHorizontal, Brain, Bot, MessageSquare, Sparkles).
- Produces: `KBPage` reads `?path=` via `useSearchParams`; selecting a file navigates `/kb?path=<file>`; `FileTree` props: `{ selectedPath: string | null; onSelect(path: string, isDir: boolean): void }`. Directory expansion is lazy (each expanded dir mounts a child `useKBTree(dirPath)` — the API is per-directory). Special root dirs get icons + muted styling when `system: true` (backend flag) — user content (`notes/`, `memory/`) sorts before system dirs, matching spec §6 "your notes come first": sort key `(system ? 1 : 0, is_dir ? 0 : 1, name)`.
- Tree row actions (dropdown per row): New note / New folder (dirs only), Rename…, Delete… — dialogs; delete confirms with the path shown; rename dialog pre-fills the current path and accepts a full new path (this IS the move mechanism for now — deferral note in Global Constraints).
- Empty content area (no `?path=` or a dir selected): a friendly empty state ("Select a note or create one").

- [ ] **Step 1: Failing test** — `tree.test.tsx`: mock fetch for `/api/v1/kb/tree?path=` (root: `notes` dir + `README.md` + system `chats` dir) and `/api/v1/kb/tree?path=notes` (one file `notes/a.md`); render `<FileTree selectedPath={null} onSelect={spy}/>` inside QueryClientProvider; assert README.md row renders; `chats` row has the muted class (`text-muted-2` — assert via className match); click `notes` folder → child row `a.md` appears (lazy load fired); click `a.md` → spy called with `("notes/a.md", false)`.
- [ ] **Step 2:** verify FAIL, implement `FileTree.tsx` (recursive `TreeDir`/`TreeRow` components, `expanded` state per dir, chevron rotate, icons per kind: memory→Brain, agents→Bot, chats→MessageSquare, skills→Sparkles, other dirs→Folder/FolderOpen, files→FileText) and `KBPage.tsx`:

```tsx
export default function KBPage() {
  const [params, setParams] = useSearchParams();
  const path = params.get("path");
  const isFile = !!path && path.endsWith(".md");
  return (
    <>
      <ContextPane>
        <KBPaneHeader />  {/* "Knowledge Base" title + new-note button + search box (Task 6 fills search) */}
        <FileTree selectedPath={path} onSelect={(p) => setParams({ path: p })} />
      </ContextPane>
      {isFile ? <NoteEditor path={path} key={path} /> : <KBEmptyState />}
    </>
  );
}
```

`NoteEditor` is a stub in this task (`<div>editor: {path}</div>` — Task 3 replaces); `key={path}` forces remount per note (editor state isolation). Row-action dialogs live in FileTree; wire the mutations with error display (ApiError.message inline in the dialog).
- [ ] **Step 3:** router swap; run all tests + build; commit `feat(ui): KB page + lazy file tree with create/rename/delete`.

---

### Task 3: TipTap editor core — markdown round-trip, autosave, fidelity fallback, raw mode

**Files:**
- Create: `web/ui/src/pages/kb/NoteEditor.tsx`, `web/ui/src/pages/kb/editor.ts` (TipTap config + markdown helpers), `web/ui/src/pages/kb/editor.test.ts`
- Modify: `web/ui/src/pages/kb/KBPage.tsx` (real NoteEditor import)

**Interfaces:**
- Consumes: `useKBNote`, `useSaveNote`, `rawURL`.
- Produces (Tasks 4-6 build on): `buildExtensions(opts?)` in `editor.ts` returning the TipTap extension array; `toMarkdown(editor): string`; `checkFidelity(md: string): boolean` (parse→serialize→normalized-compare); `<NoteEditor path>` with save-state callback contract `onStateChange?(s: "saved"|"saving"|"dirty"|"error"|"raw")` (Task 6's header consumes it).

- [ ] **Step 1: Install**

```bash
cd /home/user/simple-agents-v2/web/ui
npm install @tiptap/react @tiptap/pm @tiptap/starter-kit tiptap-markdown \
  @tiptap/extension-link @tiptap/extension-placeholder \
  @tiptap/extension-task-list @tiptap/extension-task-item \
  @tiptap/extension-table @tiptap/extension-table-row \
  @tiptap/extension-table-cell @tiptap/extension-table-header \
  @tiptap/suggestion
```

(If tiptap-markdown's peer range conflicts with the installed TipTap major, resolve by installing the TipTap major tiptap-markdown supports — note versions in the report.)

- [ ] **Step 2: Failing fidelity tests** — `editor.test.ts` (node-side, no DOM render needed — TipTap can run headless in jsdom):

```ts
import { fidelityRoundTrip, checkFidelity } from "./editor";

const CLEAN = `# Title

Some **bold** and *italic* and \`code\`.

- a list
- [ ] a todo
- [x] done

> quote

\`\`\`js
const x = 1;
\`\`\`
`;

const HTML_COMMENT = `# About Me

<!-- Add your name, location, role, and background here -->
`;

test("clean markdown round-trips", () => {
  expect(checkFidelity(CLEAN)).toBe(true);
});

test("wikilinks survive round-trip as literal text", () => {
  expect(checkFidelity("See [[other-note]] and [[notes/deep]].\n")).toBe(true);
});

test("HTML comments are detected as lossy (raw-mode fallback)", () => {
  // The memory scaffolds (USER.md/SOUL.md) contain HTML placeholder comments;
  // if tiptap-markdown ever round-trips them cleanly this test flips — then
  // remove the raw-mode forcing for comments, not the test.
  expect(checkFidelity(HTML_COMMENT)).toBe(false);
});
```

If the HTML-comment case turns out to round-trip cleanly with `html: true` serializer options, KEEP fidelity=true and adjust the test + report it — the contract is "lossy → raw", not "comments → raw".

- [ ] **Step 3: Implement `editor.ts`:**

```ts
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import Link from "@tiptap/extension-link";
import Placeholder from "@tiptap/extension-placeholder";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import Table from "@tiptap/extension-table";
import TableRow from "@tiptap/extension-table-row";
import TableCell from "@tiptap/extension-table-cell";
import TableHeader from "@tiptap/extension-table-header";
import { Markdown } from "tiptap-markdown";

export function buildExtensions(extra: any[] = []) {
  return [
    StarterKit,
    Link.configure({ openOnClick: false }),
    Placeholder.configure({ placeholder: "Type / for blocks…" }),
    TaskList,
    TaskItem.configure({ nested: true }),
    Table.configure({ resizable: false }),
    TableRow, TableCell, TableHeader,
    Markdown.configure({ html: false, linkify: false, breaks: false }),
    ...extra,
  ];
}

export function toMarkdown(editor: Editor): string {
  return editor.storage.markdown.getMarkdown();
}

const normalize = (md: string) =>
  md.replace(/[ \t]+$/gm, "").replace(/\n{3,}/g, "\n\n").trim();

// fidelityRoundTrip: load md into a headless editor, serialize back.
export function fidelityRoundTrip(md: string): string {
  const editor = new Editor({ extensions: buildExtensions(), content: md });
  const out = toMarkdown(editor);
  editor.destroy();
  return out;
}

export function checkFidelity(md: string): boolean {
  try {
    return normalize(fidelityRoundTrip(md)) === normalize(md);
  } catch {
    return false;
  }
}
```

(Exact `Markdown.configure` option names and the `content` markdown-parsing entry point depend on the installed tiptap-markdown version — check its README in node_modules and adjust mechanically; the storage-based `getMarkdown()` is its stable API. If headless `new Editor` needs `element: document.createElement("div")` in jsdom, add it.)

- [ ] **Step 4: Implement `NoteEditor.tsx`:** load via `useKBNote(path)`; on data: run `checkFidelity(content)` once → `mode: "wysiwyg" | "raw"`; WYSIWYG renders `<EditorContent>` with `useEditor({ extensions: buildExtensions(), content })`; raw mode renders a monospace `<textarea>` with a banner: `"Opened in raw markdown to protect formatting this editor can't represent yet."` + a "Switch to raw" / "Try WYSIWYG anyway" toggle (trying anyway re-checks nothing — user accepts the risk explicitly; label the button that way). Autosave: `onUpdate` (or textarea onChange) marks dirty → 1000ms debounce → `useSaveNote().mutate({ path, content: toMarkdown(editor) })` (or textarea value) → state transitions dirty→saving→saved / error (ApiError.message). Flush pending save on unmount and on `Ctrl/Cmd+S`. A "Raw" toggle button + "Download" link (`rawURL(path)`) always available. Editor typography: wrap EditorContent in `prose`-like utility classes using the token palette (headings bold, code bg-chrome, checkboxes aligned) — a small `editor.css` imported by NoteEditor is fine; keep it under ~60 lines.
- [ ] **Step 5:** wire into KBPage (replace stub). Run all tests + build; commit `feat(ui): TipTap markdown editor — autosave, fidelity fallback to raw mode`.

---

### Task 4: Bubble toolbar + slash menu

**Files:**
- Create: `web/ui/src/pages/kb/BubbleToolbar.tsx`, `web/ui/src/pages/kb/SlashMenu.tsx` (+ `slashItems.ts`), `web/ui/src/pages/kb/slash.test.ts`
- Modify: `web/ui/src/pages/kb/editor.ts` (slash extension in buildExtensions via `extra`), `web/ui/src/pages/kb/NoteEditor.tsx`

**Interfaces:**
- Consumes: TipTap `BubbleMenu` (from @tiptap/react), `@tiptap/suggestion`.
- Produces: `slashItems: SlashItem[]` where `SlashItem = { title: string; keywords: string; run(editor): void }` — items: Heading 1/2/3, Bullet list, Numbered list, To-do list, Quote, Code block, Divider, Table (3×3). BubbleToolbar buttons: Bold, Italic, Strikethrough, Code, H1, H2, Link (prompt-based URL for now), Bullet, To-do, Quote — each toggling via `editor.chain().focus()...run()` with active-state styling.
- Slash menu: a Suggestion-based extension triggered by `/` at block start (or after whitespace), filtering `slashItems` by title/keywords, rendered as a floating list (position via the suggestion `clientRect`), keyboard navigation (up/down/enter/escape). Keep the renderer plain React + fixed positioning — no popper dependency.

- [ ] **Step 1:** failing `slash.test.ts` — pure-logic tests: filtering (`filterSlashItems("tod")` → To-do first), and each item's `run` invoked against a headless editor changes the doc (e.g. Heading 1 run → `editor.isActive("heading", { level: 1 })` true; Table run → doc contains a table node).
- [ ] **Step 2:** implement items + filter + suggestion extension + UI components; wire BubbleToolbar (only when selection non-empty) and slash menu into NoteEditor (WYSIWYG mode only).
- [ ] **Step 3:** run all + build; commit `feat(ui): editor bubble toolbar + slash menu`.

---

### Task 5: Wikilink pills + backlinks + resolve endpoint

**Files:**
- Create: `web/ui/src/pages/kb/wikilinks.ts`, `web/ui/src/pages/kb/wikilinks.test.ts`, `web/api_kb_resolve_test.go` additions in `web/api_kb.go` (one handler)
- Modify: `web/ui/src/pages/kb/NoteEditor.tsx` (decoration extension + backlinks strip), `web/api_kb.go` (+route), `web/api_parity_test.go` (+row `GET /api/v1/kb/resolve`)

**Interfaces:**
- Go: `GET /api/v1/kb/resolve?link=<wikilink-target>` → 200 `{"path":"notes/target.md"}` or 404 `not_found`. Implementation: reuse the vault's existing wikilink resolution — grep `internal/vault/links.go` for the function `RenderHTMLLinks`/`Backlinks` use to map a link name to a note path (LinkIndex); call the same one. ~25 lines + a Go test (seed two notes, resolve by bare name and by path, unknown → 404).
- Frontend `wikilinks.ts`: a TipTap extension wrapping a ProseMirror plugin that (a) scans text nodes with `/\[\[([^\]]+)\]\]/g` and adds inline **decorations** (class `wikilink-pill`, style: accent-soft bg, accent text, rounded, cursor-pointer) — the DOCUMENT text stays literal so markdown round-trip is untouched; (b) on click of a decorated range (plugin `handleClick`), calls `opts.onNavigate(target)`.
- NoteEditor: `onNavigate` = `api.get(/api/v1/kb/resolve?link=…)` → found: `setParams({path})`; 404: toast/inline hint "note not found: <name>". Backlinks strip: under the editor top edge, `note.backlinks.map` → pill buttons navigating to each path; hidden when empty. Pure function `findWikilinkRanges(text: string): {from:number;to:number;target:string}[]` exported for tests.

- [ ] **Step 1:** failing tests — Go resolve test (RED: route missing); `wikilinks.test.ts` for `findWikilinkRanges` (offsets, multiple links, ignores single brackets) and fidelity: `checkFidelity("a [[x]] b\n")` still true with the extension active in `buildExtensions([wikilinkExtension(noop)])`.
- [ ] **Step 2:** implement Go endpoint (envelope errors; register in `registerKBAPI`; add parity-test row) → `go test ./web/... -run 'TestAPIKB|TestAPIParity' -count=1` green.
- [ ] **Step 3:** implement the extension + NoteEditor wiring + backlinks strip → `npm test -- --run` green.
- [ ] **Step 4:** full suites both sides; commit `feat(kb): wikilink pills + click-to-navigate resolve endpoint + backlinks strip`.

---

### Task 6: UI-owned note header + rename + search

**Files:**
- Create: `web/ui/src/pages/kb/NoteHeader.tsx`, `web/ui/src/pages/kb/SearchBox.tsx`, `web/ui/src/pages/kb/header.test.tsx`
- Modify: `web/ui/src/pages/kb/KBPage.tsx`, `web/ui/src/pages/kb/NoteEditor.tsx` (emit onStateChange)

**Interfaces:**
- `NoteHeader` (spec §6 "UI-owned header"): breadcrumb from the path segments (each ancestor clickable → selects the dir in `?path=`), title = filename minus `.md` rendered as an inline-editable `<input>` (blur/Enter with a changed value → `useRenameNote().mutate({from, to})` where `to` = same dir + new name + `.md`, then `setParams({path: to})`; invalid → ApiError inline); right side: save-state chip fed by NoteEditor's `onStateChange` ("Saved ✓" / "Saving…" / "Unsaved" / "Save failed" / "Raw mode"), backlinks count, Raw toggle, Download, and a row menu (Delete). The title lives in app chrome — NEVER written into the file body.
- `SearchBox` (context pane, under the pane title): input with 300ms debounce → `useKBSearch(q)`; results replace the tree while `q.length >= 2` (path + line + snippet, mark the match substring); click → `setParams({path: hit.path})`; Escape/clear restores the tree.

- [ ] **Step 1:** failing `header.test.tsx` — renders NoteHeader for `notes/trip plan.md`: breadcrumb shows `notes`, title input value `trip plan`; typing a new title + Enter fires the rename mutation with `{from:"notes/trip plan.md", to:"notes/summer.md"}` (fetch mocked); save-state chip text matches the `state` prop.
- [ ] **Step 2:** implement header + search; wire into KBPage/NoteEditor.
- [ ] **Step 3:** all tests + build; commit `feat(ui): UI-owned note header (breadcrumb, inline rename, save state) + KB search`.

---

### Task 7: Round-trip corpus, e2e smoke, docs

**Files:**
- Create: `web/ui/src/pages/kb/corpus.test.ts`
- Modify: `/home/user/simple-agents-v2/CLAUDE.md` (one line), ledger close-out by controller.

- [ ] **Step 1: Corpus test** — `corpus.test.ts` runs `checkFidelity` over a table of real-world snippets: the two memory-scaffold files' exact content (expected LOSSY → raw mode, per Task 3 — or clean if Task 3 flipped it; assert consistency with `editor.test.ts`), nested lists 3 deep, mixed task/bullet lists, tables with alignment, code fences with language + inner backticks, reference-style links, images `![alt](url)`, hard breaks, `# Skills: csv, pdf` header lines, `[[wikilinks]]` inline everywhere, em-dashes/unicode. For each: either fidelity=true, OR the test documents it as expected-lossy (a named `EXPECTED_LOSSY` list) — no silent failures. The point: the raw-mode fallback boundary is pinned by tests, so an editor-library upgrade that changes serialization gets caught.
- [ ] **Step 2: Manual e2e smoke** (report evidence, no automation): `make build && SA_PORT=8090 ./bin/simple-agents serve &`; with a browser-less check via curl confirm /app serves; then the REAL smoke is operator-driven — note in the report that the interactive editor smoke (type, slash menu, wikilink click, rename, search) is for Ilija on http://100.116.224.96:8090/app or after deploy. Kill the server you started.
- [ ] **Step 3: Docs** — CLAUDE.md `/api/v1` subsection: append `GET /api/v1/kb/resolve` to the kb line. Full suites (`go test ./... -count=1 -timeout 120s`, `npm test -- --run`, `npm run build`) green.
- [ ] **Step 4:** commit `feat(kb): markdown fidelity corpus + docs; KB editor complete`.

---

## Self-review notes (already applied)

- **Spec §6 coverage:** WYSIWYG on plain .md (Task 3), bubble toolbar + slash menu (4), UI-owned header incl. rename-renames-file (6), stylized tree with system muting + user-first ordering (2), wikilink pills + backlinks (5), search (6), raw toggle (3), autosave with save state (3/6). Drag-move + image upload = documented deferrals.
- **Fidelity-first design** goes beyond the spec's "raw toggle": lossy notes never silently save from WYSIWYG (Global Constraints) — this is the difference between an editor bug and agent-state corruption.
- **Type consistency:** `KBNode/KBNote/KBSearchHit` mirror `web/api_kb.go` DTOs byte-for-byte; `onStateChange` states enumerated once (Task 3) and consumed by name (Task 6); `slashItems`/`findWikilinkRanges`/`buildExtensions(extra)` defined before use.
- tiptap-markdown version-sensitivity is called out where it bites (Task 3 Steps 1/3) with mechanical-adjustment license, not hand-waving.
