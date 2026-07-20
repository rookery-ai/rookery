# Agent Files as Documents — Design

**Date:** 2026-07-17
**Status:** Approved (brainstorming session)
**Scope:** Sub-plan 7 of the post-redesign track. SP8 (everyday feel) and SP9 (power & creation) are separate specs.

---

## 1. Problem

Everything an agent writes lands in the user's vault, but the knowledge base can only open `.md` files (`isFile = path.endsWith(".md")`). So the two files that describe an agent's life — `agent.json` (manifest) and `state.json` (memory between runs) — are visible in the tree and dead on click. The same is true of `tools/*.py` and skill `scripts/`, which can never be markdown.

The goal is that **everything an agent produces is readable in the knowledge base**, and the files that are genuinely *documents* become documents.

## 2. Goals

- `state.json` becomes `state.md` — a readable, hand-editable document with the machine state in a fenced block.
- `agent.json` disappears; its one unique field moves into AGENT.md's existing header convention.
- The KB opens non-markdown files (code, data, text) in a read-only view; binaries offer download.
- Existing agents migrate automatically and idempotently, without losing state.
- The agent runtime's output contract (`[STATE]` markers) is unchanged.

## 3. Non-goals

- Changing the `[STATE]` protocol or how agents emit state. Agent *output* stays exactly as it is; only the file on disk changes shape.
- Editing `tools/*.py` from the KB. The designer is the sanctioned path for changing agent code because it re-tests the result.
- Converting `.kb/db-export/*.json` sidecars. They are hidden internal reflection data, not agent-authored files.
- Rendering CSV/JSON as tables or trees. Monospace text is the deliverable.

## 4. `state.md`

### 4.1 Format

````md
# State — <Agent Name>

_Managed by Simple Agents. The block below is this agent's memory between runs —
edit it if you need to fix something by hand._

```json
{ "last_seen_id": 12345, "cursor": "2026-07-17T09:00:00Z" }
```

## Notes

<optional agent-written prose>
````

Design constraints behind this shape:

- **No HTML comments.** The fidelity corpus proves they do not round-trip through the editor and would force `state.md` into raw mode permanently. Italic prose round-trips clean.
- **Fenced `json` blocks are pinned CLEAN in the corpus**, so `state.md` opens in the normal WYSIWYG editor.
- The `## Notes` section gives the agent somewhere to write human-facing context. It is optional and never machine-parsed.

### 4.2 Parsing contract

- **Read:** the state object is the content of the **first ` ```json ` fence** in the file. No fence → state is `{}`, logged at warn level (self-healing; the next write appends one).
- **Write:** replace only that fence's content. Everything outside it — heading, prose, `## Notes` — is preserved byte-for-byte. A file with no fence gets one appended after the intro paragraph.
- **Seed:** a new agent's `state.md` is the template above with `{}`. This replaces the `{}`-seeded `state.json` the runner writes into its sandbox temp dir.
- The runner injects the parsed JSON into the coder prompt exactly as it does today.

### 4.3 Editing safety

`state.md` is editable in the KB, because hand-fixing a stuck cursor is the reason to convert it at all.

`PUT /api/v1/kb/note` returns **409 `agent_running`** when the path matches `agents/<agentID>/state.md` and that agent has a run in flight (the same in-memory/DB check the run tracker already exposes). The guard lives in the backend, not the UI, because the runner is server-side and the frontend cannot be trusted to know. The SPA surfaces the envelope message inline in the editor.

## 5. `agent.json` — removed

`AgentManifest` carries `ID`, `Name`, `RequiredSecrets`, `Skills`, `CreatedAt`. `Skills` is deliberately empty since the skills-attachment cutover (the `agent_skills` table is the source of truth); `ID`/`Name`/`CreatedAt` all live in the `agents` table. Only `RequiredSecrets` is unique data.

- `RequiredSecrets` moves into AGENT.md as a header line: `# Secrets: SENDGRID_KEY, GH_TOKEN`.
- Parsing mirrors `parseSkillsLine` — tolerant of case, separators, and formatting drift; a missing header means "none declared".
- The writer is whatever populates `manifest.RequiredSecrets` today (the designer's save path).
- `AgentManifest`, `LoadManifest`, and `AgentManifestPath` are deleted; all callers move to the header or the DB. The agent detail page's missing-secrets warning reads the parsed header.

## 6. Migration

Idempotent, runs at startup **before the scheduler starts**, following the existing `MigrateLegacyLayout` pattern. For each workspace vault, for each `agents/<id>/`:

1. If `state.json` exists and `state.md` does not: write `state.md` with the JSON contents in the fence, read it back and verify the parsed object equals the original, **then** delete `state.json`.
2. If `agent.json` exists: when `RequiredSecrets` is non-empty and AGENT.md has no `# Secrets:` header, insert one alongside the other headers; then delete `agent.json`.
3. Any failure at any step leaves both files in place and logs loudly. State loss is the one unacceptable outcome.

Draft agent dirs (`draft_<slug>`) are migrated on the same terms.

## 7. KB opens code and data files

The note endpoint gains a discriminator so the SPA knows how to render:

- `kind: "markdown"` — today's behavior, the WYSIWYG/raw editor.
- `kind: "code"` — **any file whose content is valid UTF-8 and under 1 MB** (in practice `.py`, `.json`, `.yaml`, `.txt`, `.sh`, `.sql`): read-only monospace view, preserved whitespace, with the existing UI-owned header (breadcrumb, path, Download, Delete) and no save affordance. Content sniffing, not an extension allowlist — agents invent file types.
- `kind: "binary"` — content that is not valid UTF-8, **or** any file over 1 MB: a "Binary file — Download" panel rather than dumping bytes.

`kb/raw` already serves the bytes and is unchanged; it backs the Download link.

## 8. Prompt changes

`platformContextBlock` and `BuildCoderPrompt` currently tell agents their state lives in `state.json`. They change to describe `state.md`: the JSON fence is the memory, the `## Notes` section is theirs to write, and the `[STATE]` output marker is unchanged. The wording must not imply agents should hand-edit the fence — they emit `[STATE]`, the runner writes the file.

## 9. Testing

- **Round-trip:** merge → write → read yields an identical object; prose and `## Notes` outside the fence survive a write.
- **Self-healing:** a `state.md` with no fence parses as `{}` and gains a fence on the next write.
- **Migration:** idempotent across repeated runs on a seeded vault (JSON present, MD present, both present, neither present); a forced write failure leaves both files intact.
- **Fidelity:** a corpus entry pinning the `state.md` template as clean, so an editor upgrade that breaks it fails loudly.
- **End-to-end:** a runner test where an agent writes state on run 1 and reads it back on run 2.
- **Guard:** `PUT kb/note` on a live agent's `state.md` returns 409 `agent_running`; on an idle agent it saves.
- **KB kinds:** a `.py` file returns `kind:"code"` and renders read-only; an oversized/binary file returns `kind:"binary"`.

Manual verification after merge: one of the operator's real agents runs and its state survives the migration.

## 10. Risks

| Risk | Mitigation |
|---|---|
| Migration loses agent state | Verify-then-delete; failure leaves both files; runs before the scheduler |
| A run writes state while the user edits `state.md` | Backend 409 guard on the save path |
| Editor mangles the JSON fence | Fences pinned clean in the corpus; the fidelity gate forces raw mode if that ever changes |
| Agents keep emitting the old shape | Protocol unchanged by design — nothing to relearn |
| A caller of the deleted manifest is missed | `go build` is the completeness proof; the same method used for the template deletion |
