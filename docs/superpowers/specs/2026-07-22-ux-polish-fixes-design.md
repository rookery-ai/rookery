# UX polish: progress lines, search titles, delete cleanup, frontmatter, KB recents

**Date:** 2026-07-22
**Status:** approved

Five independent user-reported defects. Each is scoped to its own section; they share
no state and can be implemented and reviewed in any order.

## 1. Tool progress lines leak host paths and raw JSON

**Symptom**

```
🔧 bash({"command": "cd /home/user/.simple-agents-v2/vaults/fd11c4…)
```

**Cause** — `toolMilestone` (`internal/coder/api_engine.go`) extracts a detail
string from `path`/`query`/`pattern`/`url` only. `bash` takes `command`, which
matches none of them, so the function falls through to dumping the raw JSON
arg blob. `run_script` is NOT affected: it takes a `path` argument and already
renders as `🔧 run_script(tools/foo.py)`.

Two defects compound: the missing `command` case, and the fact that the command
text itself embeds the absolute vault path the model typed.

**Fix**

- Add `command` to the extracted fields.
- Add a shortening pass applied to every detail string, whatever field it came
  from: rewrite an absolute vault path to its vault-relative form, and the
  workspace home to `~`. This also covers a `read_file` call where the model
  typed an absolute path.
- `toolMilestone` becomes a method on the type holding the vault root, since it
  now needs that root. Keep the formatting logic pure and separately testable.

One change covers both delivery surfaces — Telegram and the web SSE stream share
the same `WithProgress` sink.

**Out of scope** — no other database ID was found leaking into user-facing
progress output; the reported IDs were all vault-path UUIDs, which this fixes.

## 2. Global search shows UUID filenames

**Symptom** — searching shows `chats/33139123-6939-4d12-9bf8-409b2e042d24.md`.

**Cause** — only the `notes` group is affected. It is backed by the ripgrep
searcher, which returns real vault files, including reflected ones whose
*filename is a UUID*: `chats/<id>.md`, `inbox/<id>.md`,
`agents/<id>/logs/run_<ts>.md`. `web/api_search.go` sets `Title` to the raw path.

Showing "path and file name" alone does not solve this — for these files the
filename *is* the UUID. The title has to be resolved from content or the DB.

**Fix** — extract `kbDisplayTitle(workspaceID, path) string` from the existing
`enrichKBDisplayNames` (`web/handlers_kb.go`). Critically it is keyed on the
**full path**, not the immediate parent directory as the current enricher is, so
it resolves nested paths like `agents/<id>/logs/run_<ts>.md` that the current
implementation cannot reach.

Resolution rules, by path shape:

| Path | Title |
|---|---|
| `agents/<id>/logs/run_<ts>.md` | `<agent name> — run <ts>` |
| `agents/<id>/<file>` | `<agent name> — <file>` |
| `chats/<id>.md`, `inbox/<id>.md`, `reminders/<id>.md`, `memory/<f>.md` | first `# ` heading |
| anything else | filename stem |

Both `enrichKBDisplayNames` and the search handler call it, so tree labels and
search titles cannot drift apart.

Search results render the resolved title as the primary line and the full path,
dimmed, as the secondary line — both stay visible.

## 3. Deleted items persist in the knowledge base

**Cause** — deletes are DB-only for chats and inbox messages, so the reflected
note and its `.kb/db-export/<table>/<id>.json` sidecar survive. Agent delete
already removes the agent directory but orphans that agent's run sidecars.

**Fix** — one shared helper on the reflector:

```go
func (r *Reflector) Unreflect(workspaceID, relPath, table, id string) error
```

removing both the note and the sidecar, tolerant of either being already absent.
Wired into the chat, inbox, and skill delete handlers; agent delete keeps its
existing `RemoveAll` and additionally drops that agent's run sidecars (identified
by reading each sidecar's `AgentID`).

**No index work needed** — the BM25 retrieval index rebuilds its file set on each
search and drops paths it no longer sees (`internal/vault/index.go`), so a
deleted file leaves the index without intervention. Verified by reading the
revalidation path, not assumed.

## 4. Reflected notes will not open in the rich text editor

**Symptom** — inbox notes open in raw markdown mode.

**Cause, established by probe rather than inspection** — YAML frontmatter is the
*sole* blocker. `---` parses as a horizontal rule and the following key lines
become a setext `##` heading, so `checkFidelity` fails and `NoteEditor` falls
back to raw. Running the real note shapes through `checkFidelity` with the
frontmatter removed:

| Note body | Fidelity |
|---|---|
| inbox, with frontmatter | ✗ |
| inbox, frontmatter stripped | ✓ |
| chat transcript | ✓ |
| agent run log | ✓ |

