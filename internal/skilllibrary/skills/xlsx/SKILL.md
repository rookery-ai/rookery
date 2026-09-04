---
name: xlsx
description: Use this skill whenever the user wants to read, create, edit, or analyze Excel .xlsx files — extracting sheets or data, summarising figures, or generating a new spreadsheet. Triggers include "read this excel", "extract xlsx data", "create a spreadsheet", "totals from this workbook", "edit xlsx formulas".
version: 2.0.0
license: MIT-0
category: File Processing
metadata:
  install:
    - kind: pip
      package: openpyxl
---

# XLSX

Three different jobs, three different answers. Only the last one needs a library.

## Reading and analysing — platform tools

```bash
rookery kb convert book.xlsx --dest notes
```

Each sheet becomes a markdown table with its header. Then:

```
kb_file_map(path="notes/book.md")
kb_table_query(path="notes/book.md", op="sum", column="amount", group_by="region")
```

`kb_file_map` first, always, on a workbook of any size: a converted sheet is
often dominated by one wide column, and the map tells you that before you spend
the turn budget reading it.

**Totals and group-bys go through `kb_table_query`, not through code.** The host
computes them, so they are right or they error. The same arithmetic written by
hand is right or it is quietly wrong, and a wrong total looks exactly like a
correct one.

**Do not install pandas.** It was previously recommended here for this job and
the platform now does it properly.

## Producing a simple sheet

If the user wants data they can open in Excel and the content is a plain table,
write a CSV. Excel opens it, it is diffable, and it needs nothing installed:

```python
import csv
with open("out.csv", "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["region", "amount"])
    w.writerows(rows)
```

Say that you produced a CSV. Do not silently give someone a `.csv` when they
asked for a `.xlsx` — ask, or produce the real thing below.

## Authoring a real workbook — openpyxl

This is the case that genuinely needs a library, and the only reason one is
declared here: formulas, multiple sheets, number formats, styling.

```python
import openpyxl
wb = openpyxl.Workbook()
ws = wb.active
ws.title = "Summary"
ws.append(["region", "amount"])
for r in rows:
    ws.append(r)
ws["C2"] = "=SUM(B2:B100)"
wb.save("summary.xlsx")
```

Two things that will surprise you:

- **openpyxl does not evaluate formulas.** `load_workbook(path, data_only=True)`
  returns the value Excel last *cached*, which is empty for a file no
  spreadsheet application has ever opened. If you need a computed value, compute
  it yourself and write it as a literal.
- **A `.xlsx` you wrote has no cached values at all**, so an agent reading back
  its own output with `data_only=True` sees blanks. Read your own data from the
  source you wrote it from, not from the file you just produced.
