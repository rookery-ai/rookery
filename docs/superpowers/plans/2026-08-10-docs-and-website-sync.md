# Documentation and Website Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make it mechanically impossible for `README.md`, `CLAUDE.md`, the documentation site and the landing page to disagree with the source about counts, environment variables, command names, provider names and brand logos.

**Architecture:** Four layers. A tracked Python checker (`scripts/check-docs-sync.py`) is the only enforcement — it derives facts from source and asserts the prose agrees, skipping website checks when that repository is absent. A user-level `docs-sync` skill carries the procedure for updating both repositories. A path-guarded `PostToolUse` hook gives a cheap best-effort nudge at edit time. A short `CLAUDE.md` section points at the skill.

**Tech Stack:** Python 3 (already a project dependency — the AST guardrails shell out to it), GNU Make, Bash for the hook, Astro/Starlight for the website.

## Global Constraints

- **Reconciliation lands before the gate.** Every documentation error is fixed in Tasks 2–6. `make ci` wiring is Task 7 and must not happen earlier. Wiring a gate while known drift exists turns the next unrelated PR red, and the cheap fix is to weaken the assertion.
- **The checker is Python, not Bash.** The spec named `check-docs-sync.sh`; this plan uses `scripts/check-docs-sync.py`. Set arithmetic, frontmatter handling and multi-file regex capture are where Bash checkers die. `make docs-sync-check` is the interface either way.
- **Website assertions skip, never fail, when `rookery-web` is absent.** `make ci` runs where no second checkout exists.
- **Never commit to `main`** in either repository. Website work happens in a git worktree inside `~/rookery-web`, never in the checkout directly — it may hold uncommitted work.
- **Conventional Commits** in both repositories.
- **Real counts as of 2026-08-10:** 91 connector providers, 471 actions, 22 core skills, 14 `ROOKERY_*` variables in source (9 user-facing + 5 internal), 7 user-facing CLI commands.
- **Do not modify the superpowers plugin.** Its path is version-pinned at `6.2.0/`; edits are lost on upgrade.
- **`rookery/.gitignore` keeps ignoring `.claude/`.** The skill and hook live at user level in `~/.claude/`.

---

### Task 1: Checker foundation

**Files:**
- Create: `scripts/check-docs-sync.py`
- Modify: `Makefile` (add `docs-sync-check` target after the `test` target at line 83)

**Interfaces:**
- Consumes: nothing.
- Produces: `product_root() -> Path`, `web_root() -> Path | None`, `read(path: Path) -> str`, `fail(label: str, detail: str) -> None`, `register(fn)` decorator collecting assertion functions, and a `--selftest` flag. Later tasks add one assertion function each via `@register`.

- [ ] **Step 1: Write the failing test**

Create `scripts/check-docs-sync.py` with only the selftest harness and one deliberately failing case, to prove the harness reports failure:

```python
#!/usr/bin/env python3
"""Assert documentation claims match the source they describe.

Product-side assertions always run. Website assertions run only when the
rookery-web repository can be found, and are skipped (not failed) otherwise:
make ci runs in an environment that has no second checkout, and a gate that
depends on a repository it cannot see is a gate that gets removed.
"""
import os
import re
import subprocess
import sys
from pathlib import Path

FAILURES: list[tuple[str, str]] = []
SKIPS: list[str] = []
ASSERTIONS: list = []


def register(fn):
    ASSERTIONS.append(fn)
    return fn


def fail(label: str, detail: str) -> None:
    FAILURES.append((label, detail))


def skip(reason: str) -> None:
    SKIPS.append(reason)


def product_root() -> Path:
    return Path(__file__).resolve().parent.parent


def web_root() -> Path | None:
    """Locate rookery-web.

    The obvious sibling heuristic (product_root().parent / "rookery-web") is
    WRONG inside a git worktree, which is where this most often runs: there,
    product_root() is .claude/worktrees/<name> and its parent is the worktrees
    directory. Resolve the main checkout through git's common dir first.
    """
    env = os.environ.get("ROOKERY_WEB_DIR")
    candidates = []
    if env:
        candidates.append(Path(env))
    try:
        common = subprocess.run(
            ["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
            cwd=product_root(), capture_output=True, text=True, check=True,
        ).stdout.strip()
        if common:
            candidates.append(Path(common).parent.parent / "rookery-web")
    except (subprocess.CalledProcessError, OSError):
        pass
    candidates.append(Path.home() / "rookery-web")
    for c in candidates:
        if (c / "astro.config.mjs").exists():
            return c
    return None


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def selftest() -> int:
    """Run each assertion's inline cases against synthetic input.

    The real repository is not a regression test: once drift is fixed, a
    broken assertion passes silently. These cases keep the detectors honest.
    """
    cases = [fn for fn in ASSERTIONS if hasattr(fn, "selftest")]
    errors = 0
    for fn in cases:
        try:
            fn.selftest()
            print(f"selftest ok: {fn.__name__}")
        except AssertionError as exc:
            print(f"selftest FAILED: {fn.__name__}: {exc}")
            errors += 1
    if not cases:
        print("selftest: no cases registered")
    return 1 if errors else 0


def main() -> int:
    if "--selftest" in sys.argv:
        return selftest()
    for fn in ASSERTIONS:
        fn()
    for reason in SKIPS:
        print(f"SKIP: {reason}")
    if FAILURES:
        print(f"\n{len(FAILURES)} documentation claim(s) disagree with the source:\n")
        for label, detail in FAILURES:
            print(f"  {label}\n    {detail}")
        print("\nFix the documentation, or the source, so they agree.")
        return 1
    print(f"docs-sync: {len(ASSERTIONS)} assertion(s) passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 2: Run it to verify the harness works with zero assertions**

```bash
chmod +x scripts/check-docs-sync.py
python3 scripts/check-docs-sync.py
```

Expected: `docs-sync: 0 assertion(s) passed`, exit 0.

- [ ] **Step 3: Verify web discovery resolves from inside a worktree**

```bash
python3 -c "
import sys; sys.path.insert(0, 'scripts')
import importlib.util
spec = importlib.util.spec_from_file_location('c', 'scripts/check-docs-sync.py')
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
print('web root:', m.web_root())
"
```

Expected: prints `/home/rookie/rookery-web`. If it prints `None`, the git common-dir resolution is broken — fix before continuing, because every website assertion depends on it.

- [ ] **Step 4: Add the Makefile target**

Insert after line 83 (the `test` target's recipe):

```makefile
## docs-sync-check: assert the docs agree with the source (both repos)
docs-sync-check:
	python3 scripts/check-docs-sync.py

