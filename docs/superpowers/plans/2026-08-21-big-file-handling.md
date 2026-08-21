# Big-File Handling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a model answer questions about a large knowledge-base file without paging it blindly — by showing it the file's shape first, letting it fetch precisely, and computing arithmetic host-side.

**Architecture:** One root cause, two parts. `read_file` is a byte window, so any file over the 8 KiB cap forces blind paging from offset 0. Part A adds a map (`kb_file_map`), section reads, and per-file search — reusing the heading chunker and BM25 index that already exist. Part B adds `kb_table_query`, a parameters-in/rows-out aggregator computed in plain Go.

**Tech Stack:** Go 1.26 (`GOTOOLCHAIN=auto`), existing `internal/vault` (ChunkMarkdown, Indexer, Searcher), `internal/coder` host tools, the KB loopback bridge for CLI coders.

**Spec:**
- Part A — `docs/superpowers/specs/2026-08-21-big-file-navigation-design.md`
- Part B — `docs/superpowers/specs/2026-08-21-table-computation-design.md`

## Global Constraints

- **`GOTOOLCHAIN=auto` on every Go command.** Host Go is older than `go.mod` requires; bare `go test` fails outright.
- **Branch, never commit to `main`.** Conventional Commits (`type(scope): summary`).
- **Every tool result stays under `maxToolResult` (8 KiB)** — that bound is the contract callers depend on.
- **A tool result is never empty.** An empty string breaks strict provider serializers; return a non-`error:` notice instead ("(no matches)").
- **A "no results" outcome is NOT an `error:` string.** The API engine's oscillation guard counts `error:` as a failing call.
- **Both coder kinds reach every new capability** — native tools for the API engine, the KB bridge for CLI coders.
- **Never touch `rookery.db`** from any query path. Table computation reads one vault file, in memory.
- **A slice field on a DTO must never marshal to `null`** — initialise with `[]T{}`.
- Full gate: `GOTOOLCHAIN=auto make ci`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/vault/filemap.go` (new) | `FileMap(v, workspaceID, rel)` — classify a file and describe its shape. Pure; no tool plumbing. |
| `internal/vault/table.go` (new) | Parse a markdown table into rows; column byte-weights; `Query` (filter/group/aggregate/sort/limit). Pure. |
| `internal/vault/search.go` (modify) | Per-file scoping in both exact searchers; separate per-file cap. |
| `internal/vault/index.go` (modify) | `SearchWithin` — the include-path counterpart of `SearchExcluding`. |
| `internal/vault/kbsearch.go` (modify) | Thread a path scope through the two-pass search. |
| `internal/coder/hosttools.go` (modify) | Register + dispatch `kb_file_map` and `kb_table_query`; `section`/`path` args. |
| `internal/vault/bridge.go` (modify) | Bridge endpoints so CLI coders reach the same calls. |
| `internal/coder/api_engine.go` (modify) | An empty completion is not a finished answer. |
| `web/chat_turn_tracker.go` (modify) | Log one line per finished chat turn. |

Parsing and querying live in `internal/vault` (pure, testable without a coder); only registration and dispatch live in `internal/coder`. That mirrors how `SearchKB` is structured today.

---

### Task 1: An empty completion is not a finished answer

**Files:**
- Modify: `internal/coder/api_engine.go:162` (the final-answer branch)
- Test: `internal/coder/api_engine_test.go`

**Interfaces:**
- Produces: `Result.StopReason` gains the value `"empty"`.

Background: the branch returns `Result{Text: resp.Content}` and sets `StopReason: ""` — explicitly "was not cut short" — even when the model emitted no tool calls and no text. That is how two dead chat turns were recorded as successes. Fix this first: it is independent, it is what made the original bug invisible, and every later task benefits from failures being legible.

- [ ] **Step 1: Write the failing test**

```go
// A model that stops calling tools AND says nothing has not answered. Returning
// that as a successful empty Result is what let two dead chat turns be recorded
// as successes with nothing in the log.
func TestEmptyFinalAnswerIsNotTreatedAsSuccess(t *testing.T) {
	c, ws := newAPITestCoder(t)
	calls := 0
	c.testComplete = func(req llm.Request) (*llm.Response, error) {
		calls++
		return &llm.Response{Content: ""}, nil // no tool calls, no text
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.TrimSpace(res.Text) == "" {
		t.Errorf("returned an empty Result as a finished answer")
	}
	if res.StopReason != "empty" {
		t.Errorf("StopReason = %q, want %q", res.StopReason, "empty")
	}
	if calls < 2 {
		t.Errorf("model was not nudged to answer in words (calls=%d)", calls)
	}
}
```

Use whatever provider-stub helper `api_engine_test.go` already has; check the file for the existing seam (it stubs `Complete` for the milestone tests) and follow it rather than inventing a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestEmptyFinalAnswer -v`
Expected: FAIL — `StopReason = "", want "empty"`.

- [ ] **Step 3: Write the implementation**

In the final-answer branch, before constructing the `Result`:

```go
// An empty completion is not an answer. The model stopped calling tools and
// said nothing, which previously returned Text:"" with StopReason:"" — a
// successful turn carrying nothing, which chat then rendered as a placeholder
// and the log did not mention at all.
//
// One nudge, bounded the same way verifyFinishNudge is: a model that has run
// out of useful things to say will not be argued into saying more, and a
// second round costs a turn from the budget that is usually why it went quiet.
if strings.TrimSpace(resp.Content) == "" && emptyNudges < maxEmptyNudges {
	emptyNudges++
	req.Messages = append(req.Messages,
		llm.Message{Role: "assistant", Content: ""},
		llm.Message{Role: "user", Content: emptyAnswerNudge},
	)
	continue
}
```

with, near the other budget constants:

```go
// One retry only — see the comment at the use site.
const maxEmptyNudges = 1

const emptyAnswerNudge = "You returned no text. Answer the question in words now, " +
	"using what you already have. If you could not complete it, say what you got " +
	"and what stopped you — do not reply with nothing."
```

and, when the nudge is spent:

```go
res.StopReason = "empty"
if strings.TrimSpace(res.Text) == "" {
	res.Text = emptyAnswerFallback
}
```

```go
// Composed from run facts rather than left to the model, for the same reason
// exhaustionSummary is: the one thing a model in this state has proven is that
// it will not produce text.
const emptyAnswerFallback = "I wasn't able to produce an answer for that. " +
	"This usually means the request needed more of a large file than fits in one " +
	"conversation — try asking for a specific part of it, or a narrower question."
```

- [ ] **Step 4: Run tests**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -count=1`
Expected: PASS

- [ ] **Step 5: Log one line per finished chat turn**

In `web/chat_turn_tracker.go`, in the goroutine's success branch, beside the existing failure `slog.Warn`:

```go
// Chat turns logged NOTHING on the happy path, so a turn that produced no
// text left no trace anywhere — which is why the server log was silent about
// two failed turns. agentrunner gained this line for the same reason.
slog.Info("chat: turn finished",
	"chat", chatID, "turn", st.id,
	"milestones", len(st.lines),
	"reply_bytes", len(cleaned),
	"empty", strings.TrimSpace(reply) == "")
```

- [ ] **Step 6: Commit**

```bash
git add internal/coder/api_engine.go internal/coder/api_engine_test.go web/chat_turn_tracker.go
git commit -m "fix(coder): stop treating an empty completion as a finished answer"
```

---

### Task 2: Per-file search scoping

**Files:**
- Modify: `internal/vault/search.go` (`Search`, `searchRipgrep`, `searchGo`)
- Modify: `internal/vault/index.go` (add `SearchWithin`)
- Modify: `internal/vault/kbsearch.go` (`SearchKB` gains a scope)
- Test: `internal/vault/searchscope_test.go` (new)

**Interfaces:**
- Produces:
  - `Searcher.Search(ctx, workspaceID, query string) ([]SearchHit, error)` is unchanged; a new method `SearchIn(ctx, workspaceID, query, relPath string) ([]SearchHit, error)`.
  - `(*Indexer).SearchWithin(workspaceID, query string, limit int, relPath string) []Scored`
  - `SearchKBIn(ctx, v, searcher, workspaceID, query, relPath string, maxBytes int) string`
  - `const MaxHitsInFile = 50`

Three traps, all load-bearing:

1. **Both exact searchers cap at 5 matches per file** — `--max-count 5` in `searchRipgrep`, `count >= 5` in `searchGo`. Right for a whole-vault search (one file must not dominate), wrong when the caller asked about one file.
2. **The ranked pass is exclude-only.** `Indexer.SearchExcluding` filters prefixes *out*. Scope only the exact pass and the ranked half keeps answering from other files.
3. **The rg/Go split is a per-CALL fallback, not per-host.** `Search` tries `rg` and falls through to `searchGo` on *any* rg error, so the same machine can take either path on consecutive calls. Divergence appears as nondeterminism. No test currently compares them.

- [ ] **Step 1: Write the failing tests**

Create `internal/vault/searchscope_test.go`:

```go
package vault

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// scopeFixture builds a vault with the query term in two files, and 20 hits in
// the target file so the per-file cap is observable.
func scopeFixture(t *testing.T) (*Vault, string) {
	t.Helper()
	v, ws := newTestVault(t) // existing helper in this package
	var b strings.Builder
	b.WriteString("# Target\n\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line %d mentions dentist here\n", i)
	}
	if err := v.WriteNote(ws, "notes/target.md", b.String()); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := v.WriteNote(ws, "notes/other.md", "# Other\n\ndentist appears here too\n"); err != nil {
		t.Fatalf("write other: %v", err)
	}
	return v, ws
}

