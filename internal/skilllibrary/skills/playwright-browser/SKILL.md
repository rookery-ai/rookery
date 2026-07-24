---
name: playwright-browser
description: Use this skill whenever the user wants to interact with a JavaScript-rendered page or a real browser — scraping SPAs, clicking through a flow, taking screenshots, or reading content that loads after page load. Triggers include "the page is blank when scraped", "it's a SPA", "click the button then read", "take a screenshot of the page", "log in and fetch".
version: 1.0.0
license: MIT-0
category: Web & Research
metadata:
  requires:
    bins: [playwright]
  install:
    - kind: pip
      package: playwright
    - kind: binary
      bin: playwright
      # The `playwright` console script lands at $HOME/.local/bin/playwright
      # after `pip install --user playwright`. Then run
      # `playwright install chromium` to fetch the browser binary into the
      # user's persistent cache. The env block gives you the absolute path.
      url: https://pypi.org/simple/playwright
---

# Playwright Browser

Drive a real headless browser with `playwright` to read JavaScript-rendered
pages, click through flows, fill forms, and capture screenshots. Use this when
the `web-research` skill returns empty/missing content (the page renders client-side).

## Requirements

- `playwright` (Python) — `python3 -m pip install --user playwright`.
- Browser binary — after the pip install, fetch it once:
  `playwright install chromium` (or `firefox`). The browser lands in the user's
  persistent cache (`~/.cache/ms-playwright/`), shared across runs.

The runtime environment block tells you the absolute path of the `playwright`
console script. Invoke it by that path.

## Read a rendered page

```python
from playwright.sync_api import sync_playwright
import json, sys

url = sys.argv[1]
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.goto(url, wait_until="networkidle", timeout=30000)
    page.wait_for_timeout(500)  # let late JS settle
    text = page.inner_text("body")
    print(json.dumps({"url": url, "text": text[:8000]}, ensure_ascii=False))
    browser.close()
```

## Click a flow + screenshot

```python
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
    b = p.chromium.launch(headless=True)
    pg = b.new_page()
    pg.goto("https://example.com")
    pg.click("button#load-more")
    pg.wait_for_load_state("networkidle")
    pg.screenshot(path="result.png", full_page=True)
    b.close()
```

## Best practices

- `wait_until="networkidle"` + a short settle delay beats flaky scrapes.
- Set an explicit `timeout` (default 30s) — never hang on a stuck page.
- Reuse one browser context across multiple pages; close it when done.
- Write screenshots/extracted data into the vault or `$TMPDIR`, never `/tmp`.
- For heavy multi-step login flows on a connected service (Gmail, GitHub, …),
  prefer the user's native connector tools (if that service is connected) over scripted logins.