docs-sync-selftest:
	python3 scripts/check-docs-sync.py --selftest
```

- [ ] **Step 5: Run both targets**

```bash
make docs-sync-check && make docs-sync-selftest
```

Expected: both exit 0.

- [ ] **Step 6: Commit**

```bash
git add scripts/check-docs-sync.py Makefile
git commit -m "chore: add docs-sync checker foundation"
```

---

### Task 2: Claims table — catches README's 45-against-91

**Files:**
- Modify: `scripts/check-docs-sync.py`
- Modify: `README.md:6`, `README.md:45`

**Interfaces:**
- Consumes: `register`, `fail`, `read`, `product_root`, `web_root`, `skip` from Task 1.
- Produces: `providers() -> set[str]`, `action_count() -> int`, `core_skills() -> set[str]`, and `CLAIMS: list[tuple[str, str, str]]` of `(relative_path, regex_with_one_group, key)` where `key` indexes `derived()`.

- [ ] **Step 1: Write the assertion (the failing test — real drift is the fixture)**

Add to `scripts/check-docs-sync.py`, above `def selftest()`:

```python
def providers() -> set[str]:
    d = product_root() / "internal" / "connectors" / "providers"
    return {p.stem for p in d.glob("*.yaml")}


def action_count() -> int:
    d = product_root() / "internal" / "connectors" / "connectors"
    return sum(
        len(re.findall(r"^  - name:", read(p), re.M)) for p in d.glob("*.yaml")
    )


def core_skills() -> set[str]:
    d = product_root() / "internal" / "skilllibrary" / "skills"
    return {p.name for p in d.iterdir() if p.is_dir()}


def derived() -> dict[str, int]:
    return {
        "providers": len(providers()),
        "actions": action_count(),
        "skills": len(core_skills()),
    }


# (repo, relative path, regex with exactly one capture group, derived key)
# The regex is matched against the WHOLE file including YAML frontmatter: the
# skills claim lives in the `description:` field of concepts/skills.md, not in
# its prose, and a body-only scan would match nothing and pass silently.
CLAIMS = [
    ("product", "README.md", r"reach (\d+) external services", "providers"),
    ("product", "README.md", r"\*\*Connectors\*\* — (\d+) providers", "providers"),
    ("product", "README.md", r"providers, ~(\d+) curated actions", "actions"),
    ("product", "README.md", r"reusable capability documents, (\d+) bundled", "skills"),
    ("web", "src/pages/index.astro", r"(\d+)\+? services", "providers"),
    ("web", "src/content/docs/docs/concepts/skills.md", r"— (\d+) built in", "skills"),
]


@register
def check_claims() -> None:
    values = derived()
    web = web_root()
    for repo, rel, pattern, key in CLAIMS:
        if repo == "web":
            if web is None:
                continue
            path = web / rel
        else:
            path = product_root() / rel
        if not path.exists():
            fail("claims", f"{rel}: file not found")
            continue
        text = read(path)
        m = re.search(pattern, text)
        if not m:
            fail("claims", f"{rel}: no text matched /{pattern}/ — the claim moved or was reworded")
            continue
        claimed, actual = int(m.group(1)), values[key]
        if claimed != actual:
            line = text[: m.start()].count("\n") + 1
            fail("claims", f"{rel}:{line} claims {claimed} {key}, source has {actual}")
    if web is None:
        skip("rookery-web not found — website claims not checked")
```

- [ ] **Step 2: Run it to verify it fails on the real drift**

```bash
make docs-sync-check
```

Expected: FAIL, exit 1, reporting `README.md:6 claims 45 providers, source has 91` and `README.md:45 claims 45 providers, source has 91` and `README.md:45 claims 272 actions, source has 471`. The landing-page `100+` line is handled in Task 3; if it reports here too, that is fine.

- [ ] **Step 3: Fix `README.md`**

Line 6 — change `reach 45 external services` to `reach 91 external services`.

Line 45 — change:

```
- **Connectors** — 45 providers, ~272 curated actions, self-managed OAuth. No
```

to:

```
- **Connectors** — 91 providers, ~471 curated actions, self-managed OAuth. No
```

- [ ] **Step 4: Run to verify product-side claims pass**

```bash
make docs-sync-check
```

Expected: no `README.md` failures. Failures against `index.astro` may remain (Task 3).

- [ ] **Step 5: Add the selftest case**

Append immediately after `check_claims`:

```python
def _claims_selftest() -> None:
    text = "we reach 45 external services today"
    m = re.search(r"reach (\d+) external services", text)
    assert m and int(m.group(1)) == 45, "claim regex must capture the number"
    assert re.search(r"reach (\d+) external services", "reach ninety-one") is None, \
        "claim regex must not match prose numbers"


check_claims.selftest = _claims_selftest
```

- [ ] **Step 6: Run the selftest**

```bash
make docs-sync-selftest
```

Expected: `selftest ok: check_claims`, exit 0.

- [ ] **Step 7: Commit**

```bash
git add scripts/check-docs-sync.py README.md
git commit -m "fix(docs): correct README connector and action counts

README claimed 45 providers and ~272 actions against a real 91 and 471.
Adds the claims assertion that would have caught it."
```

---

### Task 3: Inflated approximations — catches the landing page's "100+"

**Files:**
- Modify: `scripts/check-docs-sync.py`
- Modify (website): `src/pages/index.astro:380`

**Interfaces:**
- Consumes: `providers`, `register`, `fail`, `web_root` from Tasks 1–2.
- Produces: `check_inflated()` assertion.

This is the first cross-repository write. Establish the worktree workflow here; Task 11 reuses it.

- [ ] **Step 1: Write the assertion**

Add to `scripts/check-docs-sync.py`:

```python
# An exact-number regex cannot catch "100+ services" against a real 91: the
# claim is approximate, and false in the direction that matters.
INFLATABLE = [
    ("src/pages/index.astro", r"(\d+)\+\s*services", "providers"),
    ("src/content/docs/docs/reference/connected-services.md", r"(\d+)\+\s*services", "providers"),
]


