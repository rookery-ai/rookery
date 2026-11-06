# Rename agents and skills

## Why

A name stops being true. An agent called `Three-city forecast` that now only covers two
cities, or a name that was simply too long, can only be fixed today by deleting the agent
and building it again, which loses its run history, schedule and memory.

Renaming changes the **label**. It must not change what the agent or skill does, and it
must not break anything that finds them by name.

## Goals

- Rename an agent or a user skill from its page.
- After a rename, nothing that works by name is broken, and the old name no longer shows
  in any place the platform generated.
- A rename either happens or is refused with a reason the owner can act on.

## Non-goals

- Changing behaviour. If an agent's instructions mention the old scope, that is an edit.
- Rewriting prose. A sentence that mentions the old name stays as written. The response
  lists those files so the owner can look at them.
- Renaming core skills: they are embedded in the binary and have reserved names.
- A chat-platform `/rename` command.
- Renaming a draft that has not been saved yet.

## Anti-goals

- Blind find-and-replace over the knowledge base. A one-word skill called `notes` must
  not turn every `notes/` path in the vault into something else.
- A rename that half-applies silently.
- Rewriting the audit log. It is a record of what happened, including the old name.

## The rule

**Structural references are rewritten. Prose is left alone.** A structural reference is
one the platform generated or resolves by name.

### Agent: what changes