Every reflected body is already rich-text-safe. Only the YAML block breaks it.

**Fix — editor-side, not reflector-side.** `NoteEditor` splits a leading
`---…---` block off before choosing its mode, runs `checkFidelity` and the
WYSIWYG editor on the **body only**, and re-prepends the block byte-for-byte on
save. The frontmatter renders as a collapsed meta strip above the document. Raw
mode continues to show the whole file, YAML included.

Chosen over changing the reflector because it fixes **notes already in the
vault** — inbox, chats, run logs, reminders — with no migration, and covers
imported notes carrying frontmatter from elsewhere. Nothing in the Go codebase
parses frontmatter back out of a reflected `.md` (verified: `internal/memory`
strips it from legacy notes, `internal/skilllibrary` parses `SKILL.md`, neither
touches reflected notes), and the `.kb` sidecar already holds the structured
value losslessly, so the block is free to be treated as opaque bytes.

**Preservation contract** — the frontmatter block is never parsed, reformatted,
or re-serialized. It is sliced off as a string and concatenated back unchanged.
A note that opens and saves with no edit must be byte-identical.

## 5. Knowledge base opens empty; no recent files

**Requirement (as clarified)** — "Recent" means files the user has **manually
clicked in the UI**, most-recently-viewed first. It is a view history, not an
mtime listing. This deliberately excludes everything an agent writes: agent run
logs, reflected chats and inbox notes are the most recently *written* files by
far, and an mtime-ordered list would be a wall of `run_<ts>.md`.

**Fix** — a `useRecentFiles` hook backed by `localStorage` under `sa.kb.recent`,
following the existing `usePaneWidth` precedent (corrupt or unparseable stored
value falls back to empty rather than throwing).

- Entries are `{path, title}`. The title is captured at click time from the
  caller, which already knows it — `FileTree` has `node.display_name`, search
  has the hit path — so no extra request is needed.
- Written only on a **file** click, never a directory, and never on programmatic
  navigation.
- Clicking an existing entry moves it to the front. Store 10, display 5.
- Rendered above the file tree in the context pane.

**Auto-open** — landing on `/kb` with no `?path=` navigates to entry 0 with
`replace: true`, so Back does not bounce between the bare route and the note. An
empty history keeps the current "Select a note or create one" empty state.

**Stale entries** — an entry whose file no longer exists is dropped lazily when
its note fetch returns 404. In-UI renames update the entry through the existing
`handleMoved` hook.

## Refinements found during implementation

Four things the design above did not anticipate, each caught by review or by a
test rather than in production:

1. **`save_to_kb` had defect #1 too.** Its subject arrives as `source`, matching
   none of the extracted fields, so it hit the same raw-JSON fallback as `bash`.
   Added to the same extraction chain.

2. **The KB page's own search box shares defect #2.** It is a separate surface
   (`/api/v1/kb/search`, `pages/kb/SearchBox.tsx`) from the ⌘K palette and
   rendered `hit.path` as the label — identical UUID titles. `apiKBSearchHit`
   gained a `title` field from the same `kbDisplayTitle`.

3. **Recents must be scoped per workspace (#5).** Entries are workspace-relative
   paths, and the platform's model is one owner switching between isolated
   workspaces. A single `sa.kb.recent` key would show workspace A's notes in
   workspace B and — worse than a visible error — auto-open B's same-*named*
   file under A's title, which looks correct and is not. The key is now
   `sa.kb.recent.<workspaceID>`, read from the session. Because the id arrives
   asynchronously, the hook loads on workspace change rather than at mount, and
   guards the persist effect so the empty initial state cannot be written over a
   stored list.

4. **Frontmatter detection needed a `key: value` requirement (#4).** Treating any
   leading `---…---` as metadata absorbs the heading of a note that opens with a
   setext heading:

   ```
   ---
   Chapter 1
   ---
   ```

   That text would then render nowhere — the metadata strip only displays parsed
   pairs, and the editor only sees the body — which is *worse* than the original
   bug, where the note opened in raw mode with every line visible. Detection now
   requires at least one `key: value` line. Every reflected note qualifies; a
   setext heading never does.

## Testing

| Area | Test |
|---|---|
| 1 | Go: `bash` command extraction; absolute-vault-path shortening; existing cases unchanged |
| 2 | Go: `kbDisplayTitle` per path shape, including the nested agent-log path |
| 3 | Go: `Unreflect` removes note + sidecar, tolerates missing either |
| 4 | vitest: frontmatter split/restore is byte-identical; body-only fidelity on the real inbox note shape |
| 5 | vitest: MRU ordering, dedupe, cap, corrupt-storage fallback |

The frontmatter case gets an explicit regression test built from the real inbox
note shape, since that is the defect actually reported.