@register
def check_inflated() -> None:
    web = web_root()
    if web is None:
        return
    values = derived()
    for rel, pattern, key in INFLATABLE:
        path = web / rel
        if not path.exists():
            continue
        text = read(path)
        for m in re.finditer(pattern, text):
            claimed, actual = int(m.group(1)), values[key]
            if claimed > actual:
                line = text[: m.start()].count("\n") + 1
                fail(
                    "inflated",
                    f"{rel}:{line} claims {claimed}+ {key}, but there are only {actual}",
                )


def _inflated_selftest() -> None:
    m = re.search(r"(\d+)\+\s*services", "100+ services")
    assert m and int(m.group(1)) == 100, "must capture an N+ claim"
    assert 100 > 91, "an N+ claim above the real count is a failure"
    assert re.search(r"(\d+)\+\s*services", "91 services") is None, \
        "an exact claim is the claims table's job, not this one"


check_inflated.selftest = _inflated_selftest
```

- [ ] **Step 2: Run to verify it fails on the real landing page**

```bash
make docs-sync-check
```

Expected: FAIL reporting `src/pages/index.astro:380 claims 100+ providers, but there are only 91`.

- [ ] **Step 3: Create a worktree in the website repository**

Never edit `~/rookery-web` directly — it is the user's checkout and may hold uncommitted work.

```bash
git -C ~/rookery-web fetch origin
git -C ~/rookery-web worktree add -b docs/sync-counts /tmp/rookery-web-sync origin/main
git -C /tmp/rookery-web-sync status --short
```

Expected: worktree created, `status` prints nothing.

- [ ] **Step 4: Fix the landing page claim**

In `/tmp/rookery-web-sync/src/pages/index.astro`, replace `100+ services` with `91 services`.

Verify the surrounding copy still reads correctly — if it says "over 100+ services", the whole phrase needs rewording, not just the number:

```bash
grep -n -B2 -A2 'services' /tmp/rookery-web-sync/src/pages/index.astro | sed -n '1,20p'
```

- [ ] **Step 5: Re-run the check against the worktree**

```bash
ROOKERY_WEB_DIR=/tmp/rookery-web-sync make docs-sync-check
```

Expected: no `inflated` failures.

- [ ] **Step 6: Commit in both repositories**

```bash
git -C /tmp/rookery-web-sync add src/pages/index.astro
git -C /tmp/rookery-web-sync commit -m "fix: state the real service count

The landing page claimed 100+ services against a real 91."

git add scripts/check-docs-sync.py
git commit -m "chore: flag inflated N+ documentation claims"
```

---

### Task 4: Environment variable coverage

**Files:**
- Modify: `scripts/check-docs-sync.py`
- Create: `scripts/docs-sync-internal-env.txt`

**Interfaces:**
- Consumes: `register`, `fail`, `read`, `product_root`, `web_root`.
- Produces: `env_vars() -> set[str]`, `internal_env() -> set[str]`, `check_env()` assertion.

- [ ] **Step 1: Create the internal-only allowlist**

The allowlist is required, not a convenience: source declares 14 variables, the documentation lists 9, and the 5-variable difference is correct — those five are injected by the runtime into coder subprocesses and are never set by an operator. Without it the check reports five false positives on its first run and gets switched off.

Create `scripts/docs-sync-internal-env.txt`:

```
# ROOKERY_* variables the runtime injects into subprocesses. An operator never
# sets these, so they are deliberately absent from the public configuration
# reference. One reason per line — if you cannot write a reason, the variable is
# user-facing and belongs in the docs instead.
ROOKERY_BUILD_PHASE      # set during agent/skill builds; gates mutating connector actions
ROOKERY_CONNECTOR_URL    # loopback connector bridge address, per run
ROOKERY_CONNECTOR_TOKEN  # per-run bearer token for the connector bridge
ROOKERY_KB_URL           # loopback knowledge-base bridge address, per run
ROOKERY_KB_TOKEN         # per-run bearer token for the knowledge-base bridge
```

- [ ] **Step 2: Write the assertion**

```python
def env_vars() -> set[str]:
    out = subprocess.run(
        ["grep", "-rhoE", r'"ROOKERY_[A-Z_]+"', "internal", "cmd", "web"],
        cwd=product_root(), capture_output=True, text=True,
    ).stdout
    return {line.strip('"') for line in out.split() if line}


def internal_env() -> set[str]:
    path = product_root() / "scripts" / "docs-sync-internal-env.txt"
    names = set()
    for line in read(path).splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        names.add(line.split()[0])
    return names


@register
def check_env() -> None:
    web = web_root()
    if web is None:
        return
    path = web / "src/content/docs/docs/operations/configuration.md"
    documented = set(re.findall(r"ROOKERY_[A-Z_]+", read(path)))
    expected = env_vars() - internal_env()
    missing = expected - documented
    for name in sorted(missing):
        fail("env", f"{name} is read by the source but absent from operations/configuration.md")
    stale = documented - env_vars()
    for name in sorted(stale):
        fail("env", f"{name} is documented but no longer read by any source file")


def _env_selftest() -> None:
    source = {"ROOKERY_PORT", "ROOKERY_KB_TOKEN", "ROOKERY_NEW"}
    internal = {"ROOKERY_KB_TOKEN"}
    documented = {"ROOKERY_PORT", "ROOKERY_GONE"}
    assert (source - internal) - documented == {"ROOKERY_NEW"}, "must flag an undocumented public var"
    assert documented - source == {"ROOKERY_GONE"}, "must flag a documented var the source dropped"


