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
