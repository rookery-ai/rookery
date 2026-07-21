#!/usr/bin/env python3
"""Convert a .docx file to markdown (or another pandoc format) using the pandoc CLI.

Usage:
    python3 docx_convert.py <file.docx> [--to markdown] [--out result.md]

Prints the converted text to stdout unless --out is given.
"""
import argparse
import os
import shutil
import subprocess
import sys


def find_pandoc():
    """Return the absolute path to pandoc, or None.

    cli-tool-installer places binaries in $HOME/.local/bin, which is not on the
    sandboxed agent's PATH, so that location is checked first.
    """
    local = os.path.join(os.path.expanduser("~"), ".local", "bin", "pandoc")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("pandoc")


def main():
    ap = argparse.ArgumentParser(description="Convert a .docx via pandoc.")
    ap.add_argument("docx", help="path to the .docx file")
    ap.add_argument("--to", default="markdown", help="pandoc output format (default: markdown)")
    ap.add_argument("--out", help="write to this file instead of stdout")
    args = ap.parse_args()

    if not os.path.isfile(args.docx):
        print(f"no such file: {args.docx}", file=sys.stderr)
        return 1

    binary = find_pandoc()
    if not binary:
        print(
            "pandoc is not installed. Install it with the cli-tool-installer skill, "
            "then call it at $HOME/.local/bin/pandoc",
            file=sys.stderr,
        )
        return 2

    cmd = [binary, args.docx, "-t", args.to]
    if args.out:
        cmd += ["-o", args.out]

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(result.stderr.strip() or "pandoc failed", file=sys.stderr)
        return result.returncode

    if args.out:
        print(f"wrote {args.out}")
    else:
        sys.stdout.write(result.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