check_env.selftest = _env_selftest
```

- [ ] **Step 3: Run the check**

```bash
make docs-sync-check && make docs-sync-selftest
```

Expected: PASS. Source has 14 variables, the allowlist covers 5, and `configuration.md` documents the other 9. If anything is reported, resolve it before continuing — an assertion that fails on day one for reasons outside the reconciliation list gets weakened rather than fixed.

- [ ] **Step 4: Commit**

```bash
git add scripts/check-docs-sync.py scripts/docs-sync-internal-env.txt
git commit -m "chore: assert every public ROOKERY_ variable is documented"
```

---

### Task 5: CLI coverage — catches CLAUDE.md's non-existent `db migrate`

**Files:**
- Modify: `scripts/check-docs-sync.py`
- Modify: `CLAUDE.md` (the "Database migration" lines in the Commands block)

**Interfaces:**
- Consumes: `register`, `fail`, `read`, `product_root`, `web_root`.
- Produces: `declared_cli_names() -> set[str]`, `check_cli()` assertion.

The website's `reference/cli.md` is correct — it states there is no separate migration command. `CLAUDE.md` gives the command line for one. There is no `db` or `migrate` subcommand registered anywhere.

- [ ] **Step 1: Write the assertion**

**Do not try to reconstruct the command tree by parsing Go.** That approach was
built and measured against the real source, and it fails two ways: `backupCommand`
lives in `backup_cmd.go` rather than `main.go`, so a `main.go`-only scan loses
`backup` entirely and then falsely reports that `cli.md` documents a command that
does not exist; and resolving a constructor to "its first `Name:`" returns `dir`
for `backupCommand`, because the first `Name:` in that body belongs to a *flag*.
Both are day-one false failures, which is how an assertion gets weakened instead
of fixed.

Use containment instead: collect every `Name: "..."` string declared anywhere in
`cmd/rookery` — commands and flags together, 26 of them — and assert that
documented command words appear in that set.

This is deliberately one-directional. It catches a documented command that does
not exist (the `db migrate` case) and a command removed from the source while
still documented. It does **not** catch a newly added command nobody documented;
that case belongs to the hook and the skill's trigger map, and pretending
otherwise would require the parsing that does not work.

```python
def declared_cli_names() -> set[str]:
    """Every Name: string in cmd/rookery — commands and flags alike.

    Deliberately does not distinguish the two. See the plan: reconstructing the
    command tree from source is unreliable, and a flag name in the set only ever
    makes this check more permissive, never wrong in the failing direction.
    """
    d = product_root() / "cmd" / "rookery"
    blob = "\n".join(read(p) for p in sorted(d.glob("*.go")))
    return set(re.findall(r'Name:\s*"([^"]+)"', blob))


@register
def check_cli() -> None:
    declared = declared_cli_names()
    web = web_root()
    if web is not None:
        path = web / "src/content/docs/docs/reference/cli.md"
        for m in re.finditer(r"^## (\S+)", read(path), re.M):
            if m.group(1) not in declared:
                fail("cli", f"reference/cli.md documents '{m.group(1)}', which no source file declares")
    # Product docs invoke commands inline. A command named here that does not
    # exist is how CLAUDE.md came to document `rookery db migrate`.
    for rel in ("CLAUDE.md", "README.md"):
        text = read(product_root() / rel)
        for m in re.finditer(r"rookery ([a-z][a-z-]+)\b", text):
            word = m.group(1)
            if word not in declared:
                line = text[: m.start()].count("\n") + 1
                fail("cli", f"{rel}:{line} invokes 'rookery {word}', which no source file declares")


def _cli_selftest() -> None:
    declared = {"serve", "owner", "backup", "dir"}
    documented = ["serve", "owner", "backup", "db"]
    missing = [d for d in documented if d not in declared]
    assert missing == ["db"], "must flag a documented command the source never declares"
    assert "serve" in declared, "a real command must not be flagged"


check_cli.selftest = _cli_selftest
```

- [ ] **Step 2: Run to verify it fails on `CLAUDE.md`**

```bash
make docs-sync-check
```

Expected: FAIL with exactly one `cli` entry — `CLAUDE.md:40 invokes 'rookery db', which no source file declares`. This was verified against the real repository while writing the plan: all seven `cli.md` headings resolve, and `db` is the only unresolved invocation. Any *additional* report means something changed since; fix each one in Step 3.

- [ ] **Step 3: Fix `CLAUDE.md`**

Find the Commands block and remove the migration command, replacing it with the real behaviour. Change:

```
# Database migration
./bin/rookery db migrate
```

to:

```
# Migrations are applied automatically when the database is opened —
# there is no separate migration command.
```

Search the whole file for any other occurrence and remove it too:

```bash
grep -n 'db migrate' CLAUDE.md
```

Expected after the fix: no output.

- [ ] **Step 4: Re-run the check and the selftest**

```bash
make docs-sync-check && make docs-sync-selftest
```

Expected: no `cli` failures, `selftest ok: check_cli`.

- [ ] **Step 5: Commit**

```bash
git add scripts/check-docs-sync.py CLAUDE.md
git commit -m "fix(docs): drop the db migrate command that never existed

CLAUDE.md documented ./bin/rookery db migrate; no db or migrate subcommand
is registered. The website already stated the correct behaviour."
```

---

### Task 6: Provider name and logo coverage — catches CLAUDE.md's Zoom

**Files:**
- Modify: `scripts/check-docs-sync.py`
- Modify: `CLAUDE.md:260`

**Interfaces:**
- Consumes: `providers`, `register`, `fail`, `read`, `product_root`, `web_root`.
- Produces: `check_provider_names()`, `check_logos()` assertions.

`reference/connected-services.md` states no count — it enumerates services by name, which is a better page for it. So the assertion there is coverage, not arithmetic. The removal half is what catches Zoom: its YAML was deleted two releases ago and `CLAUDE.md` still lists it among the providers.

- [ ] **Step 1: Write both assertions**

```python
# Provider slugs whose display name cannot be derived from the filename.
DISPLAY_NAMES = {
    "google_searchconsole": "Search Console",
    "firefly_iii": "Firefly III",
    "home_assistant": "Home Assistant",
    "hackernews": "Hacker News",
}

# Names that were once providers and must never reappear in prose as though
# they still are. Removing a provider means removing every mention of it.
REMOVED_PROVIDERS = {"Zoom", "Fitbit"}


def display_name(slug: str) -> str:
    return DISPLAY_NAMES.get(slug, slug.replace("_", " ").title())


@register
def check_provider_names() -> None:
    slugs = providers()
    web = web_root()
    if web is not None:
        path = web / "src/content/docs/docs/reference/connected-services.md"
        text = read(path)
        for slug in sorted(slugs):
            name = display_name(slug)
            if name.lower() not in text.lower():
                fail("providers", f"provider '{name}' is not named in reference/connected-services.md")
    # A provider that was removed must not survive in prose anywhere.
    targets = [(product_root() / "CLAUDE.md", "CLAUDE.md"),
               (product_root() / "README.md", "README.md")]
    if web is not None:
        targets.append((web / "src/content/docs/docs/reference/connected-services.md",
                        "connected-services.md"))
    for path, label in targets:
        text = read(path)
        for gone in sorted(REMOVED_PROVIDERS):
            if gone in slugs:
                continue
            for m in re.finditer(rf"\b{gone}\b", text):
                line = text[: m.start()].count("\n") + 1
                fail("providers", f"{label}:{line} names '{gone}', which is no longer a provider")


