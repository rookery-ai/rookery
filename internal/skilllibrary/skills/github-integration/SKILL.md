---
name: github-integration
description: Use this skill whenever the user wants to work with GitHub repositories — list/open/close issues, create/review PRs, read a repo's files, search code, check CI status, or create a release. Triggers include "open an issue in", "list open prs", "read the repo files", "search the codebase on github", "create a release".
version: 1.0.0
license: MIT-0
category: Integrations
metadata:
  openclaw:
    requires:
      anyBins: [GITHUB_TOKEN]
    install:
      - kind: pip
        package: requests
---

# GitHub Integration

Act on GitHub repositories. Preferred path is the **GitHub REST API** (token
from secrets); if the user has Composio connected for GitHub, the
`composio-toolkit` skill is an alternative (OAuth, no token management).

## Requirements

- `GITHUB_TOKEN` env var (a fine-grained or classic PAT with the needed scopes) —
  read from `os.environ`, **never hardcode**. If absent, fall back to Composio.
- `requests` (Python) — `python3 -m pip install --user requests`.

The runtime environment block tells you whether `GITHUB_TOKEN` is available.

## REST API (preferred)

- Base: `https://api.github.com`
- Auth: header `Authorization: Bearer <GITHUB_TOKEN>`
- Rate limit: 5000/hr authenticated.

```python
import requests, os, json, sys
H = {"Authorization": f"Bearer {os.environ['GITHUB_TOKEN']}",
     "Accept": "application/vnd.github+json"}
owner, repo = "owner", "repo"

# Open an issue
r = requests.post(f"https://api.github.com/repos/{owner}/{repo}/issues",
                  headers=H, json={"title":"Bug report","body":"...","labels":["bug"]})
print(r.status_code, r.json().get("html_url"))

# List open PRs
prs = requests.get(f"https://api.github.com/repos/{owner}/{repo}/pulls?state=open",
                   headers=H).json()
print(json.dumps([{"n":p["number"],"title":p["title"]} for p in prs]))

# Read a file
r = requests.get(f"https://api.github.com/repos/{owner}/{repo}/contents/README.md",
                 headers=H, params={"ref":"main"})
import base64
print(base64.b64decode(r.json()["content"]).decode())
```

## How to act (LLM-driven)

1. Confirm owner/repo (ask if ambiguous).
2. Pick the smallest-scoped call that does the job.
3. Execute via REST; inspect the response; report the URL/status to the user.

## Best practices

- **Never hardcode** tokens — `os.environ["GITHUB_TOKEN"]`.
- Confirm destructive/write actions (merge, delete branch, force-push) with the
  user before executing.
- Prefer the REST API over cloning when you only need to read a few files.
- For large repos or code search, `GET /search/code` and the Git Trees API beat
  cloning everything.