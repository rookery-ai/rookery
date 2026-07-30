# Skill parity: one format, one viewer for core and user skills

**Date:** 2026-07-30
**Status:** approved

## Problem

Built-in (core) skills and user-created skills are the same kind of object — a
`SKILL.md` with YAML frontmatter plus an optional `scripts/` directory — but the
app describes and displays them as two different things.

**Format.** `BuildSkillImplementationPrompt` (`internal/prompts/prompts.go:2184`)
instructs the same frontmatter that core skills carry, but qualifies it with
"only name + description are strictly required". A generated skill can therefore
ship without `version`, `license`, or `category`, while every one of the 22 core
skills carries all three. Nothing validates or defaults the missing fields on
save.

**API.** `apiSkillListItem` and `apiCoreSkillListItem`
(`web/api_skills.go:40-60`) both expose only id/slug, name, and description.
Neither carries `category`, `version`, or the `requires` tool list — even though
`skilllibrary.ParseMeta` already parses all of it, and `LoadBundled()` already
returns it for core skills before the DTO throws it away.

**UI.** `web/ui/src/pages/skills/SkillDetailPage.tsx` holds two unrelated
components in one file:

- `SkillDetailPage` (user skills) renders the raw file in a monospace
  `<textarea>` with Save and Delete.
- `CoreSkillViewPage` (core skills) renders the file as ReactMarkdown, read-only.

So the same document is a code editor in one place and a formatted document in
the other, and there is no way to read a user skill as rendered markdown or to
view a core skill's source. In the list, `CoreSkillCard` is dashed-bordered,
`bg-chrome/50`, and `text-muted-2`, while `SkillCard` is solid and full-contrast:
built-in skills read as second-class, when in fact they are the skills every
workspace actually has.

## Goals

- One component renders both kinds, with the difference between them limited to
  what is genuinely different: a core skill's content is embedded in the binary
  and cannot be written to.
- The same metadata is surfaced for both, parsed by the same parser.
- A newly created skill's frontmatter matches a core skill's.

## Non-goals

- Making core skills editable. They are `go:embed`ed; there is no file to write.
  An "override a core skill" feature is a different design.
- Changing the skill format itself, or `ParseMeta`'s accepted shapes. It already
  handles both the current `metadata.requires` and the legacy
  `metadata.openclaw.requires` nestings.
- Skill editing or import over chat (a known gap, tracked separately).

## Components

### Server: one parser feeds both DTOs

Both list DTOs and the detail DTO gain the same three fields:

```go
type apiSkillMeta struct {
    Category string   `json:"category"`  // "" → rendered as "Other"
    Version  string   `json:"version"`
    Requires []string `json:"requires"`  // flattened bins + anyBins + env
}
```

Populated by running `skilllibrary.ParseMeta` over the skill's content in **both**
paths — `apiListSkills` for user skills (content via `s.skills`, the skill store)
and `LoadBundled()` for core. One parser means a user skill and a core skill with
identical frontmatter are described identically; two parsers would drift.

`requires` is flattened for display with its kind preserved as a prefix so the UI
needs no per-kind branch: `bins` entries render as-is, `anyBins` as
`"a or b"`, and `env` entries as `"$NAME"`. The flattening happens server-side
because `ParseMeta`'s nested shape is a Go detail the SPA should not learn.

`apiGetSkill` gains the same fields alongside the `content` it already returns, so
the detail view needs one request rather than a fetch plus a client-side YAML
parse.

Note on `apiListSkills` degradation: it already tolerates `s.skills == nil` (the
skill store not being configured) by returning skills without content. In that
case the metadata fields are zero-valued and the UI renders the same header with
"Other" and no version — the list must not fail over missing metadata.

### Server: generated skills carry full frontmatter

Two changes, deliberately split between "ask" and "guarantee":

1. **`BuildSkillImplementationPrompt`** drops "only name + description are
   strictly required" and requires `version`, `license`, and `category`, with
   `category` constrained to the value set core skills already use (File
   Processing, Agent Behaviour, Web & Research, Development, Productivity,
   Integrations, Meta, Other).

2. **`SkillSaver.SaveSkill`** parses the generated frontmatter and **fills
   defaults** for anything missing — `version: 1.0.0`, `license: MIT-0`,
   `category: Other` — rather than rejecting the save. A weak model omitting a
   field must not destroy a completed design conversation; the prompt is the
   ask, the defaulting is the guarantee. An unrecognised `category` value is
   replaced with `Other` rather than passed through, so the UI's grouping cannot
   be polluted by a hallucinated category.

The paste-import path (`POST /api/v1/skills`) gets the same defaulting, since a
pasted `SKILL.md` from elsewhere has the same gap.

### Client: one `SkillView`

`SkillDetailPage.tsx` is replaced by a shared `SkillView` component taking
`kind: "core" | "user"`. Both kinds render:

```
┌─ SkillView ───────────────────────────────────┐
│ pdf                            [Rendered|Raw] │
│ File Processing · v1.0.0 · Built-in           │
│ Needs: pdftotext or pandoc                    │
│ Use this skill whenever the user wants to     │
│ read, extract text from…                      │
│───────────────────────────────────────────────│
│ # PDF                                          │
│ Read, extract, merge, split…      (markdown)   │
└───────────────────────────────────────────────┘
```