@register
def check_logos() -> None:
    web = web_root()
    if web is None:
        return
    logos = {p.stem for p in (web / "src" / "assets" / "logos").glob("*.svg")}
    # Set equality is NOT asserted: the website legitimately carries logos with
    # no connector behind them (claude.svg, cursor.svg are coder marks shown on
    # the landing page). Coverage in one direction only.
    for slug in sorted(providers() - logos):
        fail("logos", f"provider '{slug}' has no logo at src/assets/logos/{slug}.svg")


def _provider_selftest() -> None:
    assert display_name("google_tasks") == "Google Tasks", "slug should title-case"
    assert display_name("firefly_iii") == "Firefly III", "override should win"
    slugs, logos = {"gmail", "notion"}, {"gmail", "claude", "cursor"}
    assert slugs - logos == {"notion"}, "must flag a provider with no logo"
    assert not (logos - slugs) & slugs, "extra website logos must not be flagged"


check_provider_names.selftest = _provider_selftest
```

- [ ] **Step 2: Run to verify it fails on Zoom**

```bash
make docs-sync-check
```

Expected: FAIL reporting `CLAUDE.md:260 names 'Zoom', which is no longer a provider`. It may also report providers missing from `connected-services.md` — record each; fix them in Step 4.

- [ ] **Step 3: Fix `CLAUDE.md:260`**

Remove `Zoom, ` from the parenthesised provider list on that line. Check the second enumeration too:

```bash
grep -n 'Zoom' CLAUDE.md
```

Expected after the fix: no output, or only lines that describe Zoom's *removal* as history. If a line documents the removal deliberately, add a narrow exemption rather than deleting the history — the `TestRemovedProvidersStayRemoved` rationale lives in that prose.

- [ ] **Step 4: Fix any provider missing from the website page**

For each provider reported in Step 2, add it to the correct category section of `/tmp/rookery-web-sync/src/content/docs/docs/reference/connected-services.md`. Then:

```bash
ROOKERY_WEB_DIR=/tmp/rookery-web-sync make docs-sync-check
```

- [ ] **Step 5: Run the selftest**

```bash
make docs-sync-selftest
```

Expected: `selftest ok: check_provider_names`.

- [ ] **Step 6: Commit**

```bash
git add scripts/check-docs-sync.py CLAUDE.md
git commit -m "fix(docs): drop Zoom from the provider list

Zoom was removed two releases ago; CLAUDE.md still listed it among the 91
providers. Adds the removed-provider assertion that catches it."
```

If the website worktree changed:

```bash
git -C /tmp/rookery-web-sync add -A
git -C /tmp/rookery-web-sync commit -m "docs: cover every connector provider by name"
```

---

### Task 7: Wire into `make ci`

**Files:**
- Modify: `Makefile:90` (the `ci` target's prerequisite list)
- Modify: `.github/workflows/pr.yml` (the `go` job)

Do this only after Tasks 2–6 are green. Wiring the gate while known drift exists turns the next unrelated pull request red for pre-existing errors, and the cheapest way out is to weaken the assertion.

**`make ci` wiring alone gates nothing on GitHub.** `pr.yml` is the only thing
that actually gates a pull request, and it never calls `make` — it runs
`gofmt`/`go vet`/`go test` inline in its own `go` job. A `ci-docs` prerequisite
added only to the `Makefile` is real for a contributor who happens to run
`make ci` locally and invisible to CI itself. This was found during
implementation (after `Makefile` wiring landed with nothing catching it) and
fixed by adding the same two commands — `check-docs-sync.py --selftest` then
`check-docs-sync.py` — as two more inline steps in the `go` job, right after
`go test -race`, reusing the `python3` that job already verifies is present.
Both changes below are required; the `Makefile` change alone repeats the gap.

**Interfaces:**
- Consumes: the `docs-sync-check` and `docs-sync-selftest` targets from Task 1.
- Produces: `ci-docs` target, and two new steps in `pr.yml`'s `go` job.

- [ ] **Step 1: Verify everything is green before wiring**

```bash
make docs-sync-check && make docs-sync-selftest
```

Expected: both exit 0. **If either fails, stop** — finish the reconciliation first.

- [ ] **Step 2: Add the `ci-docs` target and add it to `ci`**

Change line 90 from:

```makefile
ci: ci-fmt ci-vet ci-test ci-cross ci-ui
```

to:

```makefile
ci: ci-fmt ci-vet ci-test ci-cross ci-ui ci-docs
```

Add after the `ci-ui` recipe (line 115):

```makefile
## ci-docs: assert documentation claims match the source. Website assertions
## skip when rookery-web is absent, which is the normal case in CI.
ci-docs:
	python3 scripts/check-docs-sync.py --selftest
	python3 scripts/check-docs-sync.py
```

- [ ] **Step 3: Add the same two commands to `pr.yml`'s `go` job**

`pr.yml` never calls `make`, so `ci-docs` alone reaches nobody on a real pull
request. Add two inline steps to the existing `go` job, right after
`go test -race`, reusing the `python3` that job already verifies is present:

```yaml
      - name: Docs sync selftest
        run: python3 scripts/check-docs-sync.py --selftest

      - name: Docs sync check
        run: python3 scripts/check-docs-sync.py
