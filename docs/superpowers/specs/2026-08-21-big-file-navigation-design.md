# Big-file navigation: showing the model the shape before it reads

Date: 2026-08-21
Status: design, part **A** of two. Part B (`2026-08-21-table-computation-design.md`)
depends on the schema this produces.

## The failure this comes from

An owner opened a chat on `notes/card-transactions.md` (155 KB, converted from a
CSV) and asked for a monthly total and their top five transactions. The reply
was **empty** — twice. The placeholder shipped in #245 made it visible rather
than a blank bubble, but the turn genuinely produced no text.

Measured, not assumed:

| | |
|---|---|
| Rows in the file | **98** |
| `apiTransaction` column (a raw JSON payload per row) | **131 KB — 88% of the file** |
| The nine columns that answer the question | **8.3 KB ≈ 2,000 tokens** |
| `read_file` cap | 8 KiB → **19 paged calls** to read it all |
| Chat turn budget (`maxAPITurns`) | 30 |

The model spent its budget paging a JSON blob it never needed, and then returned
an empty completion. **The file was never too big. The useful part is 8 KB.**

Two contributing defects are recorded here and fixed in this work:

- **`api_engine.go:162` treats an empty completion as a finished answer.** It
  returns `Result{Text: resp.Content}` with `StopReason: ""` — explicitly "was
  not cut short" — when the model emitted no tool calls and no text.
- **Nothing logs it.** A chat turn that succeeds logs no line at all, which is
  why the server log is silent about two failed turns. `agentrunner` gained
  exactly this line for exactly this reason (see "Runs log one `agentrunner: run
  finished` line" in CLAUDE.md); chat never did.

## The root cause is one thing, not three

It is tempting to treat tables, long prose and mixed markdown as three problems.
They are one: **`read_file` is a byte window, so for any file over the cap the
model must page blindly from offset 0.** It cannot ask what is in the file,
cannot jump to the part that matters, and cannot compute. A table is only the
most extreme case because a single junk column can be 88% of the bytes.

The machinery to answer prose questions already exists and is simply not
addressable per-file:

| capability | exists | reachable per-file |
|---|---|---|
| heading-aware chunking (`ChunkMarkdown`) | yes | no |
| BM25 ranked retrieval (`Indexer.Search`) | yes | no — whole vault only |
| exact match (`Searcher`) | yes | no — whole vault only |
| byte-range paging (`readFileSlice`) | yes | yes, but structureless |

So this is not "build retrieval". It is "make what we have addressable, and give
the model a map first".

## Design

### 1. `kb_file_map(path)` — the map

One new host tool. Returns what the model needs to plan a strategy *before*
spending turns, with the shape determined by content:

For a table:

```
notes/card-transactions.md — markdown table, 155 KB (~39k tokens)
98 rows × 18 columns

columns: date, merchantName, merchantCountry, USDAmount, originalAmount,
         originalCurrency, status, mcc, MCC Label, accountAmount, …

⚠ apiTransaction holds 88% of this file's bytes (131 KB).
  Selecting it will exhaust your context.

Reading this file whole costs ~19 calls. For totals or rankings use
kb_table_query. For specific columns use read_file with select.
```

For prose, the same call returns a heading outline with per-section byte costs.
For anything else (code, unknown), size plus the advice to use `read_file` with
`offset`.

**The warning line is the feature.** It is what stops the model paging, and it is
derivable — bytes per column for a table, bytes per section for prose. A column
or section over ~40% of the file gets flagged.

### 2. `read_file` gains `section:`

Fetch a heading's content rather than a byte range, resolved against
`ChunkMarkdown`'s existing heading trail. Byte paging stays for pathological
input; it stops being the only option.

### 3. `search_files` gains `path:`

Search within one file, reusing the index that already exists. **This is the
part with the traps**, and all three must be handled together.

- **All three retrieval paths must implement it.** `SearchKB` runs an exact pass
  and a ranked pass; the exact pass has two implementations. Scoping some and
  not others produces a search that was scoped to one file and answers from
  others.
- **The per-file match cap must differ from the whole-vault cap.** Both exact
  searchers hardcode 5 matches per file (`--max-count 5` in `searchRipgrep`,
  `count >= 5` in `searchGo`). That is right when searching everything — it
  stops one file dominating — and exactly wrong when the caller asked about one
  file. Scoped search raises it (`MaxHitsInFile`, 50).
- **The ranked pass is exclude-only.** `Indexer.SearchExcluding` filters
  prefixes *out*; there is no include. A `SearchWithin(workspaceID, query,
  limit, path)` is required, or the ranked half silently ignores the scope.

**The ripgrep/pure-Go split is a per-CALL fallback, not a per-host one.**
`Searcher.Search` tries `rg` and falls through to `searchGo` on *any* rg error.
So these are not "different machines": the same machine can take different paths
on consecutive calls. Divergence therefore shows up as nondeterminism on one
host, which is far harder to diagnose than a host-level difference. There is
currently **no test comparing the two** (`links_test.go` exercises `searchGo`
alone). This work adds one: the same scoped query through both, asserting the
same hits.

That is the same class of defect CLAUDE.md already records for the CLI bridge
missing the BM25 upgrade — "strictly worse retrieval for no reason a user could
see or control".

### 4. An empty completion is not an answer

`runToolLoop`'s final-answer branch gains a guard: if the model returns no tool
calls **and** no text, that is not a finished answer. It gets one nudge to
answer in words (bounded, mirroring `verifyFinishNudge`), and if it still
produces nothing the engine returns a `StopReason` saying so rather than an
empty `Result`.

Chat also gains a `chat: turn finished` log line carrying tool-call count, turn
count, whether the reply was empty, and token usage — the same line
`agentrunner` has, for the same reason.

## Both coder kinds

`kb_file_map` is a native tool for the API engine and reaches CLI coders through
the existing KB bridge, like `search_files`. Coder kind must not change what the
model can do.

## Testing

- **Fixtures from the real file.** The 88%-column case is the motivating input;
  a synthetic fixture with evenly-sized columns would pass a broken flag.
- **Searcher parity**: one scoped query, both implementations, same hits. This is
  the test whose absence is the risk.
- **Cap**: a file with 20 matches returns >5 when scoped, still ≤5 per file when
  searching the vault.
- **Ranked scope**: a query matching two files returns hits from one when scoped.
- **Empty completion**: a stubbed provider returning no content and no tool calls
  produces a non-empty `Result` with a `StopReason`, never `Text: ""`.
- **Map output** stays inside `maxToolResult` for a vault of any size — the
  outline is bounded, not one line per heading in a 500-heading document.

## Out of scope

- Computation over table values — part B.
- Trimming `req.Messages` mid-loop. It is a real gap (nothing trims history, so
  long tool loops grow unbounded) but it is a separate change to the engine's
  memory model, and this work removes the pressure that exposed it.
- Changing the 8 KiB `maxToolResult` cap. Raising it treats the symptom; the
  problem is that 131 KB of the payload was never wanted.
