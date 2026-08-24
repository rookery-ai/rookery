# Build and dry-run KB write journal

*2026-08-24*

## The bug

An agent **build** and the create-build **dry run** write into the user's live
knowledge base. Observed twice against a real note: the file's inode and md5
changed while the build ran, and the user's content was replaced by the
rehearsal's.

`internal/agentdesigner/dryrun.go` asserts the opposite:

> dryRunPrompt deliberately passes NO VaultRoot, and that omission is the only
> thing keeping a rehearsal out of the user's live knowledge base

That is false. Withholding `VaultRoot` only withholds the `<agent_workspace>`
prompt block. It removes a *description* of the vault, not *access* to it — and
three separate channels grant that access regardless of what the prompt says.

## Three write channels, not one

| # | Channel | Why it reaches the user's notes |
|---|---------|--------------------------------|
| 1 | `write_file` / `edit_file` | `hostToolSet.resolveVault` (`internal/coder/hosttools.go`) validates the resolved path against the **vault root**, never against `workDir`. Both the absolute branch and the relative branch do this, so any path under the vault is writable. |
| 2 | `save_to_kb` | Writes into the knowledge base *by contract*. It is not gated by `includeExecTools`, so a build and a dry run are both offered it. |
| 3 | `bash` / `run_script` | `buildScriptCommand` builds a Landlock `Spec` per invocation whose `ReadWritePaths` includes `filepath.Join(h.dataDir, "vaults", h.workspaceID)` — the whole vault, read-write. |

The tool *descriptions* advertise this. `write_file` says "Path is relative to
the vault root, or absolute within the vault"; `save_to_kb` says "file it in the
user's knowledge base". The model is not escaping confinement — it is doing what
its tool contracts invite. There was never any containment to defeat.

## What we are changing, and what we are not

**In scope.** A build (`Flow.runGeneration`) and a dry run (`Flow.dryRun`) must
leave the user's knowledge base byte-identical to how they found it, so the
agent's first real run starts from a clean slate. All three channels above.

**Out of scope, deliberately.**

- *Preventing* the writes. A rehearsal that cannot write the KB cannot rehearse
  a KB-writing agent, which is most of them. The build is supposed to run real
  end-to-end tests; we keep that and undo the side effects instead.
- Real agent runs and one-off chat. They write the KB directly on purpose
  (see CLAUDE.md, "Agent access model"). Their behaviour is unchanged.
- Removing `agents/<id>/notes/`. Nothing in production creates that directory
  and the `<agent_workspace>` prompt block already tells agents not to keep a
  second copy of a user-facing note there, so the layout is left alone rather
  than churned for no functional gain.

## Mechanism: a write journal, reverted per phase

`vault.WriteJournal` records the prior state of every protected-region path a
rehearsal touches, so the whole lot can be undone when the rehearsal finishes.

It lives in `internal/vault` beside `Guard` so it can reuse `isProtected` and the
content-bearing `Guard.Snapshot` walker rather than forking a second definition
of "the user's authored knowledge".

```go
type WriteJournal struct { ... }

func (v *Vault) NewWriteJournal(workspaceID string) *WriteJournal

// Record captures a path's current state before the caller overwrites it.
func (j *WriteJournal) Record(abs string) error

// AroundExec brackets an opaque call: snapshot, run, fold whatever changed in.
func (j *WriteJournal) AroundExec(fn func() error) error

// Revert undoes every recorded change, newest first.
func (j *WriteJournal) Revert() ([]string, error)
```

**First write wins.** A path already in the journal is never re-recorded, so the
state restored is the one from before the rehearsal began, not from before the
most recent of several writes.

**Nil-safe throughout**, matching `Guard`. The journal is non-nil *only* for a
build and a dry run; every other surface passes nil and is untouched by
construction. That is what keeps the blast radius small.

### Per-channel wiring

- **Channels 1 and 2** — `writeFile`, `editFile` and `saveToKB` call
  `j.Record(abs)` before writing. Exact, no guessing, no window.
- **Channel 3, API engine** — `runScript` and `runBash` wrap the subprocess in
  `j.AroundExec`. A snapshot immediately before and a diff immediately after
  keeps the attribution window to *one script call* — seconds.
- **Channel 3, CLI engine** — a CLI coder writes from a subprocess with no host
  tools to instrument, so there is no finer signal available. `Coder.Generate`
  brackets the whole call in `j.AroundExec` when a journal is set and the engine
  is not the API one. The window is the whole build; that is a real limitation,
  recorded rather than hidden.

### The concurrency window

`AroundExec` attributes *every* protected-region change during its bracket to
the rehearsal. A legitimate concurrent write — a chat turn, a scheduled agent —
landing inside that bracket would be reverted with it.

This is why the API engine journals per script call rather than around the whole
rehearsal: a dry run is a full agent run measured over 1.5M tokens and lasting
minutes, and reverting a minutes-wide diff would trade a data-integrity bug for
a worse one. Narrowing to the individual call reduces the exposure to seconds.
For a CLI coder the wide window is unavoidable and is stated above.

### Directory pruning

Reverting a *created* file leaves its parent directories behind. `Record` notes
which ancestor directories did not exist at record time; `Revert` removes those,
and only those, and only while they are empty. A directory the user already had
is never removed, even if the revert leaves it empty.

## Consequences

**`dryRunPrompt` gains `VaultRoot`.** This deliberately inverts the comment at
`dryrun.go`. That omission never provided containment; all it did was make the
rehearsal less faithful than the real run it is meant to predict. With the
journal in place, the rehearsal can be given the same prompt a real run gets.
The comment is rewritten to point at the journal.

**The review message can say something true.** "Wrote `notes/weather.md`, then
rolled it back" answers what a build is allowed to prove: it proves the real
thing and leaves no trace.

## Failure modes

- **Revert must run on every exit path** — success, error, `[BLOCKED]`, coder
  timeout, and `Cancel()`. It is deferred at the point the journal is created.
- **A build followed by a dry run uses two independent journals**, each reverted
  at the end of its own phase, so the dry run genuinely starts from a clean KB.
- **`save_to_kb` writes two files** — the note plus the preserved original — so
  journaling happens at the filesystem paths it touches, not at the note path
  alone.
- **A failed revert must not fail the build.** The build is already on disk and
  past its guardrails. A revert error is logged loudly and the build proceeds,
  matching the dry run's existing best-effort contract.

## Testing

Unit tests over the journal (create/modify/delete round-trip, first-write-wins,
directory pruning, nil-safety), plus per-channel tests that a build-profile
`hostToolSet` leaves the protected region unchanged.

CI is not sufficient on its own here: 17 green checks once missed three designer
defects that a single live build surfaced in 25 minutes. Verification includes a
real build and a real dry run against a running server, with the KB hashed
before and after.