```

- [ ] **Step 4: Verify the CI path works without the website repository**

`ROOKERY_WEB_DIR=/nonexistent HOME=/nonexistent make ci-docs` does **not**
prove this: `web_root()` resolves its sibling candidate from git's own common
dir (`git rev-parse --path-format=absolute --git-common-dir` →
`.parent.parent / "rookery-web"`), which finds the real checkout's real
sibling regardless of either variable. Neither env var is consulted for that
candidate.

What actually isolates it: copy the repository into a throwaway location whose
parent has no `rookery-web`, `git init` it there so the common-dir lookup
resolves inside the copy, and isolate `HOME` too (the fallback candidate is
`~/rookery-web`, which exists on this machine). For example:

```bash
mkdir -p /tmp/docs-sync-isolation/isolated-home
cp -r . /tmp/docs-sync-isolation/rookery-copy
rm -rf /tmp/docs-sync-isolation/rookery-copy/.git
git -C /tmp/docs-sync-isolation/rookery-copy init -q
git -C /tmp/docs-sync-isolation/rookery-copy add -A
git -C /tmp/docs-sync-isolation/rookery-copy -c user.email=t@t -c user.name=t commit -q -m throwaway
# in a subshell/script, so HOME is not exported into this session:
#   unset ROOKERY_WEB_DIR; export HOME=/tmp/docs-sync-isolation/isolated-home
#   cd /tmp/docs-sync-isolation/rookery-copy && python3 scripts/check-docs-sync.py
```

Expected: exit 0, printing `SKIP: rookery-web not found — website claims not
checked` followed by `docs-sync: 7 assertion(s) passed`. This is the CI
environment; if it fails, the skip semantics are broken and the gate will be
removed within a week. Remove the throwaway copy afterward.

- [ ] **Step 5: Commit**

Shipped as two commits, since the `pr.yml` gap was found after the `Makefile`
change had already landed and been believed sufficient:

```bash
git add Makefile
git commit -m "ci: gate on documentation claims matching the source"
# ...then, once the gap above was found:
git add .github/workflows/pr.yml CLAUDE.md
git commit -m "ci: run the docs-sync gate on every pull request"
```

---

### Task 8: The `docs-sync` skill

**Files:**
- Create: `~/.claude/skills/docs-sync/SKILL.md`

**Interfaces:**
- Consumes: `make docs-sync-check` from Task 1.
- Produces: a skill named `docs-sync`, discoverable by name.

- [ ] **Step 1: Write the skill**

```markdown
---
name: docs-sync
description: Use when finishing work in the rookery product repository, before opening a pull request - checks whether the change affects README.md, CLAUDE.md, or the rookery-web documentation site, and updates every surface it touches.
---

# Documentation and website sync

Four surfaces describe Rookery, and each can be wrong without anything
failing: `README.md`, `CLAUDE.md`, the documentation site, and the landing
page. This skill keeps them agreeing with the source.

## When to run

At finish time, before opening a pull request in `~/rookery`. Also after any
release, for the sweep at the bottom of this file.

## Procedure

1. Diff the branch against `main`: `git diff --name-only main...HEAD`.
2. Look every changed path up in the trigger map below.
3. **If nothing maps, stop** and say "no documentation-facing change". Most
   commits are in this class and must cost nothing.
4. Update `README.md` and `CLAUDE.md` where the change invalidates a claim
   they make. The product side is not an afterthought — it holds the oldest
   errors this mechanism has found.
5. Update the website (see *Working in the website repository*).
6. Run `ROOKERY_WEB_DIR=<worktree> make docs-sync-check`. A failure blocks the
   pull request.
7. Open a pull request in each repository, the website one linking to the
   product one. Report both URLs.

## Trigger map

| Change in the product | Update in the website |
|---|---|
| `internal/connectors/{providers,connectors}/*.yaml` added or removed | `reference/connected-services.md`, the service count in `index.astro`, `LogoWall.astro`, `src/assets/logos/<provider>.svg` |
| `coder.APIProviders()` | `getting-started/choosing-a-model.md`, `concepts/models.md`, `ModelChips.tsx` |
| A `ROOKERY_*` variable added, renamed, or given a new default | `operations/configuration.md` |
| A subcommand added or changed in `cmd/rookery` | `reference/cli.md` |
| `internal/skilllibrary/skills/*` | `concepts/skills.md` |
| A chat adapter added in `internal/gateway` | `concepts/notifications.md` |
| A backup `Destination` added | `concepts/backup-and-restore.md` |
| `.goreleaser.yaml`, `packaging/`, `Dockerfile` | `installation/*.md` |
| `/api/v1` routes added or removed | `reference/api.md` |
| A user-visible SPA feature | the matching `concepts/*.md` |
| A new website page | the sidebar in `astro.config.mjs` — navigation is hand-maintained |

## Working in the website repository

`~/rookery-web` is the user's own checkout and may hold uncommitted work.
Never edit it directly. Use a worktree:

```bash
git -C ~/rookery-web fetch origin
git -C ~/rookery-web worktree add -b docs/<slug> /tmp/rookery-web-<slug> origin/main
```

Commit there, push, open the pull request, and remove the worktree when the
pull request is merged:

```bash
git -C ~/rookery-web worktree remove /tmp/rookery-web-<slug>
```

Never commit to `main` in either repository. Both use Conventional Commits.

## Verify claims against source, never against prose

Every number, variable name, command name and provider name is checked against
the source that implements it — not against another document. `README.md`
understated the connector count by half for months because it was copied
forward instead of measured.

`make docs-sync-check` mechanises the checkable part. It does not check whether
a paragraph *describes* a feature correctly; that still needs reading.

## Release sweep

