---
name: git-and-github
description: Use this skill for work inside a git repository on this machine — cloning, reading history, diffing, branching, committing, and inspecting what changed. Triggers include "clone this repo", "what changed in", "show me the diff", "commit these changes", "check the git log", "which branch", "read the repo". For GitHub's API (issues, pull requests, releases, CI status) use the connected GitHub service instead.
version: 1.0.0
license: MIT-0
category: Development
---

# Git and GitHub

Local repository work. For anything that talks to GitHub's API, use the connected
GitHub account instead — see the bottom of this document.

## Local git

Run git through an argument list, never a shell string:

```python
import subprocess
result = subprocess.run(["git", "-C", repo, "log", "--oneline", "-20"],
                        capture_output=True, text=True)
```

Useful invocations:

| Goal | Command |
|---|---|
| Clone | `git clone --depth 1 <url> <dir>` |
| Recent history | `git -C <dir> log --oneline -20` |
| What changed | `git -C <dir> diff --stat HEAD~1` |
| Current branch | `git -C <dir> rev-parse --abbrev-ref HEAD` |
| Who changed a line | `git -C <dir> blame -L 10,20 <file>` |
| Search history | `git -C <dir> log -S "<string>" --oneline` |

Rules:

- `--depth 1` unless history is the point. A full clone of a large repo wastes the run.
- Clone into `$TMPDIR`, not the knowledge base, unless the user asked to keep it.
- NEVER push, force-push, or rewrite history unless the user explicitly asked in this
  conversation. Reading is safe; writing to a remote is not reversible.
- Read `git status` before committing anything, and report what you are about to commit.

## GitHub's API — use the connection

Issues, pull requests, releases, CI status and code search go through the connected
GitHub account, which is already authenticated. Do NOT hunt for a token, and do not
`pip install` a GitHub SDK — the connection exposes these as tools directly.

If no GitHub account is connected, say so and tell the user to connect one on the
connections page. Do not fall back to unauthenticated scraping.
