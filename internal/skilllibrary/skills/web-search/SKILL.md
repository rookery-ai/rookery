---
name: web-search
description: Use this skill whenever the user wants to look something up on the web — research a topic, find current facts, check documentation, or get fresh URLs. Triggers include "search the web for", "look up", "find recent info on", "what's the latest on", "research this".
version: 1.0.0
license: MIT-0
category: Web & Research
metadata:
  openclaw:
    requires:
      env: [WEB_SEARCH_TOOL]
    install:
      - kind: pip
        package: duckduckgo-search
---

# Web Search

Search the web and synthesize the results for the user. This is an LLM-driven
skill: you run the search, read the snippets/titles/URLs, and write a concise,
citation-bearing summary in your own words.

## Requirements

- `duckduckgo-search` (Python) — `python3 -m pip install --user duckduckgo-search`.
  No API key needed for basic DuckDuckGo results.
- Optional SerpAPI/Brave/Tavily: if the `WEB_SEARCH_TOOL` env var and a secret
  are set, prefer the paid engine; otherwise default to DuckDuckGo.

The runtime environment block tells you which search tools/secrets are available.

## Run a search (DuckDuckGo)

```python
from duckduckgo_search import DDGS
import json, sys
q = sys.argv[1] if len(sys.argv) > 1 else ""
results = []
with DDGS() as ddgs:
    for r in ddgs.text(q, max_results=8):
        results.append({"title": r["title"], "url": r["href"], "body": r["body"]})
print(json.dumps(results))
```

## How to answer (LLM synthesis)

1. Run the search above (or several, narrowing the query if the first batch is weak).
2. Read the titles + snippets. If a snippet is thin, fetch the top URL with the
   `web-scraper` skill or a plain HTTP GET to read the page.
3. Synthesize a concise answer with inline source links. Prefer the most recent
   and most authoritative sources. Distinguish "the web says" from "I know".
4. If results conflict, say so and cite both sides.

## Best practices

- Prefer specific queries; broaden only if the first search is empty.
- Always cite URLs — never present web-found facts without a source.
- For time-sensitive facts (prices, versions, news), note the search date.
- If a search engine rate-limits you, back off and retry, or switch engines.