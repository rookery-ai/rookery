---
name: csv
description: Use this skill whenever the user wants to read, transform, filter, aggregate, or convert CSV/TSV data — pivots, summaries, joins, type coercion, or CSV↔JSON/Excel conversion. Triggers include "analyze this csv", "summarize csv", "filter rows", "csv to json", "merge csv files".
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  openclaw:
    install:
      - kind: pip
        package: pandas
---

# CSV

Read, transform, aggregate, and convert CSV/TSV data. Use `pandas` for
analysis/aggregation; the built-in `csv` module is enough for simple
row-by-row work (no extra install).

## Requirements

- `pandas` (Python) — `python3 -m pip install --user pandas`. Optional but
  recommended for joins/pivots/aggregations.
- Built-in `csv` module — always available, no install.

## Read + summarize

```python
import pandas as pd, json, sys
df = pd.read_csv(sys.argv[1])
print(json.dumps({
    "rows": int(len(df)),
    "columns": list(df.columns),
    "dtypes": {c: str(t) for c, t in df.dtypes.items()},
    "head": df.head(5).fillna("").to_dict(orient="records"),
}, default=str))
```

## Aggregate / pivot

```python
import pandas as pd
df = pd.read_csv("sales.csv")
summary = df.groupby("region")["revenue"].sum().sort_values(ascending=False)
summary.to_csv("revenue_by_region.csv")
```

## Simple row filter (no pandas)

```python
import csv, json, sys
with open(sys.argv[1], newline="") as f:
    rows = [r for r in csv.DictReader(f) if r.get("status") == "paid"]
print(json.dumps(rows))
```

## CSV ↔ JSON ↔ Excel

```python
import pandas as pd
df = pd.read_csv("data.csv")
df.to_json("data.json", orient="records")
df.to_excel("data.xlsx", index=False)
```

## Best practices

- Always pass the right delimiter/encoding: `pd.read_csv(path, sep="\t", encoding="utf-8")`.
  For messy encodings try `encoding="utf-8-sig"` or `latin-1`.
- Coerce types explicitly (`df["amount"] = pd.to_numeric(df["amount"], errors="coerce")`)
  rather than trusting inferred dtypes.
- Write outputs into the vault or `$TMPDIR`, never `/tmp`.