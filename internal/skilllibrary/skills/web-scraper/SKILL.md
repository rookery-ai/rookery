---
name: web-scraper
description: Use this skill whenever the user wants to fetch a web page and extract content from its HTML — scraping a table, extracting an article, pulling a product/spec, or monitoring a page. Triggers include "scrape this page", "extract the table from", "fetch and parse", "get the article text".
version: 1.0.0
license: MIT-0
category: Web & Research
metadata:
  openclaw:
    install:
      - kind: pip
        package: requests
      - kind: pip
        package: beautifulsoup4
---

# Web Scraper

Fetch a static web page and extract content with `requests` + `beautifulsoup4`.
For JavaScript-rendered pages (content missing from raw HTML), use the
`playwright-browser` skill instead.

## Requirements

- `requests` (Python) — `python3 -m pip install --user requests`.
- `beautifulsoup4` (Python) — `python3 -m pip install --user beautifulsoup4`.
- Optional `lxml` parser — `pip install --user lxml` (faster; bs4 falls back to
  html.parser if absent).

## Fetch + extract

```python
import requests
from bs4 import BeautifulSoup
import json, sys

url = sys.argv[1]
headers = {"User-Agent": "Mozilla/5.0 (compatible; simple-agents/1.0)"}
html = requests.get(url, headers=headers, timeout=30).text
soup = BeautifulSoup(html, "lxml")

# Article text
text = "\n".join(p.get_text(strip=True) for p in soup.find_all("p") if p.get_text(strip=True))
print(json.dumps({"url": url, "text": text[:8000]}, ensure_ascii=False))

# First table as rows
table = soup.find("table")
if table:
    rows = [[c.get_text(strip=True) for c in r.find_all(["th","td"])] for r in table.find_all("tr")]
    print(json.dumps({"rows": rows}, ensure_ascii=False))
```

## Best practices

- Set a descriptive `User-Agent` and respect `robots.txt` for crawling at scale.
- Timeouts always (`timeout=30`) — never hang on a dead host.
- If the page is empty but looks JS-heavy, switch to `playwright-browser`.
- Write extracted data into the vault, never `/tmp`.
- For auth-gated sites, prefer the `composio-toolkit` skill (REST) over scraping
  brittle login flows.