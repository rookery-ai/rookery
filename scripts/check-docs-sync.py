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