// A scoped search must not answer from other files.
func TestSearchInIsScopedToOneFile(t *testing.T) {
	v, ws := scopeFixture(t)
	s := v.NewSearcher()
	hits, err := s.SearchIn(context.Background(), ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("SearchIn: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, h := range hits {
		if h.Path != "notes/target.md" {
			t.Errorf("hit leaked from %s", h.Path)
		}
	}
}

// The whole-vault cap of 5 per file is right for a vault-wide search and wrong
// when the caller asked about ONE file — five hits from the only file you asked
// about is not a search.
func TestSearchInRaisesThePerFileCap(t *testing.T) {
	v, ws := scopeFixture(t)
	s := v.NewSearcher()
	hits, _ := s.SearchIn(context.Background(), ws, "dentist", "notes/target.md")
	if len(hits) <= 5 {
		t.Errorf("got %d hits, want more than the whole-vault per-file cap of 5", len(hits))
	}

	wide, _ := s.Search(context.Background(), ws, "dentist")
	inTarget := 0
	for _, h := range wide {
		if h.Path == "notes/target.md" {
			inTarget++
		}
	}
	if inTarget > 5 {
		t.Errorf("whole-vault search returned %d hits from one file, cap is 5", inTarget)
	}
}

// Search tries ripgrep and silently falls through to searchGo on ANY rg error,
// so these are not two hosts — they are the same host on consecutive calls.
// Divergence shows up as nondeterminism, which is why they must agree.
func TestScopedSearchAgreesAcrossBothImplementations(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	v, ws := scopeFixture(t)
	s := &ripgrepSearcher{v: v}

	rgHits, err := s.searchRipgrepIn(context.Background(), v.Root(ws), ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("ripgrep: %v", err)
	}
	goHits, err := s.searchGoIn(ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("go: %v", err)
	}
	if len(rgHits) != len(goHits) {
		t.Fatalf("hit counts differ: rg=%d go=%d", len(rgHits), len(goHits))
	}
	for i := range rgHits {
		if rgHits[i].Line != goHits[i].Line || rgHits[i].Path != goHits[i].Path {
			t.Errorf("hit %d differs: rg=%+v go=%+v", i, rgHits[i], goHits[i])
		}
	}
}

// The ranked pass filters prefixes OUT and has no include; without SearchWithin
// a scoped SearchKB would return ranked passages from other files.
func TestSearchWithinScopesTheRankedPass(t *testing.T) {
	v, ws := scopeFixture(t)
	for _, s := range v.Indexer().SearchWithin(ws, "dentist", 10, "notes/target.md") {
		if s.Chunk.Path != "notes/target.md" {
			t.Errorf("ranked hit leaked from %s", s.Chunk.Path)
		}
	}
}
```

If `newTestVault` does not exist under that name, use whatever the package's existing vault fixture helper is — check `vault_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -run "TestSearchIn|TestScopedSearch|TestSearchWithin" -v`
Expected: FAIL — `SearchIn` / `SearchWithin` / `searchRipgrepIn` undefined.

- [ ] **Step 3: Implement the scoped searchers**

In `internal/vault/search.go`:

```go
// MaxHitsInFile is the per-file cap for a SCOPED search. The whole-vault
// searchers cap at 5 per file so one file cannot dominate a vault-wide result;
// that reasoning inverts when the caller named a single file, where five hits
// is not a search.
const MaxHitsInFile = 50

// SearchIn is Search restricted to one vault-relative file.
func (s *ripgrepSearcher) SearchIn(ctx context.Context, workspaceID, query, rel string) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" || rel == "" {
		return nil, nil
	}
	root := s.v.Root(workspaceID)
	if _, err := exec.LookPath("rg"); err == nil {
		if hits, err := s.searchRipgrepIn(ctx, root, workspaceID, query, rel); err == nil {
			return hits, nil
		}
		// Same fall-through as Search: rg failing for any reason must not fail
		// the search. This is exactly why the two must agree.
	}
	return s.searchGoIn(workspaceID, query, rel)
}
```

`searchRipgrepIn` is `searchRipgrep` with the target file in place of `root` and `--max-count` set to `MaxHitsInFile`; `searchGoIn` reads the one note instead of walking, with the same cap. Both keep using `snippetFor`, so table headers still travel with scoped hits.

Add `SearchIn` to the `Searcher` interface.

In `internal/vault/index.go`:

```go
// SearchWithin is the include-path counterpart of SearchExcluding: ranked
// passages from ONE file. Without it a scoped SearchKB would scope only its
// exact pass and quietly answer from the rest of the vault.
func (i *Indexer) SearchWithin(workspaceID, query string, limit int, rel string) []Scored {
	all := i.search(workspaceID, query, limit*4, nil)
	out := make([]Scored, 0, limit)
	for _, s := range all {
		if s.Chunk.Path == rel {
			out = append(out, s)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}
```

`limit*4` then filter is deliberate: the scorer ranks globally, so asking for `limit` and filtering could return nothing for a file that has matches but ranks below other files. Over-fetching then filtering is the cheap correct version; the index is in memory.

In `internal/vault/kbsearch.go`, add `SearchKBIn` taking `relPath`, using `SearchIn` and `SearchWithin`. Keep `SearchKB` as the unscoped caller so no existing call site changes.

- [ ] **Step 4: Run tests**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/vault/search.go internal/vault/index.go internal/vault/kbsearch.go internal/vault/searchscope_test.go
git commit -m "feat(vault): scope knowledge-base search to a single file"
```

---

### Task 3: File map

**Files:**
- Create: `internal/vault/filemap.go`
- Test: `internal/vault/filemap_test.go`

**Interfaces:**
- Produces:
  - `type FileShape struct { Path string; Kind string; Bytes int; Tokens int; Rows int; Columns []ColumnStat; Sections []SectionStat; Warnings []string }`
  - `type ColumnStat struct { Name string; Bytes int; Share float64 }`
  - `type SectionStat struct { Heading string; Bytes int; Line int }`
  - `func MapFile(v *Vault, workspaceID, rel string) (FileShape, error)`
  - `func (f FileShape) Render(maxBytes int) string`

This is the piece that fixes the reported bug: the model sees `apiTransaction` is 88% of the file and never pages it.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/filemap_test.go`:

```go
package vault

import (
	"fmt"
	"strings"
	"testing"
)

// Shaped after the real note: a table whose bulk is ONE junk column. A fixture
// with evenly-sized columns would pass a broken warning.
func lopsidedTable(rows int) string {
	var b strings.Builder
	b.WriteString("# Transactions\n\n*Converted from card-transactions.csv.*\n\n")
	b.WriteString("| date | merchantName | USDAmount | apiTransaction |\n")
	b.WriteString("|---|---|---|---|\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "| 2026-08-%02d | Merchant %d | %d.00 | %s |\n",
			i%28+1, i, i, strings.Repeat("x", 1200))
	}
	return b.String()
}

func TestMapFileFlagsADisproportionateColumn(t *testing.T) {
	v, ws := newTestVault(t)
	if err := v.WriteNote(ws, "notes/tx.md", lopsidedTable(98)); err != nil {
		t.Fatalf("write: %v", err)
	}

	shape, err := MapFile(v, ws, "notes/tx.md")
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	if shape.Kind != "table" {
		t.Errorf("Kind = %q, want table", shape.Kind)
	}
	if shape.Rows != 98 {
		t.Errorf("Rows = %d, want 98", shape.Rows)
	}
	if len(shape.Columns) != 4 {
		t.Fatalf("Columns = %d, want 4", len(shape.Columns))
	}
	joined := strings.Join(shape.Warnings, "\n")
	if !strings.Contains(joined, "apiTransaction") {
		t.Errorf("the dominant column was not flagged: %v", shape.Warnings)
	}
	if strings.Contains(joined, "USDAmount") {
		t.Errorf("a small column was wrongly flagged: %v", shape.Warnings)
	}
}

// Prose gets an outline instead of columns — same call, shape by content.
func TestMapFileOutlinesProse(t *testing.T) {
	v, ws := newTestVault(t)
	doc := "# Trip\n\nintro\n\n## Flights\n\n" + strings.Repeat("detail. ", 400) +
		"\n\n## Hotels\n\nshort\n"
	if err := v.WriteNote(ws, "notes/trip.md", doc); err != nil {
		t.Fatalf("write: %v", err)
	}

	shape, err := MapFile(v, ws, "notes/trip.md")
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	if shape.Kind != "prose" {
		t.Errorf("Kind = %q, want prose", shape.Kind)
	}
	var headings []string
	for _, s := range shape.Sections {
		headings = append(headings, s.Heading)
	}
	if len(headings) < 3 {
		t.Errorf("outline too thin: %v", headings)
	}
}

// The rendered map is itself a tool result and must respect the cap — a
// 500-heading document must not produce 500 lines.
func TestRenderedMapRespectsTheByteCap(t *testing.T) {
	v, ws := newTestVault(t)
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "## Section %d\n\nsome text here\n\n", i)
	}
	if err := v.WriteNote(ws, "notes/many.md", b.String()); err != nil {
		t.Fatalf("write: %v", err)
	}
	shape, _ := MapFile(v, ws, "notes/many.md")
	out := shape.Render(8192)
	if len(out) > 8192 {
		t.Errorf("rendered map is %d bytes, over the 8192 cap", len(out))
	}
	if !strings.Contains(out, "more") {
		t.Errorf("truncated silently — should say how many were omitted:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -run TestMapFile -v`
Expected: FAIL — `MapFile` undefined.

- [ ] **Step 3: Implement**

`internal/vault/filemap.go`. Classification: a file is `"table"` when `tableHeader` (from `chunk.go`) matches within its first section and the delimiter-backed table accounts for most of its lines; `"prose"` when it has headings; `"code"`/`"other"` otherwise. Column byte-weights come from summing cell lengths per column. Sections reuse `ChunkMarkdown`'s heading trail rather than a second heading parser.

Warning rule:

```go
// A part of a file worth warning about is one that will dominate the model's
// context if selected. 40% is the threshold: below it, reading the whole file
// is a reasonable strategy; above it, one column or section IS the file.
const dominantShare = 0.40
```

`Render` sorts sections/columns by size, emits the largest first, and closes with `…and N more` when it hits the cap — never a silent truncation.

- [ ] **Step 4: Run tests**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/vault/filemap.go internal/vault/filemap_test.go
git commit -m "feat(vault): describe a file's shape before it is read"
```

---

### Task 4: Table parsing and querying

**Files:**
- Create: `internal/vault/table.go`
- Test: `internal/vault/table_test.go`

**Interfaces:**
- Consumes: `tableHeader` (`chunk.go`).
- Produces:
  - `type Table struct { Columns []string; Rows [][]string }`
  - `func ParseTable(content string) (Table, error)`
  - `type TableQuery struct { Select []string; Where map[string]string; GroupBy string; Metric string; Op string; Order string; Limit int }`
  - `type QueryResult struct { Columns []string; Rows [][]string; Skipped int; Notes []string }`
  - `func (t Table) Query(q TableQuery) (QueryResult, error)`

No SQLite and no application database: this is a pure function over one file's text. `Op` is one of `sum`/`avg`/`count`/`min`/`max`; `GroupBy` accepts a column name or `date:month`/`date:day`/`date:year`.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/table_test.go`:

```go
package vault

import (
	"strings"
	"testing"
)

// Values shaped like the real file: markdown-escaped negatives, mixed
// currencies, APPROVED/PENDING.
const txFixture = `| date | merchantName | USDAmount | status |
|---|---|---|---|
| 2026-06-02T10:00:00Z | Kaufland | 12.50 | APPROVED |
| 2026-06-11T10:00:00Z | Neptun | 100.00 | APPROVED |
| 2026-07-03T10:00:00Z | Kaufland | 7.25 | APPROVED |
| 2026-07-04T10:00:00Z | OpenRouter | 10.85 | PENDING |
| 2026-07-20T10:00:00Z | Neptun | \-5.00 | APPROVED |
`

func TestQuerySumsByMonth(t *testing.T) {
	tab, err := ParseTable(txFixture)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	res, err := tab.Query(TableQuery{
		Where: map[string]string{"status": "APPROVED"}, GroupBy: "date:month",
		Metric: "USDAmount", Op: "sum", Order: "asc",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Computed by hand, not from the tool: 12.50+100.00 and 7.25+(-5.00).
	want := map[string]string{"2026-06": "112.50", "2026-07": "2.25"}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d groups, want 2: %v", len(res.Rows), res.Rows)
	}
	for _, r := range res.Rows {
		if want[r[0]] != r[1] {
			t.Errorf("%s = %s, want %s", r[0], r[1], want[r[0]])
		}
	}
}

func TestQueryTopN(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	res, _ := tab.Query(TableQuery{
		Select: []string{"merchantName", "USDAmount"},
		Metric: "USDAmount", Op: "max", GroupBy: "merchantName",
		Order: "desc", Limit: 2,
	})
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "Neptun" {
		t.Errorf("top merchant = %s, want Neptun", res.Rows[0][0])
	}
}

// A total computed from some of the rows without saying so is worse than an
// error — the reader cannot tell it is wrong.
func TestQueryReportsUncoercibleValues(t *testing.T) {
	bad := strings.Replace(txFixture, "7.25", "n/a", 1)
	tab, _ := ParseTable(bad)
	res, _ := tab.Query(TableQuery{Metric: "USDAmount", Op: "sum"})
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
	if len(res.Notes) == 0 {
		t.Errorf("skipped rows were not reported to the caller")
	}
}

// A pipe inside a cell must not silently shift every later column.
func TestParseTableHandlesEscapedPipes(t *testing.T) {
	src := "| a | b |\n|---|---|\n| x \\| y | z |\n"
	tab, err := ParseTable(src)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if len(tab.Rows[0]) != 2 {
		t.Fatalf("row has %d cells, want 2: %v", len(tab.Rows[0]), tab.Rows[0])
	}
	if tab.Rows[0][1] != "z" {
		t.Errorf("columns shifted: %v", tab.Rows[0])
	}
}

func TestQueryRejectsUnknownOp(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	if _, err := tab.Query(TableQuery{Metric: "USDAmount", Op: "median"}); err == nil {
		t.Error("unknown op accepted")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -run "TestQuery|TestParseTable" -v`
Expected: FAIL — `ParseTable` undefined.

- [ ] **Step 3: Implement**

Number coercion strips markdown escaping (`\-` → `-`), thousands separators and currency symbols before `strconv.ParseFloat`. A cell that will not parse increments `Skipped` and appends to `Notes` — it is never silently dropped. `date:month` takes the first 7 characters of an ISO-8601 timestamp; a value that is not a date increments `Skipped` too.

- [ ] **Step 4: Run tests**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/vault/table.go internal/vault/table_test.go
git commit -m "feat(vault): parse and aggregate markdown tables"
```

---

### Task 5: Expose the tools to both coder kinds

**Files:**
- Modify: `internal/coder/hosttools.go` (registration ~line 193; dispatch ~line 557)
- Modify: `internal/vault/bridge.go` (new endpoints)
- Test: `internal/coder/bigfiletools_test.go` (new)

**Interfaces:**
- Consumes: `MapFile`, `FileShape.Render`, `ParseTable`, `Table.Query`, `SearchKBIn`.
- Produces: tools `kb_file_map`, `kb_table_query`; `read_file` gains `section`; `search_files` gains `path`.

- [ ] **Step 1: Write the failing test**

```go
// The two tools must be offered to a chat coder — chat is where the reported
// failure happened, and chat has no exec tools to fall back on.
func TestBigFileToolsAreOfferedInChat(t *testing.T) {
	h := &hostToolSet{includeExecTools: false, vlt: testVault(t)}
	var names []string
	for _, tool := range h.tools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"kb_file_map", "kb_table_query"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s not offered in chat: %v", want, names)
		}
	}
}