The website pins no version — it uses `<version>` placeholders and links to the
releases page. So there is nothing to sync per release, and the useful form of
a release trigger is a sweep: when release-please cuts a release, walk the
generated changelog for user-visible entries and confirm each has a home in the
documentation. This catches a feature that shipped without touching a mapped
path.
```

- [ ] **Step 2: Verify the skill is discoverable**

Start a fresh session (or run `/context`) and confirm `docs-sync` appears in the available skills list. User-level skills load regardless of working directory, so it must be available inside worktrees too.

Expected: `docs-sync` listed. If absent, check the frontmatter parses — `name` and `description` are both required.

- [ ] **Step 3: No commit**

`~/.claude/` is outside both repositories and is deliberately not version-controlled. Nothing to commit.

---

### Task 9: The `PostToolUse` hook

**Files:**
- Create: `~/.claude/hooks/rookery-docs-sync-gate`
- Modify: `~/.claude/settings.json` (add a `PostToolUse` entry alongside the existing `PreToolUse` and `SessionStart` blocks)

**Interfaces:**
- Consumes: nothing.
- Produces: a hook emitting `hookSpecificOutput.additionalContext` on trigger-path edits.

The hook is best-effort and its silence proves nothing: an edit made through `Bash` (a `sed` invocation, a Python one-liner) never matches an `Edit|Write` matcher. The gate is Task 7; this is a cheap nudge.

- [ ] **Step 1: Verify the stdin contract empirically before writing logic**

The exact `PostToolUse` payload shape must be observed, not assumed. Create a temporary logging hook:

```bash
cat > ~/.claude/hooks/rookery-docs-sync-gate <<'EOF'
#!/bin/bash
cat > /tmp/posttooluse-sample.json
exit 0
EOF
chmod +x ~/.claude/hooks/rookery-docs-sync-gate
```

Register it in `~/.claude/settings.json` inside the existing `"hooks"` object:

```json
"PostToolUse": [
  {
    "matcher": "Edit|Write",
    "hooks": [
      { "type": "command", "command": "~/.claude/hooks/rookery-docs-sync-gate", "timeout": 5 }
    ]
  }
]
```

- [ ] **Step 2: Trigger it and read the payload**

In a new session, edit any file, then:

```bash
python3 -m json.tool /tmp/posttooluse-sample.json
```

Expected: an object containing `tool_name` and `tool_input`. Record the exact path to the edited file — `.tool_input.file_path` is the expected location. **Use the observed key in Step 3**, not the assumed one.

- [ ] **Step 3: Write the real hook**

```bash
#!/bin/bash
# PostToolUse: remind that a documentation-relevant path changed.
#
# Best-effort only. Edits made through Bash (sed, a python one-liner) never
# reach an Edit|Write matcher, so silence from this hook does NOT mean no
# trigger path was touched. The gate is `make docs-sync-check`.
#
# Silent on every failure path, like cbm-code-discovery-gate: a hook that
# errors noisily during unrelated work gets disabled.
set -u
payload="$(cat)" || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

file="$(printf '%s' "$payload" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
print(d.get("tool_input", {}).get("file_path", ""))
' 2>/dev/null)" || exit 0

