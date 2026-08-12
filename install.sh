#!/bin/sh
# Rookery installer for Linux and macOS.
#
#   curl -fsSL https://rookery.sh/install.sh | sh
#
# This script has exactly one job: put a verified `rookery` binary on your PATH
# and tell you what to run next. It does not bootstrap an owner, create a
# workspace or start a service — `rookery onboard` does all of that, in Go,
# where it is written once and tested rather than twice in shell and PowerShell.
#
# POSIX sh, not bash: the macOS default shell and a minimal Debian image both
# have to run this, and `curl | sh` gives us whatever /bin/sh happens to be.
#
# Environment overrides:
#   ROOKERY_VERSION   install this tag instead of the latest release
#   ROOKERY_BIN_DIR   install here instead of the first writable default
#   ROOKERY_YES       =1 to answer yes to the host-tool prompt (non-interactive)
#   ROOKERY_NO_TOOLS  =1 to skip the host-tool step entirely

set -eu

REPO="rookery-ai/rookery"
TOOLS_PROMPTED=0

# ── output ───────────────────────────────────────────────────────────────────

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	B="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"; R="$(printf '\033[0m')"
	RED="$(printf '\033[31m')"; YEL="$(printf '\033[33m')"; GRN="$(printf '\033[32m')"
else
	B=""; DIM=""; R=""; RED=""; YEL=""; GRN=""
fi

say()  { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$B" "$R" "$*"; }
warn() { printf '%s warn%s %s\n' "$YEL" "$R" "$*" >&2; }
die()  { printf '%serror%s %s\n' "$RED" "$R" "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || die "this installer needs '$1' on PATH"
}

# ── platform ─────────────────────────────────────────────────────────────────

detect_platform() {
	os="$(uname -s)"
	case "$os" in
		Linux)  OS=linux ;;
		Darwin) OS=darwin ;;
		*)
			die "unsupported OS '$os'. Windows: use install.ps1 (irm https://rookery.sh/install.ps1 | iex)"
			;;
	esac

	arch="$(uname -m)"
	case "$arch" in
		x86_64|amd64)  ARCH=amd64 ;;
		aarch64|arm64) ARCH=arm64 ;;
		*)
			die "unsupported architecture '$arch'. Rookery ships amd64 and arm64; build from source for others."
			;;
	esac
}

# ── install location ─────────────────────────────────────────────────────────

# Prefer a directory already on PATH that we can write without sudo. A binary
# installed somewhere the shell will not look is the same as not installed, and
# is harder to diagnose, so an off-PATH fallback says so out loud at the end.
pick_bin_dir() {
	if [ -n "${ROOKERY_BIN_DIR:-}" ]; then
		BIN_DIR="$ROOKERY_BIN_DIR"
		mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
		return
	fi
	for d in "$HOME/.local/bin" /usr/local/bin; do
		if [ -d "$d" ] && [ -w "$d" ]; then BIN_DIR="$d"; return; fi
	done
	BIN_DIR="$HOME/.local/bin"
	mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
}

on_path() {
	case ":$PATH:" in *":$1:"*) return 0 ;; *) return 1 ;; esac
}

# ── release resolution ───────────────────────────────────────────────────────

latest_version() {
	# The API answers with the tag of the newest non-draft release. Parsed with
	# sed rather than jq: jq is not installed by default anywhere this runs.
	curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
		| sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
		| head -n 1
}

# A 404 here is overwhelmingly one specific thing while the repository is
# private: release assets require an authenticated request, so an anonymous
# download gets "Not Found" rather than "Unauthorized". Saying so is the
# difference between a two-minute fix and an hour of guessing.
download_failed() {
	say ""
	die "could not download $1

This usually means one of:
  • the repository is still private — release assets then need an authenticated
    request, and an anonymous download returns 404. Install from a locally built
    artifact, or wait for the public release.
  • the tag ${VERSION:-<latest>} has no release asset for ${OS}/${ARCH}.
  • the network or a proxy blocked the request.

Releases: https://github.com/$REPO/releases"
}

