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


def _claims_selftest() -> None:
    text = "we reach 45 external services today"
    m = re.search(r"reach (\d+) external services", text)
    assert m and int(m.group(1)) == 45, "claim regex must capture the number"
    assert re.search(r"reach (\d+) external services", "reach ninety-one") is None, \
        "claim regex must not match prose numbers"


check_claims.selftest = _claims_selftest


# An exact-number regex cannot catch "100+ services" against a real 91: the
# claim is approximate, and false in the direction that matters.
#
# The noun after "N+" varies by sentence ("100+ services", "the 100+
# supported") — a pattern that only recognizes "services" leaves other
# phrasings of the same false claim undetected while the check reports
# green. But the noun list must stay SHORT and each entry demonstrably
# needed: a noun only belongs here if it can only mean "connector services
# here" in context. "connections"/"integrations"/"providers" are ordinary
# English words with unrelated senses ("load-tested with 1000+ connections
# open simultaneously" is a true, unrelated claim) — including them turns a
# targeted check into one that fires on honest prose, and a gate that fires
# on true sentences is a gate that gets disabled. Widen this list only when
# real text in one of the INFLATABLE files needs the new noun, and say
# which line justified it (grep first).
INFLATED_NOUNS = r"(?:services|supported)"
INFLATABLE = [
    ("src/pages/index.astro", rf"(\d+)\+\s*({INFLATED_NOUNS})", "providers"),
    ("src/content/docs/docs/reference/connected-services.md", rf"(\d+)\+\s*({INFLATED_NOUNS})", "providers"),
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
            claimed, actual, noun = int(m.group(1)), values[key], m.group(2)
            if claimed > actual:
                line = text[: m.start()].count("\n") + 1
                fail(
                    "inflated",
                    f"{rel}:{line} claims {claimed}+ {noun}, but there are only {actual}",
                )


def _inflated_selftest() -> None:
    pattern = rf"(\d+)\+\s*({INFLATED_NOUNS})"
    m = re.search(pattern, "100+ services")
    assert m and int(m.group(1)) == 100, "must capture an N+ claim"
    assert 100 > 91, "an N+ claim above the real count is a failure"
    assert re.search(pattern, "91 services") is None, \
        "an exact claim is the claims table's job, not this one"
    # The noun varies by sentence — "A selection of the 100+ supported."
    # (index.astro:395) is the same false claim, worded differently, and
    # must be caught too, not just the "N+ services" spelling.
    m2 = re.search(pattern, "A selection of the 100+ supported.")
    assert m2 and int(m2.group(1)) == 100, \
        "must also catch 'N+ supported', not just 'N+ services'"
    # False-positive guard: "connections"/"integrations" are ordinary words
    # with unrelated senses, so they must NOT be in INFLATED_NOUNS — this is
    # a real sentence that is true and must not fire.
    assert re.search(pattern, "load-tested with 1000+ connections open simultaneously") is None, \
        "must not fire on unrelated uses of 'connections' — that word must stay out of INFLATED_NOUNS"


check_inflated.selftest = _inflated_selftest


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


# Provider slugs whose display name cannot be derived from the filename. Most
# entries here exist because reference/connected-services.md groups the
# Google family under one "## Google" heading and names each product without
# repeating "Google" per item (e.g. "Calendar, Drive, Sheets, Docs, Tasks,
# Analytics, Ads, AdSense" rather than "Google Calendar, Google Drive, ...") —
# the page covers each product, just under a shorter name than title-casing
# the slug would produce, which is a checker limitation, not a docs gap.
DISPLAY_NAMES = {
    "google_searchconsole": "Search Console",
    "firefly_iii": "Firefly III",
    "home_assistant": "Home Assistant",
    "hackernews": "Hacker News",
    "google_ads": "Ads",
    "google_adsense": "AdSense",
    "google_analytics": "Analytics",
    "google_calendar": "Calendar",
    "google_docs": "Docs",
    "google_drive": "Drive",
    "google_sheets": "Sheets",
    "google_tasks": "Tasks",
    "lastfm": "Last.fm",
    "open_meteo": "Open-Meteo",
    "openfoodfacts": "Open Food Facts",
    "openlibrary": "Open Library",
}

# Names that were once providers and must never reappear in prose as though
# they still are. Removing a provider means removing every mention of it.
REMOVED_PROVIDERS = {"Zoom", "Fitbit"}

# A removed-provider name surviving in prose that is deliberately narrating
# the removal itself (not claiming the provider still exists) is not a
# violation — e.g. "Zoom was pulled after its connect flow could not be
# completed". Exempt only a match whose own PARAGRAPH (the blank-line-
# delimited block it sits in — CLAUDE.md's prose wraps one thought across
# several physical lines, so a single-line check would miss a removal verb
# that lands one line above or below the name) also carries an explicit
# removal verb. This is deliberately narrow: a paragraph that merely lists
# the name (a current-provider enumeration) has no such verb anywhere in it
# and still fails, which is what catches CLAUDE.md's stale Zoom listing.
REMOVAL_CONTEXT = re.compile(r"\b(removed|removal|deleted|pulled|decommissioned)\b", re.I)


def _paragraph_spans(text: str) -> list[tuple[int, int]]:
    """Paragraph boundaries: a blank line, AND a markdown table row (starts
    with '|'). A table row needs its own rule because CLAUDE.md's Key
    packages table has no blank lines between rows — without this, the whole
    ~15 KB table collapses into one blank-line-delimited "paragraph", and a
    removal verb anywhere else in that table (there are several, describing
    unrelated features) would exempt every provider name in it, including a
    genuinely stale current-provider mention."""
    spans: list[tuple[int, int]] = []
    para_start = 0
    pos = 0
    for line in text.splitlines(keepends=True):
        line_start = pos
        pos += len(line)
        stripped = line.strip()
        if stripped == "" or stripped.startswith("|"):
            if line_start > para_start:
                spans.append((para_start, line_start))
            if stripped.startswith("|"):
                spans.append((line_start, pos))
            para_start = pos
    if para_start < len(text):
        spans.append((para_start, len(text)))
    return spans


def _paragraph_at(text: str, offset: int) -> str:
    for start, end in _paragraph_spans(text):
        if start <= offset < end:
            return text[start:end]
    return ""


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
                if REMOVAL_CONTEXT.search(_paragraph_at(text, m.start())):
                    continue
                line_no = text[: m.start()].count("\n") + 1
                fail("providers", f"{label}:{line_no} names '{gone}', which is no longer a provider")


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
    assert display_name("clickup") == "Clickup", "slug should title-case"
    assert display_name("firefly_iii") == "Firefly III", "override should win"
    assert display_name("hackernews") == "Hacker News", "override should win"
    slugs, logos = {"gmail", "notion"}, {"gmail", "claude", "cursor"}
    assert slugs - logos == {"notion"}, "must flag a provider with no logo"
    assert not (logos - slugs) & slugs, "extra website logos must not be flagged"
    # A removed-provider mention narrating its own removal must not fire, even
    # when the removal verb lands on a different physical line of the same
    # paragraph (CLAUDE.md wraps prose across lines).
    doc = (
        "Intro paragraph, unrelated.\n\n"
        "Zoom was a provider. Its connect flow could not be\n"
        "completed against a real account, so it was pulled.\n\n"
        "Dropbox, Zoom, Calendly, Asana are current providers.\n"
    )
    m_history = list(re.finditer(r"\bZoom\b", doc))[0]
    m_current = list(re.finditer(r"\bZoom\b", doc))[1]
    assert REMOVAL_CONTEXT.search(_paragraph_at(doc, m_history.start())), \
        "a removal verb elsewhere in the same paragraph must exempt the mention"
    assert not REMOVAL_CONTEXT.search(_paragraph_at(doc, m_current.start())), \
        "a plain current-provider list in its own paragraph must not be exempted"
    # A table row must not merge with unrelated rows (no blank lines between
    # them) just because some other row far away happens to say "removed".
    table = (
        "| Package | Notes |\n"
        "|---|---|\n"
        "| `internal/foo` | some other feature that was removed long ago |\n"
        "| `internal/connectors` | 91 providers (..., Zoom, Calendly, ...) |\n"
    )
    m_table = list(re.finditer(r"\bZoom\b", table))[0]
    assert not REMOVAL_CONTEXT.search(_paragraph_at(table, m_table.start())), \
        "a table row must be its own paragraph, not merged with a distant row"


check_provider_names.selftest = _provider_selftest


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
