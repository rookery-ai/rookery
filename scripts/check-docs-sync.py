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


def _flex_ws(pattern: str) -> str:
    """Make every literal space in a hand-written regex pattern
    whitespace-flexible (`\\s+`), so a claim survives an ordinary prose
    re-wrap that turns an inter-word space into a newline.

    Applied at MATCH TIME ONLY — the searched TEXT is never touched. A
    confirmed peer finding: 2 of 5 CLAIMS/INFLATABLE patterns matched nothing
    against a real 80-column re-wrap of the README, purely because the
    pattern's literal space could no longer see across the new line break.
    That is the gate-killing failure mode (a correct document going red), not
    a silent miss, but it is exactly the kind of false positive that trains a
    maintainer to weaken the assertion instead of fixing the regex.

    Normalising the TEXT instead (collapsing its whitespace before matching)
    was considered and rejected: `fail()` computes `file:line` from the
    match offset, so matching against a whitespace-collapsed copy of the
    file would report a line number that does not correspond to the real
    file and send a maintainer to the wrong line. Only the pattern moves.

    Safe for every pattern in CLAIMS/INFLATABLE: none of them use a literal
    space to mean anything other than "one or more characters of inter-word
    whitespace here" — no character classes, no quoted-space literals — and
    a plain `str.replace` only touches the space character, leaving `(\\d+)`,
    escaped markdown (`\\*\\*Connectors\\*\\*`), the em-dash and the comma
    untouched.
    """
    return pattern.replace(" ", r"\s+")


