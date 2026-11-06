#!/usr/bin/env python3
"""Fail on reachable vulnerabilities in `govulncheck -format json` output.

    govulncheck -format json ./... > report.json
    python3 scripts/govulncheck-gate.py report.json
    python3 scripts/govulncheck-gate.py --selftest

This exists because the Go vulnerability database can lag an upstream fix.
`govulncheck` cannot be told to ignore an advisory, so a database entry with no
fixed version fails every pull request in the repository, even after the
dependency has been upgraded to the release that fixes it.

An exemption therefore names a module and the release that fixed the advisory,
and it applies only at that release or later: a downgrade fails again. It is not
for an advisory that has no fix. Upgrade, or leave the gate red.
"""

import json
import re
import sys

# advisory -> (module, first fixed release, where that release is stated).
# Remove an entry once the gate reports it as no longer needed.
EXEMPT = {
    "GO-2026-6872": (
        "github.com/labstack/echo/v4",
        "v4.16.3",
        "Echo's v4.16.3 changelog; the database lists no fixed v4 version",
    ),
    "GO-2026-6875": (
        "github.com/labstack/echo/v4",
        "v4.16.3",
        "Echo's v4.16.3 changelog and GHSA-99jh-6h7p-pp36; the database lists no fixed v4 version",
    ),
}


def parse_version(version):
    """Return a comparable (major, minor, patch), or None for anything else."""
    match = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", version or "")
    return tuple(int(part) for part in match.groups()) if match else None


def read_messages(text):
    """Decode the stream of JSON objects govulncheck writes, one after another."""
    decoder = json.JSONDecoder()
    messages = []
    pos = 0
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos == len(text):
            return messages
        message, pos = decoder.raw_decode(text, pos)
        messages.append(message)


def is_exempt(osv, module, version, exemptions):
    entry = exemptions.get(osv)
    if entry is None or entry[0] != module:
        return False
    have, fixed = parse_version(version), parse_version(entry[1])
    return have is not None and fixed is not None and have >= fixed


def evaluate(messages, exemptions):
    """Return (blocking, exempted), each a sorted list of (osv, module, version).

    A finding counts only when its innermost frame names a function: that is how
    govulncheck says the vulnerable code is actually called, rather than merely
    present in a required module.
    """
    blocking, exempted = set(), set()
    for message in messages:
        finding = message.get("finding")
        if not finding:
            continue
        trace = finding.get("trace") or []
        if not trace or not trace[0].get("function"):
            continue
        key = (finding["osv"], trace[0].get("module", ""), trace[0].get("version", ""))
        if is_exempt(*key, exemptions):
            exempted.add(key)
        else:
            blocking.add(key)
    return sorted(blocking), sorted(exempted)


def run(text, exemptions, out=sys.stdout):
    messages = read_messages(text)
    # An empty or truncated report must not read as a clean scan.
    if not any("config" in message for message in messages):
        print("govulncheck-gate: not a govulncheck report", file=out)
        return 1

    blocking, exempted = evaluate(messages, exemptions)
    for osv, module, version in exempted:
        print(f"exempt   {osv} {module}@{version}: {exemptions[osv][2]}", file=out)
    seen = {key[0] for key in exempted} | {key[0] for key in blocking}
    for osv in sorted(set(exemptions) - seen):
        print(f"notice   {osv} is no longer reported; remove its exemption", file=out)
    for osv, module, version in blocking:
        print(f"BLOCKING {osv} {module}@{version}: https://pkg.go.dev/vuln/{osv}", file=out)
    print(f"govulncheck-gate: {len(blocking)} blocking, {len(exempted)} exempt", file=out)
    return 1 if blocking else 0


def selftest():
    config = '{"config": {}}\n'

    def called(osv, module, version):
        return json.dumps({"finding": {"osv": osv, "trace": [
            {"module": module, "version": version, "package": module, "function": "New"},
        ]}}, indent=2) + "\n"

    def present(osv, module, version):
        return json.dumps({"finding": {"osv": osv, "trace": [
            {"module": module, "version": version},
        ]}}) + "\n"

    exemptions = {"GO-1": ("example.com/m", "v1.2.3", "test")}

    class Null:
        def write(self, _):
            pass

    cases = [
        ("clean report passes", config, 0),
        ("empty report fails", "", 1),
        ("called advisory fails", config + called("GO-2", "example.com/m", "v1.0.0"), 1),
        ("present but uncalled passes", config + present("GO-2", "example.com/m", "v1.0.0"), 0),
        ("exempt at the fixed release", config + called("GO-1", "example.com/m", "v1.2.3"), 0),
        ("exempt above the fixed release", config + called("GO-1", "example.com/m", "v1.10.0"), 0),
        ("below the fixed release fails", config + called("GO-1", "example.com/m", "v1.2.2"), 1),
        ("another module fails", config + called("GO-1", "example.com/other", "v9.0.0"), 1),
        ("an unparseable version fails", config + called("GO-1", "example.com/m", "v1.2.3-rc1"), 1),
        ("one blocker among exemptions fails",
         config + called("GO-1", "example.com/m", "v1.2.3") + called("GO-2", "example.com/m", "v1.2.3"), 1),
    ]
    failures = 0
    for name, text, want in cases:
        got = run(text, exemptions, out=Null())
        if got != want:
            print(f"FAIL {name}: exit {got}, want {want}")
            failures += 1
    for osv, (module, fixed, reason) in EXEMPT.items():
        if not module or parse_version(fixed) is None or not reason:
            print(f"FAIL {osv}: an exemption needs a module, a fixed release and a reason")
            failures += 1
    print(f"govulncheck-gate selftest: {len(cases)} cases, {failures} failed")
    return 1 if failures else 0


def main(argv):
    if argv == ["--selftest"]:
        return selftest()
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    with open(argv[0], encoding="utf-8") as report:
        return run(report.read(), EXEMPT)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