# ── host tools ───────────────────────────────────────────────────────────────
#
# Rookery runs without any of these, but degrades in ways that are easy to
# misread as bugs. python3 is the sharpest: without it the agent-tool AST
# guardrail in internal/agentdesigner self-skips, so generated tool scripts run
# unchecked. /healthz reports all four at runtime.

tool_purpose() {
	case "$1" in
		python3)   echo "agent-tool AST guardrail (without it, generated scripts run unchecked)" ;;
		rg)        echo "knowledge-base search" ;;
		pdftotext) echo "PDF text extraction" ;;
		tesseract) echo "OCR for scanned documents and images" ;;
	esac
}

# Package names differ per manager; this is the same class of mistake that made
# the rpm ship a Debian name and silently install no OCR.
pkg_name() {
	case "$2:$1" in
		dnf:rg|yum:rg|zypper:rg|pacman:rg) echo ripgrep ;;
		apt:rg)                            echo ripgrep ;;
		brew:rg)                           echo ripgrep ;;
		dnf:pdftotext|yum:pdftotext)       echo poppler-utils ;;
		zypper:pdftotext)                  echo poppler-tools ;;
		pacman:pdftotext)                  echo poppler ;;
		apt:pdftotext)                     echo poppler-utils ;;
		brew:pdftotext)                    echo poppler ;;
		dnf:tesseract|yum:tesseract)       echo tesseract ;;
		zypper:tesseract)                  echo tesseract-ocr ;;
		pacman:tesseract)                  echo tesseract ;;
		apt:tesseract)                     echo tesseract-ocr ;;
		brew:tesseract)                    echo tesseract ;;
		pacman:python3)                    echo python ;;
		*:python3)                         echo python3 ;;
	esac
}

detect_pkg_manager() {
	if [ "$OS" = darwin ]; then
		command -v brew >/dev/null 2>&1 && { PKG=brew; return; }
		PKG=""; return
	fi
	for m in dnf apt zypper pacman yum; do
		command -v "$m" >/dev/null 2>&1 && { PKG="$m"; return; }
	done
	PKG=""
}

install_cmd() {
	case "$PKG" in
		dnf)    echo "sudo dnf install -y $*" ;;
		yum)    echo "sudo yum install -y $*" ;;
		apt)    echo "sudo apt-get install -y $*" ;;
		zypper) echo "sudo zypper install -y $*" ;;
		pacman) echo "sudo pacman -S --needed --noconfirm $*" ;;
		brew)   echo "brew install $*" ;;
	esac
}

handle_host_tools() {
	[ "${ROOKERY_NO_TOOLS:-}" = 1 ] && return 0

	missing=""
	for t in python3 rg pdftotext tesseract; do
		command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
	done
	if [ -z "$missing" ]; then
		step "Host tools: all present"
		return 0
	fi

	step "Host tools"
	say "  Rookery runs without these, but some features quietly degrade:"
	for t in $missing; do
		printf '    %s%-10s%s %s\n' "$B" "$t" "$R" "$(tool_purpose "$t")"
	done
	say ""

	detect_pkg_manager
	if [ -z "$PKG" ]; then
		if [ "$OS" = darwin ]; then
			warn "no Homebrew found — install it from https://brew.sh, then: brew install $missing"
		else
			warn "no supported package manager found; install these with your distribution's tools"
		fi
		return 0
	fi

	pkgs=""
	for t in $missing; do pkgs="$pkgs $(pkg_name "$t" "$PKG")"; done
	# shellcheck disable=SC2086
	cmd="$(install_cmd $pkgs)"

	# A script piped from the network into a shell must not sudo four packages
	# on its own initiative. Ask — unless there is no terminal to ask on, in
	# which case print the command and move on rather than hanging forever.
	if [ "${ROOKERY_YES:-}" = 1 ]; then
		reply=y
	elif [ -t 0 ]; then
		printf '  Install them now with %s%s%s? [y/N] ' "$B" "$cmd" "$R"
		read -r reply </dev/tty 2>/dev/null || reply=n
	else
		say "  To install them:  ${B}${cmd}${R}"
		TOOLS_PROMPTED=1
		return 0
	fi

	case "$reply" in
		y|Y|yes|YES)
			[ "$PKG" = apt ] && sudo apt-get update -qq
			# shellcheck disable=SC2086
			if sh -c "$cmd"; then
				say "  ${GRN}installed${R}"
			else
				warn "installation failed — run it yourself: $cmd"
			fi
			;;
		*)
			say "  Skipped. Later:  ${B}${cmd}${R}"
			TOOLS_PROMPTED=1
			;;
	esac
}