- **Metadata header** — name, then a chip row `category · vN · Built-in|Yours`,
  then the required tools, then the description. Identical for both kinds.
- **Rendered / Raw toggle** — Rendered is ReactMarkdown with the existing safe
  configuration (`remarkGfm`, no `rehype-raw`, links forced to
  `target="_blank" rel="noreferrer noopener"`), so raw HTML in a `SKILL.md`
  renders as inert text rather than markup. This is the config
  `CoreSkillViewPage` already uses and it must carry over to user skills, whose
  content is model-generated and therefore less trustworthy, not more.
- **Raw** is the monospace view. Editable with Save/Delete for `kind="user"`;
  read-only for `kind="core"`.
- The toggle defaults to **Rendered** for both. A skill is a document meant to be
  read; the source is the secondary view even for the editable kind.

Unsaved-edit safety: switching from Raw to Rendered keeps the draft in state
(the existing `dirty` comparison against `skill.content` is unchanged), so the
toggle is not a way to silently lose an edit.

Routing is unchanged — `/skills/:id` and `/skills/core/:slug` both mount
`SkillView` with the appropriate `kind` and data hook (`useSkillDetail` /
`useCoreSkill`).

### Client: one card

`SkillCard` and `CoreSkillCard` collapse into one component with a
`Built-in`/`Yours` chip. Same border, same background, same text contrast. The
metadata chip row from the header appears in condensed form.

The two list *sections* stay separate ("Your skills" / "Built-in"), so grouping
and the existing empty-state logic (`showEmpty` gates the user section only) are
preserved. The change is that neither group is styled as lesser.

## Data flow

```
core:  go:embed SKILL.md ──┐
                            ├──► skilllibrary.ParseMeta ──► apiSkillMeta ──┐
user:  vault skills/<n>/ ──┘                                                │
       SKILL.md                                                             │
                                                                            ▼
                                                    SkillView (kind=core|user)
                                                      header: category·version·requires
                                                      body:   Rendered | Raw
                                                              (Raw editable iff user)

generation ──► BuildSkillImplementationPrompt (requires full frontmatter)
                        │
                        ▼
               SkillSaver.SaveSkill ──► ParseMeta ──► fill defaults ──► write
```

## Error handling

- `ParseMeta` failing on a user skill (malformed YAML the user pasted or edited)
  yields zero-valued metadata and the view still renders: header shows the name
  and "Other", body renders the content as-is. A skill must remain viewable and
  editable when its frontmatter is broken — that is exactly when the user needs
  to open it.
- An unknown `category` is coerced to `Other` on save, never rejected.
- `useCoreSkill` on an unknown slug already 404s; unchanged.
- Save on a core skill is unreachable from the UI, and the API has no core write
  route, so no server-side guard is added or needed.

## Testing

**`web` (Go)**
- Given one `SKILL.md` content, the user-skill DTO and the core-skill DTO expose
  the same metadata keys and values. This is the test that keeps the two paths
  from drifting.
- `apiListSkills` with `s.skills == nil` returns skills with zero-valued metadata
  and does not error.
- `apiGetSkill` returns category, version, and requires alongside content.

**`internal/skilldesigner`**
- A generated `SKILL.md` missing `version`/`license`/`category` saves
  successfully, and the persisted file carries the three defaults.
- An unrecognised `category` is coerced to `Other`.
- A malformed-frontmatter skill still saves (name comes from the validated slug,
  which `SaveSkill` already re-slugifies).

**`internal/prompts`**
- `BuildSkillImplementationPrompt` requires `category` and no longer contains the
  "only name + description are strictly required" phrasing.

**`internal/skilllibrary`**
- Existing `catalog_test.go` guarantees continue to hold (frontmatter parses,
  name equals directory, description carries triggers, no `scripts/` in core
  skills).

**`web/ui` (vitest)**
- `SkillView` with `kind="core"`: renders the metadata header; Raw tab is present
  and read-only; no Save or Delete control exists.
- `SkillView` with `kind="user"`: renders the same header; Raw tab is editable;
  Save is disabled until the draft differs from the loaded content.
- Toggling Rendered → Raw → Rendered preserves an unsaved edit.
- A skill with no category renders "Other" rather than an empty chip.
- The card renders a Built-in chip for core and Yours for user, with no
  dashed-border or muted-text variant.

## Accepted costs

- **Rendered-by-default is a behaviour change for user skills.** Someone used to
  landing directly in the editor now clicks Raw first. Justified: the same
  document should look the same in both places, and the read view is the more
  common intent.
- **Metadata parsing on every list request.** `apiListSkills` already reads each
  user skill's content from the store; `ParseMeta` on top is a frontmatter-only
  YAML parse over a handful of files. Not worth caching at this scale.
- **Defaulting rather than rejecting hides a weak model's omission.** A skill
  saved with `category: Other` and `version: 1.0.0` looks deliberate. The
  alternative — failing the save — loses a whole design conversation over a
  cosmetic field, which is strictly worse.