case "$file" in
  */rookery/*) ;;
  *) exit 0 ;;
esac

pages=""
case "$file" in
  *internal/connectors/providers/*|*internal/connectors/connectors/*)
    pages="reference/connected-services.md, the service count in index.astro, LogoWall.astro, and a logo SVG" ;;
  *internal/skilllibrary/skills/*)
    pages="concepts/skills.md" ;;
  *internal/gateway/*)
    pages="concepts/notifications.md (only if a chat adapter changed)" ;;
  *internal/backup/*)
    pages="concepts/backup-and-restore.md (only if a Destination changed)" ;;
  *cmd/rookery/*)
    pages="reference/cli.md (only if a subcommand changed)" ;;
  *.goreleaser.yaml|*packaging/*|*Dockerfile)
    pages="installation/*.md" ;;
  *) exit 0 ;;
esac

printf '%s' "$(python3 -c '
import json, sys
print(json.dumps({"hookSpecificOutput": {
    "hookEventName": "PostToolUse",
    "additionalContext": "docs-sync: this path is documentation-relevant. Before finishing, check " + sys.argv[1] + " in rookery-web, plus README.md and CLAUDE.md. Use the docs-sync skill.",
}}))
' "$pages" 2>/dev/null)"
exit 0
```

- [ ] **Step 4: Test the trigger path fires**

```bash
echo '{"tool_name":"Edit","tool_input":{"file_path":"/home/rookie/rookery/internal/connectors/providers/gmail.yaml"}}' \
  | ~/.claude/hooks/rookery-docs-sync-gate
```

Expected: JSON containing `connected-services.md`.

- [ ] **Step 5: Test that a non-trigger path stays silent**

```bash
echo '{"tool_name":"Edit","tool_input":{"file_path":"/home/rookie/rookery/internal/db/models.go"}}' \
  | ~/.claude/hooks/rookery-docs-sync-gate; echo "exit=$?"
```

Expected: no output, `exit=0`.

- [ ] **Step 6: Test that malformed input stays silent**

```bash
echo 'not json' | ~/.claude/hooks/rookery-docs-sync-gate; echo "exit=$?"
printf '' | ~/.claude/hooks/rookery-docs-sync-gate; echo "exit=$?"
```

Expected: no output, `exit=0` both times. A hook that errors during unrelated work gets disabled.

- [ ] **Step 7: Confirm the reminder reaches the model**

In a new session, edit a provider YAML and confirm the reminder appears in context. If it does not, the `additionalContext` shape is wrong for `PostToolUse` — re-check against the payload recorded in Step 2.

- [ ] **Step 8: No commit**

`~/.claude/` is outside both repositories.

---

### Task 10: `CLAUDE.md` pointer section

**Files:**
- Modify: `CLAUDE.md` (add a section immediately after the "Git workflow" section)

Keep it short. `CLAUDE.md` is already dense, and a long new section is a skimmed section. Its placement is what makes it work: `using-superpowers` states that `CLAUDE.md` takes precedence over skills, so a rule here outranks the plugin's workflow skills without editing the version-pinned plugin.

- [ ] **Step 1: Add the section**

```markdown
## Documentation sync

Four surfaces describe this project and each can be wrong without anything
failing: `README.md`, `CLAUDE.md`, the documentation site and the landing page
(both in `ilijad1/rookery-web`, checked out at `~/rookery-web`).

**Before opening a pull request, use the `docs-sync` skill.** It holds the
change-to-page trigger map and the cross-repository procedure. A change that
alters a connector provider, a `ROOKERY_*` variable, a CLI subcommand, a core
skill, a chat adapter, a backup destination, an `/api/v1` route or a packaging
target has a documentation obligation in both repositories.

`make docs-sync-check` asserts the checkable half — counts, variable names,
command names, provider names, logo coverage — against the source rather than
against other prose. It runs in `make ci` and skips website assertions when
`~/rookery-web` is absent. It does **not** check whether a paragraph describes
a feature correctly.

Verify every claim against source, never against another document. `README.md`
understated the connector count by half for months because it was copied
forward instead of measured.
```

- [ ] **Step 2: Verify the check still passes**

The new section names `rookery-web` and mentions commands; confirm it does not trip the CLI assertion.

```bash
make docs-sync-check
```

Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: point at the docs-sync skill before every pull request"
```

---

### Task 11: `reference/api.md` on the website

**Files:**
- Create (website): `src/content/docs/docs/reference/api.md`
- Modify (website): `astro.config.mjs` (the Reference sidebar group)

**Interfaces:**
- Consumes: the `want` table in `web/api_parity_test.go` — already the authoritative route inventory and already a merge gate, so it cannot silently fall behind the server.
- Produces: a documented `/api/v1` reference reachable from the sidebar.

The page ships with an explicit caveat that the API is the SPA's own backend and may change between releases. Publishing a route list implies a stability promise; the caveat is what withholds it. **Without the caveat this page should not be published at all.**

- [ ] **Step 1: Extract the route inventory**

```bash
grep -nE '"(GET|POST|PUT|DELETE|PATCH) /api/v1' web/api_parity_test.go | head -60
```

Record every route with its method. Group them by the route families already named in `CLAUDE.md`: auth, workspaces and admin, agents and design, skills, secrets, connectors, services, chats, reminders and inbox, kb, settings and setup, search, backup.

- [ ] **Step 2: Write the page in the website worktree**

Create `/tmp/rookery-web-sync/src/content/docs/docs/reference/api.md`:

```markdown
---
title: HTTP API
description: The /api/v1 routes the web interface uses, and how to reach them from a script.
icon: cli
---

:::caution[Not a stable interface]
This API exists to serve Rookery's own web interface. It is documented because
self-hosting means you own the whole system and may want to script against it —
not because it is a supported integration surface. **Routes and payloads can
change in any release, without a major version bump.** Pin nothing to it that
you are not prepared to fix.
:::

Every route is prefixed `/api/v1` and returns JSON.

## Authentication

The API uses the same session cookie as the web interface. Sign in first, keep
the cookie, and send it with each request.

Two levels of access apply. Owner-scoped routes need a signed-in owner.
Workspace-scoped routes additionally need a workspace to have been entered with
its master password — without one they return `403` with `no_workspace`.

## Routes

<!-- One table per family, filled in from Step 1. Method, path, one line on
     what it does, and whether it is owner- or workspace-scoped. -->
```

Fill the route tables from Step 1. Every route recorded there must appear.

- [ ] **Step 3: Add the sidebar entry**

In `/tmp/rookery-web-sync/astro.config.mjs`, in the Reference group, after the `cli` entry:

```js
{ label: "HTTP API", slug: "docs/reference/api", attrs: { "data-icon": "cli" } },
```

Navigation is hand-maintained; a page without an entry is unreachable.

- [ ] **Step 4: Build the site to verify the page renders and the link resolves**

```bash
cd /tmp/rookery-web-sync && npm ci && npm run build
```

Expected: build succeeds with no broken-link or missing-slug errors. A slug that does not match the file path fails the build.

- [ ] **Step 5: Verify no placeholder survived**

```bash
grep -n 'Filled in from\|<!-- One table' /tmp/rookery-web-sync/src/content/docs/docs/reference/api.md
```

Expected: no output. The comment is scaffolding and must not ship.

- [ ] **Step 6: Commit and open both pull requests**

```bash
git -C /tmp/rookery-web-sync add -A
git -C /tmp/rookery-web-sync commit -m "docs: add the HTTP API reference"
git -C /tmp/rookery-web-sync push -u origin docs/sync-counts
gh pr create --repo ilijad1/rookery-web --base main --head docs/sync-counts \
  --title "docs: correct the service count and add the API reference" \
  --body "Corrects the landing page's 100+ services claim against a real 91, covers every connector provider by name, and adds the HTTP API reference.

Paired with the product pull request that adds \`make docs-sync-check\`."
```

Then push the product branch and open its pull request, linking to the website one.

- [ ] **Step 7: Clean up the worktree once merged**

```bash
git -C ~/rookery-web worktree remove /tmp/rookery-web-sync
```

---

## Self-review

**Spec coverage.** Layer 1 (skill) → Task 8. Layer 2 (hook) → Task 9. Layer 3 (checker) → Tasks 1–7. Layer 4 (`CLAUDE.md` pointer) → Task 10. Seven assertions shipped: claims (2), inflated (3), env (4), CLI (5), provider names and logos (6), plus `check_readme_env_table` — added during implementation, not planned here (see "What actually shipped beyond this plan"). Trigger map → Task 8's skill body. Workflow → Task 8. Release sweep → Task 8's final section. `reference/api.md` → Task 11. Reconciliation → Tasks 2, 3, 5, 6, with the ordering constraint enforced by Task 7 Step 1. The logo difference needed no reconciliation and Task 6 asserts coverage in one direction only, as the spec requires.

**Placeholder scan.** The one scaffolding comment, in Task 11 Step 2, is deliberate — the route list is machine-extracted in Step 1 and cannot be written before that runs — and Step 5 fails the task if it survives.

**Type consistency.** `register`/`fail`/`skip`/`read`/`product_root`/`web_root` are defined in Task 1 and used unchanged thereafter. `providers()` returns a set of slugs and is consumed as such by `derived()`, `check_provider_names()` and `check_logos()`. `derived()` keys (`providers`, `actions`, `skills`) match every `CLAIMS` and `INFLATABLE` entry. Each assertion attaches its selftest as `fn.selftest`, which is what Task 1's `selftest()` looks for.

---

## What actually shipped beyond this plan

Hardening found during code review, with no other in-repo record (only a
gitignored ledger notes it):

- **The CLI scan is restricted to code contexts.** `check_cli` only looks
  inside backtick spans and fenced code blocks for `rookery <word>`, so prose
  mentioning a command in passing can't false-fire the assertion.
- **The removed-provider exemption is name-aware.** A sentence narrating one
  provider's removal no longer exempts a stale mention of a *different*
  removed provider elsewhere on the page — the earlier version matched on
  "does this sentence narrate a removal" without checking which name.
- **`_flex_ws` makes claim patterns whitespace-flexible**, so a prose re-wrap
  (a claim's surrounding sentence reflowing onto a different line boundary)
  doesn't break a `CLAIMS` regex that was written against one specific
  wrapping.
- **`check_logos` has its own selftest**, plus a guard that fails the build if
  any assertion is registered without a selftest attached — closing the
  defect class where an unpinned assertion silently never runs.
- **`check_readme_env_table`** — a seventh assertion not present in this plan
  at all, added after `README.md`'s configuration table shipped with 8 rows
  where 9 were needed (missing `ROOKERY_CLAUDE_BIN`), which no count-based
  check caught because it checks documented names against source, not a
  specific table's completeness. See the design doc for the full description.