# ── main ─────────────────────────────────────────────────────────────────────

need curl
need tar
detect_platform

VERSION="${ROOKERY_VERSION:-}"
if [ -z "$VERSION" ]; then
	step "Resolving the latest release"
	VERSION="$(latest_version || true)"
	[ -n "$VERSION" ] || download_failed "the release list"
fi
# Release tags carry a leading v; archive names do not.
NUM="${VERSION#v}"

ARCHIVE="rookery_${NUM}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/$REPO/releases/download/$VERSION"

TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

step "Downloading rookery $VERSION ($OS/$ARCH)"
curl -fsSL -o "$TMP/$ARCHIVE" "$BASE/$ARCHIVE" || download_failed "$ARCHIVE"

# Verify against the release's own checksums.txt. This catches a truncated
# download and a tampered mirror; it is not a substitute for the cosign
# signature, which is what proves the release itself is ours. `rookery version`
# below is the end-to-end check that the bytes actually run.
if curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt" 2>/dev/null; then
	step "Verifying checksum"
	if command -v sha256sum >/dev/null 2>&1; then
		SUM="$(sha256sum "$TMP/$ARCHIVE" | cut -d' ' -f1)"
	elif command -v shasum >/dev/null 2>&1; then
		SUM="$(shasum -a 256 "$TMP/$ARCHIVE" | cut -d' ' -f1)"
	else
		SUM=""
		warn "no sha256sum or shasum on PATH — skipping checksum verification"
	fi
	if [ -n "$SUM" ]; then
		WANT="$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | cut -d' ' -f1 || true)"
		[ -n "$WANT" ] || die "checksums.txt has no entry for $ARCHIVE"
		[ "$SUM" = "$WANT" ] || die "checksum mismatch for $ARCHIVE — refusing to install
  expected $WANT
  got      $SUM"
		say "  ${GRN}ok${R}"
	fi
else
	warn "checksums.txt not published for $VERSION — installing unverified"
fi

step "Extracting"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
[ -f "$TMP/rookery" ] || die "archive does not contain a rookery binary"
chmod +x "$TMP/rookery"

pick_bin_dir
step "Installing to $BIN_DIR"
if mv "$TMP/rookery" "$BIN_DIR/rookery" 2>/dev/null; then
	:
elif command -v sudo >/dev/null 2>&1 && sudo mv "$TMP/rookery" "$BIN_DIR/rookery"; then
	:
else
	die "cannot write to $BIN_DIR — set ROOKERY_BIN_DIR to a writable directory"
fi

INSTALLED_VERSION="$("$BIN_DIR/rookery" version 2>/dev/null | head -n 1 || true)"
[ -n "$INSTALLED_VERSION" ] || die "installed binary did not run — the download may be for the wrong platform"
say "  ${GRN}${INSTALLED_VERSION}${R}"

handle_host_tools

say ""
step "Done"
if ! on_path "$BIN_DIR"; then
	say ""
	warn "$BIN_DIR is not on your PATH. Add it:"
	say "    ${B}export PATH=\"$BIN_DIR:\$PATH\"${R}"
	say "  (put that in your shell profile to make it stick)"
fi
say ""
say "Next, set up your install — owner account, keys, first workspace and coder:"
say ""
say "    ${B}rookery onboard${R}"
say ""
if [ "$TOOLS_PROMPTED" = 1 ]; then
	say "${DIM}onboard will offer the skipped host tools again.${R}"
fi