// A tool result is never empty and a no-result outcome is never an error:
// the engine's oscillation guard counts `error:` as a failing call.
func TestTableQueryNoMatchesIsNotAnError(t *testing.T) {
	h := newToolSetWithNote(t, "notes/tx.md", txFixture)
	out := h.execute(context.Background(), toolCall("kb_table_query",
		`{"path":"notes/tx.md","where":{"status":"NOPE"},"metric":"USDAmount","op":"sum"}`))
	if strings.HasPrefix(out, "error:") {
		t.Errorf("no-match reported as an error: %q", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("empty tool result")
	}
}

// Default select must omit a flagged column, or a naive call reproduces the
// original bug — 131 KB of JSON dragged into context.
func TestTableQueryDefaultSelectOmitsTheFatColumn(t *testing.T) {
	h := newToolSetWithNote(t, "notes/tx.md", lopsidedTable(98))
	out := h.execute(context.Background(), toolCall("kb_table_query", `{"path":"notes/tx.md","limit":3}`))
	if strings.Contains(out, strings.Repeat("x", 100)) {
		t.Errorf("the dominant column was selected by default")
	}
	if len(out) > maxToolResult {
		t.Errorf("result is %d bytes, over the cap", len(out))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run "TestBigFileTools|TestTableQuery" -v`
Expected: FAIL — tools not registered.

- [ ] **Step 3: Register and dispatch**

Add to `tools()` — **not** gated by `includeExecTools`, mirroring `search_files` and `glob`, because chat is exactly where this is needed:

```go
{Name: "kb_file_map", Description: "Describe a knowledge-base file BEFORE reading it: " +
	"its kind, size, token cost, and structure (columns and row count for a table, " +
	"a heading outline for a document), plus a warning when one column or section " +
	"holds most of the bytes. Call this first for any file you have not read — it " +
	"tells you what to fetch instead of reading the whole thing.",
	Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},

{Name: "kb_table_query", Description: "Filter, group, aggregate and rank the rows of a " +
	"markdown table in the knowledge base. Use this for totals, averages, counts and " +
	"top-N — do NOT add up numbers yourself. Call kb_file_map first to learn the column names.",
	Parameters: rawSchema(`{"type":"object","properties":{
		"path":{"type":"string"},
		"select":{"type":"array","items":{"type":"string"}},
		"where":{"type":"object","additionalProperties":{"type":"string"}},
		"group_by":{"type":"string","description":"a column name, or date:month / date:day / date:year"},
		"metric":{"type":"string","description":"column to aggregate"},
		"op":{"type":"string","enum":["sum","avg","count","min","max"]},
		"order":{"type":"string","enum":["asc","desc"]},
		"limit":{"type":"integer"}},"required":["path"]}`)},
```

Extend the `execute` args struct with `Section string \`json:"section"\``, `Select []string \`json:"select"\``, `Where map[string]string \`json:"where"\``, `GroupBy string \`json:"group_by"\``, `Metric string \`json:"metric"\``, `Op string \`json:"op"\``, `Order string \`json:"order"\``, and add the two cases plus `section`/`path` handling in `read_file` and `search_files`.

The `op` enum is what makes a small model's mistake a validation error with a precise message rather than a silent wrong answer.

In `internal/vault/bridge.go`, add `/map` and `/table` alongside `/convert` and `/search`, and the matching `rookery kb map|table` subcommands, so a CLI coder reaches the same calls.

- [ ] **Step 4: Run tests**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ ./internal/vault/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/coder/hosttools.go internal/coder/bigfiletools_test.go internal/vault/bridge.go
git commit -m "feat(coder): offer file-map and table-query tools to both coder kinds"
```

---

### Task 6: Prompt guidance, end-to-end check, docs

**Files:**
- Modify: `internal/prompts/` (the chat + coder capability blocks)
- Modify: `CLAUDE.md`
- Test: manual against the real note; `GOTOOLCHAIN=auto make ci`

- [ ] **Step 1: Tell the model the tools exist**

The tool descriptions carry most of this, but `coderCapabilitiesBlock`'s tool-calling branch lists the file tools by name and must include the two new ones, or a model reading its capabilities will not know to reach for them. Add one line: for a file over ~8 KiB, call `kb_file_map` before `read_file`; never add up numbers by hand when `kb_table_query` can.

- [ ] **Step 2: Verify against the real file**

```bash
GOTOOLCHAIN=auto make deploy
```

Then in a chat: *"How much did I spend per month, and what are my top 5 transactions?"* against `notes/card-transactions.md`. Expected: a `kb_file_map` call, then one or two `kb_table_query` calls, then an answer — not 19 `read_file` calls, and not an empty reply.

- [ ] **Step 3: Correct the CLAUDE.md claim that contradicts this**

The "Table retrieval" section states the note has "~1000 rows" and that *"how much have I spent in total" still cannot be answered*. Both are now wrong: it is **98 rows**, and `kb_table_query` answers it. Rewrite that paragraph to describe what the tools do and keep the real remaining limit — chat still has no arbitrary compute, so anything the query parameters cannot express falls back to reading the projected table.

Also document: `kb_file_map`/`kb_table_query` in the tool list, per-file search scoping with the three traps from Task 2, and the empty-completion guard.

- [ ] **Step 4: Run the docs-sync skill**

New host tools are a user-visible capability: `concepts/knowledge-base.md` needs the "big files" story, replacing the "findable, not calculable" caveat added in the previous change.

- [ ] **Step 5: Full gate**

```bash
GOTOOLCHAIN=auto make ci
```

- [ ] **Step 6: Commit and open the PRs**

```bash
git add -A
git commit -m "docs: record big-file navigation and table computation"
git push -u origin HEAD
gh pr create --draft --title "feat(kb): navigate and compute over big knowledge-base files"
```

---

## Self-Review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| A — `kb_file_map` | 3, 5 |
| A — `read_file` `section:` | 5 |
| A — `search_files` `path:` (3 paths, cap, parity test) | 2 |
| A — empty completion is not an answer | 1 |
| A — chat turn logging | 1 |
| A — both coder kinds | 5 |
| B — `kb_table_query` parameters, plain Go, no app DB | 4, 5 |
| B — coercion failures reported | 4 |
| B — projection fallback / default select | 5 |
| B — no writes, no `run_script` in chat | 5 (tools are read-only; nothing gates on `includeExecTools`) |
| Docs + CLAUDE.md correction | 6 |

No spec requirement is unimplemented.

**Placeholder scan:** every code step carries real code. Two steps name an existing helper to look up rather than inventing one (`newTestVault`, the `Complete` stub in `api_engine_test.go`) — deliberate, because guessing a fixture name that does not exist is worse than telling the implementer where to look.

**Type consistency:** `FileShape`/`ColumnStat`/`SectionStat` defined in Task 3 and consumed in Task 5. `Table`/`TableQuery`/`QueryResult` defined in Task 4, consumed in Task 5. `SearchIn`/`SearchWithin`/`SearchKBIn`/`MaxHitsInFile` defined in Task 2, consumed in Task 5. `StopReason: "empty"` defined in Task 1 and referenced nowhere later, which is correct — it is an engine-internal signal.