# (repo, relative path, regex with exactly one capture group, derived key)
# The regex is matched against the WHOLE file including YAML frontmatter: the
# skills claim lives in the `description:` field of concepts/skills.md, not in
# its prose, and a body-only scan would match nothing and pass silently.
CLAIMS = [
    # README.md used to state the provider count in three separate sentences and
    # the skill count in a fourth, because it carried a prose section per
    # feature. Those sections are now one generated image, so each number has
    # exactly ONE prose home left — the line under docs/assets/features.svg.
    # The counts INSIDE that image are guarded by check_readme_assets instead:
    # SVG splits text across elements, so no grep over README.md can see them.
    ("product", "README.md", r"\*\*(\d+) providers\*\* and", "providers"),
    ("product", "README.md", r"and \*\*(\d+) curated actions\*\*", "actions"),
    ("product", "README.md", r"\*\*(\d+) skills\*\* built in", "skills"),
    ("web", "src/pages/index.astro", r"(\d+)\+? services", "providers"),
    # The landing page states the provider count TWICE, in different words:
    # the section heading ("N services") and the line under the logo wall
    # ("A selection of the N supported"). Only the first was pinned, so the
    # second sat at 91 through five connector waves while the heading above it
    # read 126 — two contradictory numbers on one screen. check_inflated does
    # not cover it either: that check is scoped to INFLATED "N+" claims and
    # deliberately leaves exact ones to this table.
    ("web", "src/pages/index.astro", r"selection of the (\d+) supported", "providers"),
    ("web", "src/content/docs/docs/concepts/skills.md", r"— (\d+) built in", "skills"),
    # CLAUDE.md is the surface an agent reads first, and where the original
    # errors (a stale Zoom listing, a nonexistent `db migrate` command) were
    # actually found — it had no pinned claims at all until now. Two distinct
    # "N providers" sites exist (the internal/connectors table row and the
    # connector-service-layer prose); their surrounding words differ enough
    # to anchor each separately, so both are pinned rather than picking one.
    ("product", "CLAUDE.md", r"for \*\*(\d+) providers\*\* \(Google-family", "providers"),
    ("product", "CLAUDE.md", r"\*\*(\d+) providers \(~\d+ actions\)", "providers"),
    ("product", "CLAUDE.md", r"\(~(\d+) actions\)", "actions"),
    ("product", "CLAUDE.md", r"(\d+) bundled skills", "skills"),
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
        m = re.search(_flex_ws(pattern), text)
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

    # The bug this change fixes: a literal-space pattern matches nothing once
    # prose re-wraps a space into a newline. Proving the UN-wrapped form still
    # matches (above) says nothing about that bug — every real CLAIMS pattern
    # must be exercised against a deliberately re-wrapped copy of text it is
    # supposed to match, through _flex_ws, the same way check_claims calls it.
    rewrapped_cases = [
        (r"\*\*(\d+) providers\*\* and", "**91\nproviders** and", "91"),
        (r"and \*\*(\d+) curated actions\*\*", "and **471 curated\nactions**", "471"),
        (r"\*\*(\d+) skills\*\* built in", "**22 skills** built\nin", "22"),
        (r"(\d+)\+? services", "91\nservices", "91"),
        (r"selection of the (\d+) supported", "A selection of\nthe 91 supported.", "91"),
        (r"— (\d+) built in", "—\n22 built\nin", "22"),
        (r"for \*\*(\d+) providers\*\* \(Google-family", "for **91\nproviders** (Google-family", "91"),
        (r"\*\*(\d+) providers \(~\d+ actions\)", "**91 providers\n(~471 actions)", "91"),
        (r"\(~(\d+) actions\)", "(~471\nactions)", "471"),
        (r"(\d+) bundled skills", "22 bundled\nskills", "22"),
    ]
    assert len(rewrapped_cases) == len(CLAIMS), \
        "every pattern in CLAIMS must have a re-wrap case here, not a sample of them"
    for pattern, rewrapped, expect in rewrapped_cases:
        # The unfixed (literal-space) pattern must actually fail here — this
        # is the confirmed peer finding (2 of 5 patterns broke on a real
        # 80-column re-wrap) reproduced synthetically, so this test would
        # catch a regression back to literal-space matching.
        assert re.search(pattern, rewrapped) is None, \
            f"fixture is not a real re-wrap test: literal pattern {pattern!r} " \
            f"still matched {rewrapped!r} — sharpen the fixture"
        m = re.search(_flex_ws(pattern), rewrapped)
        assert m and m.group(1) == expect, \
            f"_flex_ws({pattern!r}) must still match its re-wrapped text {rewrapped!r}"


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
        for m in re.finditer(_flex_ws(pattern), text):
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

    # check_inflated runs every pattern through _flex_ws too (see CLAIMS'
    # selftest for the fixed-vs-unfixed proof) — for INFLATABLE it is
    # currently a no-op (the pattern already uses \s* between "+" and the
    # noun, which already tolerates a wrapped newline), but the call must
    # stay wired so a future INFLATABLE pattern with a literal space is
    # covered by the same fix rather than silently exempt.
    flexed = _flex_ws(pattern)
    m3 = re.search(flexed, "100+\nservices")
    assert m3 and int(m3.group(1)) == 100, \
        "_flex_ws(pattern) must still catch an N+ claim that wraps before the noun"


check_inflated.selftest = _inflated_selftest


def env_vars() -> set[str]:
    # --include/--exclude restrict this to production Go source: a *_test.go
    # file scanned alongside it could name a test-only ROOKERY_* variable no
    # operator can ever set, which check_env / check_readme_env_table would
    # then both demand documentation for — a false positive on correct docs.
    # `--exclude` matches the BASENAME only (not the full path), which is
    # exactly what's needed here: a path-bearing exclude pattern would
    # silently match nothing and exclude no file at all.
    out = subprocess.run(
        ["grep", "-rhoE", "--include=*.go", "--exclude=*_test.go",
         r'"ROOKERY_[A-Z_]+"', "internal", "cmd", "web"],
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


def _readme_table_names_from_text(text: str) -> set[str]:
    """ROOKERY_* variable names that appear as a row in a markdown table —
    i.e. a line of the form `| `ROOKERY_X` | ... | ... |` — as opposed to
    any other mention of the name in the same document.

    README.md mentions several ROOKERY_ vars OUTSIDE the configuration
    table too (a `ROOKERY_CODER_MODE=slim` aside, a `ROOKERY_PUBLIC_URL`
    callout paragraph) — check_env's whole-file scan is fine for the
    website's configuration.md, which is essentially just the table plus
    per-variable expansions of rows already in it, but reusing that same
    whole-file approach here would let a variable satisfy "documented" by a
    passing prose mention while never actually getting a table ROW, which is
    the specific gap check_readme_env_table exists to close.
    """
    return set(re.findall(r"^\|\s*`(ROOKERY_[A-Z_]+)`\s*\|", text, re.M))


def readme_env_table_names() -> set[str]:
    return _readme_table_names_from_text(read(product_root() / "README.md"))


@register
def check_readme_env_table() -> None:
    """README.md's configuration table must list exactly the public
    ROOKERY_* variables — the same `env_vars() - internal_env()` set
    check_env already computes for the website, reused rather than
    re-derived. Runs unconditionally (no `web_root() is None` guard): this
    reads only product files, so it must still catch a stale README even
    when rookery-web isn't checked out alongside this repo.
    """
    documented = readme_env_table_names()
    expected = env_vars() - internal_env()
    missing = expected - documented
    for name in sorted(missing):
        fail("readme-env", f"{name} is a public variable but has no row in README.md's configuration table")
    extra = documented - expected
    for name in sorted(extra):
        fail("readme-env", f"README.md's configuration table lists {name}, which is not a public variable read by the source")


def _readme_env_table_selftest() -> None:
    # Red case #1 (this is the point of the change — pin the failing case,
    # not just the passing one): a public variable whose table row was
    # removed must be caught. This is literally what today's real README
    # looked like before the ROOKERY_CLAUDE_BIN row was added — the exact
    # gap this assertion was written to close.
    text_row_removed = (
        "| Variable | Default | What it does |\n"
        "|---|---|---|\n"
        "| `ROOKERY_HOST` | `0.0.0.0` | bind address |\n"
    )
    names = _readme_table_names_from_text(text_row_removed)
    expected = {"ROOKERY_HOST", "ROOKERY_CLAUDE_BIN"}
    missing = expected - names
    assert missing == {"ROOKERY_CLAUDE_BIN"}, \
        "a public variable missing its table row must be caught"

    # Red case #2, the reverse direction: a table row naming a variable the
    # source does not read at all.
    text_extra_row = (
        "| Variable | Default | What it does |\n"
        "|---|---|---|\n"
        "| `ROOKERY_HOST` | `0.0.0.0` | bind address |\n"
        "| `ROOKERY_GHOST` | — | not read by any source file |\n"
    )
    names2 = _readme_table_names_from_text(text_extra_row)
    extra = names2 - expected
    assert extra == {"ROOKERY_GHOST"}, \
        "a table row naming a variable the source does not read must be caught"

    # Green case: once the row exists, both directions are silent.
    text_complete = (
        "| Variable | Default | What it does |\n"
        "|---|---|---|\n"
        "| `ROOKERY_HOST` | `0.0.0.0` | bind address |\n"
        "| `ROOKERY_CLAUDE_BIN` | detected | override the path to a coder binary |\n"
    )
    names3 = _readme_table_names_from_text(text_complete)
    assert expected - names3 == set() and names3 - expected == set(), \
        "once every public variable has a row and nothing extra is listed, nothing should be flagged"

    # A ROOKERY_ mention outside the table (no leading `|`) must not count as
    # documentation — this is the whole reason check_env's whole-file scan
    # was not reused as-is for the README.
    prose_only = "See `ROOKERY_CLAUDE_BIN` above for details on overriding the coder binary.\n"
    assert _readme_table_names_from_text(prose_only) == set(), \
        "a prose mention outside the table must not be treated as a documented row"


check_readme_env_table.selftest = _readme_env_table_selftest


GEN_ASSETS = "scripts/gen-readme-assets.py"


@register
def check_readme_assets() -> None:
    """The README's three images are generated; a stale commit must fail here.

    Two of the twelve feature cards and the architecture diagram's outward band
    state a COUNT. check_claims cannot reach them: a number in an SVG is split
    across elements and attributes, and lives outside README.md entirely, so
    every grep-shaped guard the repo has is blind to it. The durable answer is
    to re-render from source and compare bytes — the same shape as the SPA's
    emojiData test, where the generator is the source and the committed file is
    a build artifact CI proves current.

    This matters more than it looks: the provider count in README.md drifted for
    months precisely because it was copied forward rather than measured, and
    moving it into an image would have recreated that hiding place.
    """
    script = product_root() / GEN_ASSETS
    if not script.exists():
        fail("readme-assets", f"{GEN_ASSETS} is missing — the images have no source")
        return
    proc = subprocess.run(
        [sys.executable, str(script), "--check"],
        capture_output=True, text=True, cwd=product_root(),
    )
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout).strip() or "generator exited non-zero"
        fail("readme-assets", detail.replace("\n", " "))


def _readme_assets_selftest() -> None:
    # Red case: the generator must actually FAIL on a stale file, not merely
    # succeed on a fresh one. Proven against a real render rather than a
    # fixture, because the thing being asserted IS "the committed bytes equal a
    # fresh render" — a mock of that is a mock of the whole assertion. The
    # tampered copy is written to a scratch tree so the real docs/assets is
    # never touched, even if this raises part-way through.
    import shutil
    import tempfile

    root = product_root()
    src = root / "docs" / "assets" / "features.svg"
    assert src.exists(), "docs/assets/features.svg must exist for this selftest"

    with tempfile.TemporaryDirectory() as tmp:
        backup = Path(tmp) / "features.svg"
        shutil.copy2(src, backup)
        original = src.read_bytes()
        try:
            src.write_bytes(original.replace(b"Workspaces", b"Workspacez"))
            proc = subprocess.run(
                [sys.executable, str(root / GEN_ASSETS), "--check"],
                capture_output=True, text=True, cwd=root,
            )
            assert proc.returncode != 0, \
                "a tampered docs/assets file must make --check exit non-zero"
            assert "features.svg" in (proc.stderr + proc.stdout), \
                "--check must name the file that went stale"
        finally:
            shutil.copy2(backup, src)

    # Green case: restored, the same command is silent again. Without this the
    # red case could pass simply because --check is broken and always fails.
    proc = subprocess.run(
        [sys.executable, str(root / GEN_ASSETS), "--check"],
        capture_output=True, text=True, cwd=root,
    )
    assert proc.returncode == 0, \
        f"--check must pass against the committed assets, got: {proc.stderr.strip()}"


check_readme_assets.selftest = _readme_assets_selftest


def declared_cli_names() -> set[str]:
    """Every Name: string in cmd/rookery — commands and flags alike.

    Deliberately does not distinguish the two. See the plan: reconstructing the
    command tree from source is unreliable, and a flag name in the set only ever
    makes this check more permissive, never wrong in the failing direction.
    """
    d = product_root() / "cmd" / "rookery"
    blob = "\n".join(read(p) for p in sorted(d.glob("*.go")))
    return set(re.findall(r'Name:\s*"([^"]+)"', blob))


CLI_INVOCATION_RE = re.compile(r"rookery ([a-z][a-z-]+)\b")


def _code_spans(text: str) -> list[tuple[int, int]]:
    """Offset spans of markdown CODE CONTEXT: fenced ``` blocks and inline
    `single-backtick` spans. A command-invocation check must only look where
    a reader would recognize a command — plain prose describing the product
    ("rookery reads your vault") must be structurally incapable of matching.
    Fenced spans are found first and masked out of a working copy before the
    inline-span search runs, so a fence's own triple backticks can never be
    mistaken for a pair of inline spans."""
    spans: list[tuple[int, int]] = []
    masked = list(text)
    for m in re.finditer(r"```.*?```", text, re.S):
        spans.append((m.start(), m.end()))
        for i in range(m.start(), m.end()):
            masked[i] = " "
    for m in re.finditer(r"`[^`\n]+`", "".join(masked)):
        spans.append((m.start(), m.end()))
    spans.sort()
    return spans


def _undeclared_cli_invocations(text: str, declared: set[str]) -> list[tuple[int, str]]:
    """(offset, word) for every `rookery <word>` invocation found in a CODE
    context of `text` whose word is not in `declared`. Scanning is restricted
    to `_code_spans` on purpose — see its docstring."""
    out: list[tuple[int, str]] = []
    for start, end in _code_spans(text):
        for m in CLI_INVOCATION_RE.finditer(text[start:end]):
            word = m.group(1)
            if word not in declared:
                out.append((start + m.start(), word))
    return out


@register
def check_cli() -> None:
    declared = declared_cli_names()
    web = web_root()
    if web is not None:
        path = web / "src/content/docs/docs/reference/cli.md"
        for m in re.finditer(r"^## (\S+)", read(path), re.M):
            if m.group(1) not in declared:
                fail("cli", f"reference/cli.md documents '{m.group(1)}', which no source file declares")
    # Product docs invoke commands inline, inside code context only (fenced
    # blocks or backtick spans) — never in prose. This is exactly the shape
    # of how CLAUDE.md came to document `rookery db migrate`, and it is what
    # keeps ordinary prose ("rookery reads your vault") from ever tripping
    # this check once the README is rewritten in full sentences.
    for rel in ("CLAUDE.md", "README.md"):
        text = read(product_root() / rel)
        for offset, word in _undeclared_cli_invocations(text, declared):
            line = text[:offset].count("\n") + 1
            fail("cli", f"{rel}:{line} invokes 'rookery {word}', which no source file declares")


def _cli_selftest() -> None:
    declared = {"serve", "owner", "backup", "dir"}
    documented = ["serve", "owner", "backup", "db"]
    missing = [d for d in documented if d not in declared]
    assert missing == ["db"], "must flag a documented command the source never declares"
    assert "serve" in declared, "a real command must not be flagged"

    # A fenced invocation of a non-existent command must still be caught —
    # this is the exact shape of the real `rookery db migrate` bug this
    # check was built to catch, and the fix to scan code-context-only must
    # not lose it.
    fenced = "Run it:\n\n```bash\nrookery db migrate\n```\n"
    hits = [w for _, w in _undeclared_cli_invocations(fenced, declared)]
    assert hits == ["db"], "a fenced invocation of an undeclared command must still be caught"

    # An inline backtick invocation must also be caught.
    inline = "Use `rookery frobnicate` to do the thing."
    hits = [w for _, w in _undeclared_cli_invocations(inline, declared)]
    assert hits == ["frobnicate"], "an inline-backtick invocation of an undeclared command must be caught"

    # Plain prose carries no backticks at all, so it is structurally
    # incapable of matching — this is the false-positive a README rewrite
    # into full sentences would otherwise trigger.
    prose = "rookery reads your vault and writes notes back to it."
    assert _undeclared_cli_invocations(prose, declared) == [], \
        "prose outside code context must never be caught"


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
    # Two words in the brand, one in the slug — the default .title() would
    # demand the prose say "Huggingface", which is not the name.
    "huggingface": "Hugging Face",
    "assemblyai": "AssemblyAI",
    "flyio": "Fly.io",
    "coingecko": "CoinGecko",
    "alphavantage": "Alpha Vantage",
    "digitalocean": "DigitalOcean",
    "hetzner": "Hetzner Cloud",
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
# completed". The evidence unit is the SENTENCE, not the paragraph: a
# sentence pairing the removal verb with the name is exempt.
#
# Paragraph-wide scope (checking whether ANY sentence in the paragraph has a
# removal verb ANYWHERE, regardless of which name it's about) was tried
# first and is too wide — confirmed false negative:
#     "Fitbit was removed in 2026.\nProviders include Gmail, Zoom and Slack."
# Zoom went unreported because the removal verb sat in an unrelated sentence
# about a DIFFERENT provider, elsewhere in the same paragraph.
#
# The opposite extreme — requiring the CURRENT mention's own single sentence
# to carry the verb — is also too narrow for real prose: CLAUDE.md's actual
# Fitbit-removal paragraph spreads the narrative across several sentences
# ("Fitbit was replaced by `google_health`... Existing Fitbit tokens do not
# carry over... the obvious fix for 'Fitbit is missing' is to re-add the
# YAML..."), and only the first two of four Fitbit-mentioning sentences
# repeat a removal verb — the rest lean on paragraph context, which is
# ordinary, unremarkable writing.
#
# So the actual rule (`_removal_narrated_for`) is NAME-AWARE, scoped to the
# paragraph: a mention is exempt when SOME sentence within its paragraph
# pairs THIS SPECIFIC name with a removal verb — not just any sentence with
# a verb (that was the original bug: name-agnostic), and not only the
# mention's own sentence (too narrow for multi-sentence narratives). This
# still catches the adversarial case above (no sentence anywhere in that
# paragraph pairs "Zoom" with a removal verb) while accepting CLAUDE.md's
# real multi-sentence Fitbit narrative, and it still tolerates a sentence
# that line-wraps across several physical lines (`_sentence_spans` is built
# on `_paragraph_spans`, so a sentence never crosses a blank-line or
# markdown-table-row boundary).
#
# A sentence containing BOTH the removal verb and the name — e.g. "Zoom was
# removed and Fitbit was replaced." — stays exempt for both names, even
# though "replaced" alone isn't a removal verb: the sentence contains
# "removed" AND "Fitbit" together, and word-presence (not grammatical
# binding) is deliberately the whole test. That is correct, not a gap: the
# sentence is genuinely narrating removal history.
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


def _sentence_spans(text: str) -> list[tuple[int, int]]:
    """Sentence boundaries within paragraph spans.

    Built ON TOP OF `_paragraph_spans` rather than independently, for two
    reasons: a markdown-table row must stay its own unit exactly as it does
    for paragraphs (a sentence search must never cross into an unrelated
    table row), and a sentence must never be found to span a blank-line
    paragraph break.

    A sentence ends at '.', '!' or '?' followed by whitespace — which, since
    prose wraps across physical lines, may include a newline — or at the end
    of its paragraph. Splitting on terminator-plus-whitespace means a
    sentence that line-wraps mid-thought ("...could not be\\ncompleted ...
    was pulled.") still counts as ONE sentence: the same line-wrap tolerance
    paragraph scope was originally chosen for, just narrowed to exactly one
    sentence instead of everything between blank lines.
    """
    spans: list[tuple[int, int]] = []
    for pstart, pend in _paragraph_spans(text):
        para = text[pstart:pend]
        sent_start = 0
        for m in re.finditer(r"[.!?](?=\s|$)", para):
            sent_end = m.end()
            spans.append((pstart + sent_start, pstart + sent_end))
            sent_start = sent_end
        if sent_start < len(para):
            spans.append((pstart + sent_start, pend))
    return spans


def _sentence_at(text: str, offset: int) -> str:
    for start, end in _sentence_spans(text):
        if start <= offset < end:
            return text[start:end]
    return ""


def _removal_narrated_for(text: str, name: str, offset: int) -> bool:
    """True when the `name` mention at `offset` is exempt from the
    removed-provider check: some sentence within the mention's own paragraph
    pairs THIS name with an explicit removal verb. See REMOVAL_CONTEXT's
    comment for why this is name-aware-but-paragraph-searched, rather than
    either name-agnostic-paragraph-scope (the original bug) or
    current-sentence-only scope (too narrow for a real multi-sentence
    removal narrative)."""
    para_start = para_end = None
    for pstart, pend in _paragraph_spans(text):
        if pstart <= offset < pend:
            para_start, para_end = pstart, pend
            break
    if para_start is None:
        return False
    name_re = re.compile(rf"\b{re.escape(name)}\b")
    for sstart, send in _sentence_spans(text):
        if sstart < para_start or send > para_end:
            continue
        sentence = text[sstart:send]
        if name_re.search(sentence) and REMOVAL_CONTEXT.search(sentence):
            return True
    return False


def display_name(slug: str) -> str:
    return DISPLAY_NAMES.get(slug, slug.replace("_", " ").title())


def _stale_provider_mentions(text: str, slugs: set[str]) -> list[tuple[int, str]]:
    """(offset, name) for every REMOVED_PROVIDERS display name that still
    appears in `text` as though it were a live provider — i.e. its slug is
    genuinely absent from `slugs` and the mention isn't itself narrating the
    removal (`_removal_narrated_for`).

    The slug comparison is case-INSENSITIVE on purpose: REMOVED_PROVIDERS
    holds display names ("Zoom", "Fitbit") but `providers()` returns
    lowercase filename stems ("zoom", "fitbit") — a case-sensitive
    `gone in slugs` is always False, so the escape hatch meant to stop
    complaining once a provider comes BACK never actually fired. That
    escape hatch matters: without it, re-adding `zoom.yaml` would make this
    check simultaneously demand "Zoom" appear on the services page (forward
    coverage) and fail on every mention of it as "no longer a provider" —
    red on correct documentation, with no way to green it except editing
    the checker.
    """
    lower_slugs = {s.lower() for s in slugs}
    out: list[tuple[int, str]] = []
    for gone in sorted(REMOVED_PROVIDERS):
        if gone.lower() in lower_slugs:
            continue
        for m in re.finditer(rf"\b{gone}\b", text):
            if _removal_narrated_for(text, gone, m.start()):
                continue
            out.append((m.start(), gone))
    return out


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
        for offset, gone in _stale_provider_mentions(text, slugs):
            line_no = text[:offset].count("\n") + 1
            fail("providers", f"{label}:{line_no} names '{gone}', which is no longer a provider")


# Providers with no vendored mark anywhere, exempted on purpose. This mirrors
# allowNoLogo in web/logo_coverage_test.go — the two checkers guard the same
# rendering on two surfaces, so a policy expressed in only one of them is a
# policy that will drift.
#
# The policy is unchanged: vendor the real published logo or show a letter,
# never approximate someone else's brand. Microsoft's product marks were
# REMOVED from simple-icons; worldvectorlogo carries OneDrive, Excel and
# OneNote but not To Do. Reusing the Outlook mark would label it as a different
# product, and Wunderlist — its retired predecessor — is a different brand
# again.
ALLOW_NO_LOGO = {"microsoft_todo"}


def _missing_logos(provider_slugs: set[str], logo_stems: set[str]) -> set[str]:
    """Providers with no logo. Deliberately ONE-DIRECTIONAL: a logo with no
    matching provider (claude.svg, cursor.svg — coder marks shown on the
    landing page, not connector providers) must never be reported. Set
    equality is NOT the contract here."""
    return provider_slugs - logo_stems - ALLOW_NO_LOGO


@register
def check_logos() -> None:
    web = web_root()
    if web is None:
        return
    logos = {p.stem for p in (web / "src" / "assets" / "logos").glob("*.svg")}
    for slug in sorted(_missing_logos(providers(), logos)):
        fail("logos", f"provider '{slug}' has no logo at src/assets/logos/{slug}.svg")


def _logos_selftest() -> None:
    slugs, logos = {"gmail", "notion"}, {"gmail", "claude", "cursor"}
    assert _missing_logos(slugs, logos) == {"notion"}, \
        "a provider with no logo must be flagged"
    assert _missing_logos(slugs, logos | {"notion"}) == set(), \
        "once every provider has a logo, nothing is flagged"
    # One-directional: website-only logos (coder marks with no provider
    # behind them, e.g. claude.svg/cursor.svg on the landing page) must
    # never be reported just because they have no provider counterpart.
    assert _missing_logos({"gmail"}, {"gmail", "claude", "cursor"}) == set(), \
        "extra website-only logos with no matching provider must never be flagged"


check_logos.selftest = _logos_selftest


def _provider_selftest() -> None:
    assert display_name("clickup") == "Clickup", "slug should title-case"
    assert display_name("firefly_iii") == "Firefly III", "override should win"
    assert display_name("hackernews") == "Hacker News", "override should win"

    # The confirmed adversarial false negative under (name-agnostic)
    # paragraph scope: a removal sentence about ONE provider must not exempt
    # a stale current-provider listing of a DIFFERENT provider sharing the
    # same paragraph. Exact string from the confirmed false negative.
    adversarial = "Fitbit was removed in 2026.\nProviders include Gmail, Zoom and Slack."
    m_zoom = list(re.finditer(r"\bZoom\b", adversarial))[0]
    assert not _removal_narrated_for(adversarial, "Zoom", m_zoom.start()), \
        "a removal verb paired with a DIFFERENT name must not exempt a stale listing of this one"

    # A genuine removal narration — name and verb in the SAME sentence —
    # stays exempt even when it line-wraps across several physical lines
    # (prose wraps; that is why sentence scope tolerates wrapping instead of
    # regressing to single-line scope).
    doc = (
        "Intro paragraph, unrelated.\n\n"
        "Zoom was pulled after its connect flow could not be\n"
        "completed against a real account, so it was removed.\n\n"
        "Dropbox, Zoom, Calendly, Asana are current providers.\n"
    )
    m_history = list(re.finditer(r"\bZoom\b", doc))[0]
    m_current = list(re.finditer(r"\bZoom\b", doc))[1]
    assert _removal_narrated_for(doc, "Zoom", m_history.start()), \
        "a removal verb in the SAME sentence (even line-wrapped) must exempt the mention"
    assert not _removal_narrated_for(doc, "Zoom", m_current.start()), \
        "a plain current-provider list in its own paragraph, with no sentence pairing it to a verb, must not be exempted"

    # The case that discriminates name-aware-paragraph-search from strict
    # current-sentence-only scope: real removal narratives (CLAUDE.md's
    # actual Fitbit paragraph is shaped exactly like this) spread the verb
    # and the name across MULTIPLE sentences of one paragraph. A later
    # sentence in the SAME paragraph that re-mentions the name without
    # repeating the verb must still be exempt — that is ordinary writing,
    # not a stale claim.
    spread = (
        "Fitbit was replaced by Google Health and its old API was removed. "
        "Existing Fitbit tokens do not carry over; every user re-consents. "
        "The old Fitbit connector is gone for good.\n"
    )
    m_first = list(re.finditer(r"\bFitbit\b", spread))[0]
    m_later = list(re.finditer(r"\bFitbit\b", spread))[1]
    assert _removal_narrated_for(spread, "Fitbit", m_first.start()), \
        "the sentence that actually pairs the name with the verb must be exempt"
    assert _removal_narrated_for(spread, "Fitbit", m_later.start()), \
        "a later same-paragraph sentence about the SAME name, with no verb of its own, must still be exempt " \
        "— strict current-sentence-only scope would wrongly flag this as a stale claim"

    # But that widening must stay NAME-AWARE: a different name mentioned in
    # the same paragraph, with no sentence anywhere pairing IT to a verb,
    # must still be caught — this is the adversarial case again, restated as
    # a multi-sentence paragraph to make sure paragraph-wide search doesn't
    # quietly regress into the original name-agnostic bug.
    mixed = (
        "Fitbit was replaced by Google Health and its old API was removed. "
        "Zoom, Calendly and Asana are current providers.\n"
    )
    m_zoom2 = list(re.finditer(r"\bZoom\b", mixed))[0]
    assert not _removal_narrated_for(mixed, "Zoom", m_zoom2.start()), \
        "widening the search to the whole paragraph must not exempt an unrelated name — that is the original bug"

    # A table row must not merge with unrelated rows (no blank lines between
    # them) just because some other row far away happens to say "removed".
    table = (
        "| Package | Notes |\n"
        "|---|---|\n"
        "| `internal/foo` | some other feature that was removed long ago |\n"
        "| `internal/connectors` | 91 providers (..., Zoom, Calendly, ...) |\n"
    )
    m_table = list(re.finditer(r"\bZoom\b", table))[0]
    assert not _removal_narrated_for(table, "Zoom", m_table.start()), \
        "a table row must be its own unit, not merged with a distant row"

    # Defect 1: REMOVED_PROVIDERS holds display names ("Zoom") but
    # providers() returns lowercase filename stems ("zoom") — a
    # case-sensitive `gone in slugs` check is always False, so the escape
    # hatch that's supposed to stop complaining once a provider comes BACK
    # never actually fired. Prove the guard now fires: once the provider's
    # slug reappears, a plain (non-narrated) mention must be silently
    # accepted.
    reappeared = "Zoom, Calendly and Asana are current providers.\n"
    assert _stale_provider_mentions(reappeared, {"zoom", "calendly", "asana"}) == [], \
        "once a removed provider's slug reappears, a plain mention of it must not be flagged " \
        "— this is the escape hatch Defect 1 fixed (case-insensitive slug compare)"
    # And with the slug genuinely absent, the identical text must still be
    # flagged — proves the fix didn't just disable the check outright.
    assert [name for _, name in _stale_provider_mentions(reappeared, {"calendly", "asana"})] == ["Zoom"], \
        "when the slug is genuinely absent, a plain mention must still be flagged"


check_provider_names.selftest = _provider_selftest


def selftest() -> int:
    """Run each assertion's inline cases against synthetic input.

    The real repository is not a regression test: once drift is fixed, a
    broken assertion passes silently. These cases keep the detectors honest.

    Every REGISTERED assertion must carry a `.selftest` — an assertion with
    none is silently skipped from the loop below, which is indistinguishable
    from one that ran and passed (this is exactly how `check_logos` went
    unpinned for a full assertion's lifetime). So a missing `.selftest` is
    itself a failure here, named, rather than a silent omission.
    """
    errors = 0
    for fn in ASSERTIONS:
        if not hasattr(fn, "selftest"):
            print(f"selftest FAILED: {fn.__name__}: no .selftest attached — "
                  f"an unpinned assertion is indistinguishable from a passing one")
            errors += 1
            continue
        try:
            fn.selftest()
            print(f"selftest ok: {fn.__name__}")
        except AssertionError as exc:
            print(f"selftest FAILED: {fn.__name__}: {exc}")
            errors += 1
    if not ASSERTIONS:
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
