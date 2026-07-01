---
name: cli-tool-installer
description: Use this skill to install standalone CLI tools (pandoc, qpdf, poppler/pdftotext, ffmpeg, jq, yq, wkhtmltopdf, tesseract, libreoffice) into the user's local bin directory so other skills and agents can invoke them. Triggers include "install pandoc", "I need ffmpeg", "set up poppler", "make qpdf available", "tools are missing".
version: 1.0.0
license: MIT-0
category: Development
---

# CLI Tool Installer

Installs standalone CLI binaries into the user's persistent local bin directory
(`$HOME/.local/bin/`). On this platform agents run sandboxed: they cannot write
to system paths (`/usr/bin`) and their `PATH` points at the operator's dirs, not
the agent's `$HOME`. So **every installed tool must be invoked by absolute path**:
`$HOME/.local/bin/pandoc`, never bare `pandoc`.

## Where tools live

- Binaries: `$HOME/.local/bin/` (persists across all of this user's agent runs).
- Temp files: `$TMPDIR` (= `$HOME/tmp`) — never `/tmp`.
- Network: available during runs, so downloads work.

## Install a static binary (the common case)

Most Linux CLI tools ship a static/release tarball on GitHub. Download, extract,
place the binary in `$HOME/.local/bin/`.

```bash
# Example: pandoc
mkdir -p "$HOME/.local/bin" "$HOME/tmp"
curl -fsSL "https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-linux-amd64.tar.gz" \
  -o "$HOME/tmp/pandoc.tar.gz"
tar -xzf "$HOME/tmp/pandoc.tar.gz" -C "$HOME/tmp" --strip-components=1
mv "$HOME/tmp/bin/pandoc" "$HOME/.local/bin/pandoc"
chmod +x "$HOME/.local/bin/pandoc"
"$HOME/.local/bin/pandoc" --version
```

## Check whether a tool is already installed

A tool is "available" if it exists at `$HOME/.local/bin/<name>` OR resolves on
`PATH`. Always check first to avoid re-downloading:

```bash
[ -x "$HOME/.local/bin/pandoc" ] && echo "installed" || echo "missing"
command -v pandoc >/dev/null 2>&1 && echo "on PATH" || echo "not on PATH"
```

## Known static-binary sources

| Tool | URL pattern |
|------|-------------|
| pandoc | github.com/jgm/pandoc/releases |
| qpdf | github.com/qpdf/qpdf/releases |
| jq | github.com/jqlang/jq/releases (use `jq-linux-amd64` single binary) |
| yq | github.com/mikefarah/yq/releases (single binary) |
| ffmpeg | johnvansickle.com/ffmpeg/releases (static build) |
| poppler (pdftotext) | no single static binary — prefer `pip install --user pdfplumber` for text extraction |
| wkhtmltopdf | github.com/wkhtmltopdf/packaging/releases |
| tesseract | github.com/UB-Mannheim/tesseract/wiki (or distro package) |

## Pip-installed tools (alternative)

If a tool has a Python wrapper with a bundled binary, `pip install --user`
works and persists in `$HOME/.local`:

```bash
python3 -m pip install --user --quiet pypandoc
```

## Best practices

- NEVER use `sudo`, `apt install`, or `dnf install` — they write to system paths
  the sandbox denies, and would affect all users.
- ALWAYS invoke installed tools by absolute path (`$HOME/.local/bin/<tool>`).
- After install, verify with `<tool> --version` and report the absolute path to
  the user so they know how to call it.
- If a download fails (network, 404), report which tool is missing — the
  depending skill should fall back gracefully.