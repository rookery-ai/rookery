# Table computation: arithmetic the model does not have to do

Date: 2026-08-21
Status: design, part **B** of two. Depends on part A
(`2026-08-21-big-file-navigation-design.md`) for the column schema.

## What this is for

Part A lets a model *find* things in a big file. It does not let it answer
"how much did I spend per month" or "what are my top five transactions", because
those are arithmetic over every row, and language models are unreliable at
arithmetic at any size. The owner asked both questions and got an empty reply.

The data makes the case that this is worth building rather than worked around:
the file in question has **98 rows**. Computing a monthly total is trivial —
just not for a model doing it in its head from text it paged in 8 KiB at a time.

## The interface is parameters, not SQL

The first draft of this design had the model write SQL against an in-memory
SQLite database. That was wrong for this product, and the reason is worth
recording because SQL is the obvious reach.

**The workspace coder is `deepseek-v4-flash`.** For SQL to work the model must
produce valid syntax *and* exact column names — including `MCC Label`, which
contains a space and needs quoting, and `substr(date,1,7)` for month grouping.
Those are the failure modes of a small model, and every failed query costs a turn
from the same budget that just ran out. A design that only works on a strong
model is not a design for this product.

So the model fills a schema instead:

```
kb_table_query(
  path:     "notes/card-transactions.md",
  where:    {status: "APPROVED"},
  group_by: "date:month",
  metric:   "USDAmount",
  op:       "sum",
  order:    "desc",
  limit:    5
)
```

Filling a JSON schema is what a function-calling model is trained to do, and
where a small model is at its strongest. An invalid value is caught by schema
validation with a precise message (`op must be one of sum/avg/count/min/max`)
rather than becoming a syntax error the model must debug blind.

**And once the interface is fixed parameters, SQLite earns nothing.** The host
would be generating SQL from those parameters anyway — sanitising column names
into identifiers, inferring types, building `CREATE TABLE` and a `SELECT`. That
is more machinery for the same fixed operation set, and it puts string-building
next to a query engine, which becomes an injection review later. Plain Go —
parse, filter, group, aggregate, sort, limit — is roughly 200 lines, has no
database in the loop, needs no fixtures to test, and is a single pass in
milliseconds at any row count this product will see.

**It must not be the application database.** Recording this because it is the
question a reader will ask: `rookery.db` holds `encrypted_master_password` and
every OAuth and bot token. No model-driven query path goes near it, read-only or
otherwise. This tool reads one markdown file from the vault and computes in
memory.

## Design

`kb_table_query(path, select, where, group_by, metric, op, order, limit)` —
one new host tool.

- `select` — columns to return. Omitted means every column *except* any flagged
  as disproportionate by part A's map, so the default cannot blow the context.
- `where` — equality and comparison on named columns.
- `group_by` — a column, or `date:month` / `date:day` / `date:year`, so "per
  month" does not require the model to reason about string slicing.
- `metric` + `op` — the column to aggregate and how (`sum`/`avg`/`count`/
  `min`/`max`).
- `order`, `limit` — ranking, which is what "top five" needs.

Numbers are coerced on read: the source column holds values like `\-10.8500`
(escaped for markdown) and mixed currencies. Coercion failures are **reported,
not silently skipped** — a total computed from 94 of 98 rows without saying so is
worse than an error.

**Projection is the escape hatch.** For any question the parameters cannot
express, return the table with the fat columns dropped — 8 KB rather than
155 KB — and let the model read it directly. That covers the long tail without
inventing an operation per question, and it is why the operation set can stay
small rather than growing forever.

## What this deliberately does not do

- **No arbitrary expressions, no computed columns, no joins.** Each is a step
  toward reimplementing SQL through a JSON keyhole. The projection fallback
  exists so the ceiling is "the model reads the small table", not "we add
  another operation".
- **No writing.** Read-only.
- **It does not enable `run_script` in chat.** Chat stays file-only. This tool is
  a pure function of one vault file — no shell, no network, no code execution —
  so it adds capability without moving that boundary.

## Testing

- **The real file is the fixture.** 98 rows, an 88%-of-bytes junk column, escaped
  negative numbers, mixed currencies, `APPROVED`/`PENDING` statuses.
- Monthly totals and top-N verified against values computed independently in the
  test, not against the tool's own output.
- Coercion failures surface in the result rather than being dropped.
- Default `select` omits the flagged column — the property that keeps a naive
  call from reproducing the original bug.
- A table with two header rows, ragged rows, or a pipe inside a cell parses or
  fails cleanly, never silently mis-columns.

## Sequencing

Part A ships first and alone. It fixes the reported failure — the model sees the
shape and answers from 8 KB. Whether B is still worth building is a judgement to
make *after* using A, because A may make the arithmetic questions rare enough
not to matter.
