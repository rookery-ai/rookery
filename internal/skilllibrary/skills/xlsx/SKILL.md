---
name: xlsx
description: Use this skill whenever the user wants to read, create, edit, or analyze Excel .xlsx files — extracting sheets, formulas, or data, or generating a new spreadsheet. Triggers include "read this excel", "extract xlsx data", "create a spreadsheet", "edit xlsx formulas".
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  install:
    - kind: pip
      package: openpyxl
    - kind: pip
      package: pandas
---

# XLSX

Read, create, edit, and analyze Excel `.xlsx` workbooks via `openpyxl` (formulas,
styling, multi-sheet) or `pandas` (tabular analysis). Use LibreOffice (`soffice`)
only when you must recalculate a sheet's stored formulas (openpyxl does not
evaluate them).

## Requirements

- `openpyxl` (Python) — `python3 -m pip install --user openpyxl`.
- `pandas` (Python) — `python3 -m pip install --user pandas openpyxl`.
- Optional `soffice` (LibreOffice) — for recalculation/headless conversion. Heavy;
  not required. Surface it as a dependency rather than failing silently.

The runtime environment block gives you the absolute path of any installed CLI
tool. Invoke Python via `python3`.

## Read a sheet

```python
import openpyxl, json, sys
wb = openpyxl.load_workbook(sys.argv[1], data_only=True)
out = []
for ws in wb.worksheets:
    rows = [[("" if c is None else str(c)) for c in row] for row in ws.iter_rows(values_only=True)]
    out.append({"sheet": ws.title, "rows": rows})
print(json.dumps(out))
```

`data_only=True` returns cached computed values (the last value LibreOffice/Excel
stored). If the file was never opened, formula cells return `None` — recalc via
`soffice` (see below).

## Create a workbook

```python
from openpyxl import Workbook
wb = Workbook()
ws = wb.active
ws.title = "Summary"
ws.append(["Month", "Revenue", "Cost"])
ws.append(["Jan", 1000, 600])
ws["B5"] = "=SUM(B2:B4)"
wb.save("output.xlsx")
```

## Recalculate stored formulas (LibreOffice)

```bash
soffice --headless --calc --convert-to xlsx:"Calc MS Excel 2007 XML" --outdir "$TMPDIR" input.xlsx
```

## Best practices

- **Formulas over hardcodes** — store `=SUM(...)` / `=VLOOKUP(...)` rather than
  writing computed numbers, so the sheet stays live and auditable.
- Avoid `.xls` (legacy binary); if given one, convert with `soffice` first.
- Write outputs into the vault or `$TMPDIR`, never `/tmp`.
- For large tabular analysis prefer `pandas.read_excel`; for cell/styling control
  prefer `openpyxl`.