| Where | How | Why |
|---|---|---|
| `agents.name` | transaction | source of truth |
| `inbox_messages.agent_name`, `pending_actions.agent_name` | transaction, by `agent_id` | denormalised labels |
| `agent_drafts.agent_name` (this agent's edit draft) | transaction | label on the resume prompt |
| `agent_drafts.pending_agent_md` (any draft) | `[CALL: old]` rewritten, in the transaction | would otherwise be saved back with the old call |
| `[CALL: old]` in every agent's `AGENT.md` | literal, case-sensitive | the runner resolves calls with `GetAgentByName`, an exact match; a stale one is "agent not found" |
| This agent's `AGENT.md` title | only a `# old` line | a generated title, not prose |
| `agents/<id>/state.md` | only the first line, when it is `# State — old` | never re-rendered, and never created if absent |
| `agents/<id>/logs/run_*.md` | only the `# Run of [[old]]` heading | the run's model output stays as written |
| `.kb/db-export/agent_runs/*.json` for this agent | `AgentName` field | mirror of the run note |
| `[[old]]` wikilinks in the vault | see below | |

The agent's directory is keyed by UUID and does not move. Schedules, runs, connections
and skills are keyed by id and need nothing.

### Skill: what changes

| Where | How | Why |
|---|---|---|
| `skills.name`, `skills.description` | transaction | source of truth; the description takes the same reference rules as the skill's files |
| `agent_skills.skill_name` | `UPDATE OR IGNORE`, then delete what is left | keyed by name with no foreign key, so a dangling row with the new name may already exist |
| `agent_drafts.pending_agent_md` | same rules as `AGENT.md`, in the transaction | an unknown name in `# Skills:` is dropped with only a warning |
| `skills/<old>/` | `os.Rename` | the runner reads `SKILL.md` from this path |
| `skills/.staging-<old>/` | removed | orphaned: a draft with this name refuses the rename |
| Text files in the skill, and in every agent's `AGENT.md` and `tools/` | see below | |
| `<home>/.local/share/rookery/envs/<old>` | removed | see below |
| `[[old]]` wikilinks, and links under `skills/old/` | see below | |

**Reference rules for skill names.** A name with a hyphen is distinctive, so every
whole-token occurrence is a reference to it. A name without a hyphen may be an ordinary
word, so only these forms are rewritten: the `name:` frontmatter line, `skills/old`,
`envs/old`, `env="old"`, `env=old`, `--env old`, and the `# Skills:` header (inline or
its bullet list). A token boundary is any character outside `[A-Za-z0-9_-]`.

**The Python environment is deleted, not moved.** Virtual environments hold absolute
paths, so a moved one is subtly broken. The runtime prompt already reports an environment
that is not installed and tells the agent to install it, so deletion heals itself at the
cost of one slower run. The response says this happened.

Its `rookery-env.json` records which agent asked for an install. That is provenance, like
the audit log, and goes with the environment.

### Wikilinks

Files scanned: every `.md` in the vault except `chats/` (regenerated from the database),
`.kb/` and `agents/*/logs/` (machine transcripts).

- `[[old]]`, `[[old|alias]]` and `[[old#heading]]` are rewritten case-insensitively,
  **only when `old` does not resolve to a real note**. A link to `notes/Weekly digest.md`
  must keep pointing at that note when an agent called `Weekly digest` is renamed.
- For a skill, `[[skills/old/…]]` and markdown link destinations under `skills/old/` are
  also rewritten, because the folder moved.

### Left unchanged, on purpose

Audit log rows, `agent_runs` transcripts and output, chat messages, draft conversation
history, an environment's install manifest, and any prose.

The response's `still_mentions` lists up to 20 vault files that still contain the old
name, outside the excluded folders above.

## Validation

**Agent names** are trimmed, 1–120 characters, with no control characters and none of
`[ ] | #`, which would break a wikilink or a `[CALL: …]` line. A collision is checked
case-insensitively against the workspace's other agents, because `/agent stop` matches
names that way. The agent itself is excluded, so a case-only rename works.

**Skill names** go through `skillstore.SanitizeName` and the existing name rule. A name
that belongs to a core skill, another skill, or an existing folder is refused. The folder
check is skipped when the old folder is already gone, so that a rename interrupted between
the move and the commit can be retried.

An unchanged name returns 200 and does nothing.

## When a rename is refused (409)

| Condition | Why |
|---|---|
| The agent is running, or an agent referring to it is | a run already holds its prompt, so `[CALL: old]` would fail |
| An agent using the skill is running | its environment is about to be deleted |
| An agent design session is open | it holds `AGENT.md` and the skill list in memory and would save them back |
| A skill design session or draft has the old or the new skill name | saving a skill overwrites a same-named one in place |

Each carries a sentence naming what to do. The running check is check-then-write, the same
shape as the `state.md` 409 guard. A run starting in the gap loses nothing: at worst its
note uses the old name.

## Order of operations

**Agent:** read and plan every file edit, then commit the transaction, then write the
files. **Skill:** plan, move the folder, commit the transaction (moving the folder back if
that fails), then write the files and delete the environment.

Plan first so that the agent-and-skill guards see who refers to the name. Commit before
file edits because the database decides what works; every file edit is safe to repeat.
A failed file write is logged and returned in `warnings`, not treated as a failed rename,
because the rename has happened by then.

Writes are atomic per file and keep the file's mode. Only changed files are written.
Non-UTF-8 files, symlinks and files over 5 MiB are skipped.

The KB search index is invalidated afterwards.

## API

```
PUT /api/v1/agents/:id/name   {"name": "..."}
PUT /api/v1/skills/:id/name   {"name": "..."}
→ 200 {"id", "name", "still_mentions": [], "warnings": [], "env_removed": false}
```

`still_mentions` and `warnings` are always arrays, never `null`. Errors: 400
`invalid_name`, 404 `not_found`, 409 `name_taken` / `agent_running` / `design_in_progress`.

## Interface

"Rename…" sits in the agent page's actions menu and beside Delete on a user skill's page,
opening one shared dialog. Refusals show inline in the dialog. On success a toast reports
files still mentioning the old name and a removed environment. Rename is disabled while a
skill has unsaved edits, since saving them afterwards would write the old name back.

The browser refreshes agents, skills, the KB tree, open notes and the inbox afterwards. An
open KB note that kept its old content could otherwise autosave it back over the rewrite.

## Decisions made by the builder

- 120-character limit on agent names, and the forbidden character set.
- Removing the orphaned staging folder.
- Refusing on *any* open agent design session rather than working out whether that session
  uses the name. Renames are rare, and cancelling a session is one click.
- `still_mentions` as the way to surface prose instead of offering to edit it.
