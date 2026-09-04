---
name: csv
description: Use this skill whenever the user wants to read, filter, aggregate, or summarise CSV/TSV data — totals, counts, averages, group-by summaries, or turning a spreadsheet export into something readable. Triggers include "analyze this csv", "summarize csv", "filter rows", "how much did I spend", "csv to json", "merge csv files".
version: 2.0.0
license: MIT-0
category: File Processing
---

# CSV

**You do not write code to read a CSV here.** The platform converts it and
queries it for you, and those tools handle the parts that go wrong silently —
encodings, delimiters, huge columns, type coercion.

## Get it into the knowledge base first

```bash
rookery kb convert data.csv --dest notes
```

That produces a markdown table with the header intact. Everything below works on
that note.

If the file is already in the knowledge base, skip this step.

## Look before you read

A converted spreadsheet can be enormous, and most of the bytes are usually in
one column nobody asked about. Map it first:

```
kb_file_map(path="notes/data.md")
```

You get the columns, the row count, the reading cost, and a warning when one
column dominates the file. **Read that warning.** A 155 KB export whose
`apiTransaction` column is 88% of the bytes has only 8 KB of answerable data in
it — reading the whole thing wastes the turn budget and can end the run with no
answer at all.

## Ask the question directly

`kb_table_query` filters, groups and aggregates host-side. You fill in
parameters; you never write SQL or a DataFrame expression.

```
kb_table_query(path="notes/data.md", op="sum", column="amount")
kb_table_query(path="notes/data.md", op="sum", column="amount", group_by="region")
kb_table_query(path="notes/data.md", op="count", where_column="status", where_equals="paid")
```

This is the whole point of the tool: totals and group-bys computed by the host
are right or they error. The same arithmetic written as code is right or it is
quietly wrong, and a wrong number looks exactly like a correct one.

If the operation set cannot express your question, project the useful columns
and read them — the default projection already drops a dominant column.

## Converting to another format

```bash
rookery kb convert data.csv --dest notes     # → markdown
```

For CSV → JSON or CSV → Excel, say what you need in your reply rather than
generating a file: the user asked a question, and a converted file they have to
open is rarely the answer.

## When you genuinely need code

Only when the tools above cannot express it — a reshape, a join across two
files, a custom parse. Then use the standard library `csv` module, which needs
no install:

```python
import csv, json, sys
with open(sys.argv[1], newline="", encoding="utf-8-sig") as f:
    rows = [r for r in csv.DictReader(f) if r.get("status") == "paid"]
print(json.dumps(rows[:20]))
```

`utf-8-sig` is deliberate: a spreadsheet export usually carries a byte-order
mark, and reading it as plain UTF-8 leaves the mark glued to your first column
name so every lookup on it fails.

**Do not install pandas for this.** It was previously recommended here, and it is
the wrong tool on this platform: it is a large dependency to fetch on every run,
and a groupby written by a small model produces a number rather than an error
when it is wrong.
