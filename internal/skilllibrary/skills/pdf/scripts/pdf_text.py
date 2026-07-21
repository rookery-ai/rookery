#!/usr/bin/env python3
"""Extract text from a PDF using the pdftotext CLI, falling back to pdfplumber.

Usage:
    python3 pdf_text.py <file.pdf> [--pages 1-5]

Prints the extracted text to stdout. Exits non-zero with a message on stderr if
no extraction backend is available.
"""
import argparse
import os
import shutil
import subprocess
import sys


def find_pdftotext():
    """Return the absolute path to pdftotext, or None.

    Agents run sandboxed with PATH pointing at the operator's directories, so a tool
    installed by cli-tool-installer lives in $HOME/.local/bin and must be found there
    first.
    """
    local = os.path.join(os.path.expanduser("~"), ".local", "bin", "pdftotext")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("pdftotext")


def extract_with_cli(binary, path, first, last):
    cmd = [binary, "-layout"]
    if first:
        cmd += ["-f", str(first)]
    if last:
        cmd += ["-l", str(last)]
    cmd += [path, "-"]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "pdftotext failed")
    return result.stdout


def extract_with_pdfplumber(path, first, last):
    import pdfplumber

    chunks = []
    with pdfplumber.open(path) as pdf:
        pages = pdf.pages
        if first or last:
            pages = pages[(first or 1) - 1 : (last or len(pages))]
        for page in pages:
            chunks.append(page.extract_text() or "")
    return "\n\n".join(chunks)


def parse_pages(spec):
    if not spec:
        return None, None
    if "-" in spec:
        a, _, b = spec.partition("-")
        return int(a), int(b)
    n = int(spec)
    return n, n


def main():
    ap = argparse.ArgumentParser(description="Extract text from a PDF.")
    ap.add_argument("pdf", help="path to the PDF file")
    ap.add_argument("--pages", help="page or range, e.g. 3 or 1-5")
    args = ap.parse_args()

    if not os.path.isfile(args.pdf):
        print(f"no such file: {args.pdf}", file=sys.stderr)
        return 1

    first, last = parse_pages(args.pages)

    binary = find_pdftotext()
    if binary:
        try:
            sys.stdout.write(extract_with_cli(binary, args.pdf, first, last))
            return 0
        except RuntimeError as exc:
            print(f"pdftotext failed ({exc}); trying pdfplumber", file=sys.stderr)

    try:
        sys.stdout.write(extract_with_pdfplumber(args.pdf, first, last))
        return 0
    except ImportError:
        print(
            "no PDF backend available. Install poppler's pdftotext via the "
            "cli-tool-installer skill, or: python3 -m pip install --user pdfplumber",
            file=sys.stderr,
        )
        return 2


if __name__ == "__main__":
    sys.exit(main())
