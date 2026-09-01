// Package onboard holds the platform knowledge behind `rookery onboard`: which
// host tools a working install wants, what each package manager calls them, and
// how the server is expected to run on each operating system.
//
// It lives in Go rather than in install.sh and install.ps1 so that the mapping
// is written once and tested, instead of twice in two shell dialects neither of
// which CI can meaningfully exercise. It also has to serve the operator who
// installed from an rpm, a deb or a tarball and never ran either script.
package onboard

import (
	"os/exec"
	"runtime"
	"sort"
)

// HostTool is one external binary Rookery degrades without.
//
// None of these is required to start. Each absence changes behaviour in a way
// that reads as a bug rather than as a missing dependency, which is why Purpose
// describes the CONSEQUENCE and not the tool.
type HostTool struct {
	// Bin is the canonical name: what /healthz reports, what every package-name
	// map is keyed by, and the first name probed. It is NOT necessarily the only
	// name the binary has — see WindowsBins.
	Bin string
	// Purpose is what stops working without it, in the operator's terms.
	Purpose string
	// Critical marks a tool whose absence is a security property rather than a
	// convenience: python3 gates the agent-tool AST guardrail, so without it
	// generated tool scripts run unchecked.
	Critical bool

	// WindowsBins are additional names to try on Windows, in preference order
	// after Bin.
	//
	// Windows is the only platform that diverges, and it does so for one tool.
	// python.org's distribution — which is what Python.Python.3.13 installs, and
	// what install.ps1 has always probed for as `python` — ships python.exe and
	// py.exe and no python3.exe. So this package probing `python3` alone meant
	// the installer could install Python, report success, and setup would report
	// it missing on that run and on every run afterwards, because installing it
	// again changed nothing.
	WindowsBins []string

	// VerifyByRunning requires a candidate to execute before it counts.
	//
	// Set for python3 alone, because it is the only one of these with a known
	// decoy: stock Windows ships an App Execution Alias stub named python3.exe
	// that resolves through LookPath and opens the Microsoft Store. Existence
	// alone would report a missing tool as PRESENT, which is strictly worse than
	// the bug this file is fixing — the guardrail self-skips silently, whereas
	// re-offering an installed tool is at least visible.
	VerifyByRunning bool
}

// Bins returns the candidate names for this tool on goos, in preference order.
func (t HostTool) Bins(goos string) []string {
	if goos != "windows" || len(t.WindowsBins) == 0 {
		return []string{t.Bin}
	}
	return append([]string{t.Bin}, t.WindowsBins...)
}

// HostTools is the canonical set. /healthz reports the same four, the container
// image installs them, and both native package formats recommend them.
var HostTools = []HostTool{
	{
		Bin:             "python3",
		Purpose:         "agent-tool AST guardrail — without it, generated tool scripts run unchecked",
		Critical:        true,
		WindowsBins:     []string{"python", "py"},
		VerifyByRunning: true,
	},
	{Bin: "rg", Purpose: "fast knowledge-base search (a slower pure-Go fallback is used without it)"},
	{Bin: "pdftotext", Purpose: "PDF text extraction (a weaker pure-Go fallback is used without it)"},
	{Bin: "tesseract", Purpose: "OCR for scanned documents and images"},
}

// LookPath is the probe, indirected so tests can describe a host they are not
// running on. It is the same reason coder detection takes an injectable lookup.
type LookPath func(string) (string, error)

// DefaultLookPath probes the real PATH.
func DefaultLookPath(bin string) (string, error) { return exec.LookPath(bin) }

// Missing returns the host tools not currently resolvable, in canonical order.
//
// A nil look means "this machine", and gets the full resolution: PATH first,
// then the directories the Windows installers use that are not on it. Passing a
// look explicitly means "describe a PATH and nothing else" — no directory
// search, no execution check — because a caller supplying a fake lookup is
// describing a filesystem that does not exist and has nothing there to stat or
// run.
func Missing(look LookPath) []HostTool {
	if look == nil {
		return MissingOn(CurrentHost())
	}
	return MissingOn(Host{GOOS: runtime.GOOS, LookPath: look})
}

// Manager is a host package manager Rookery knows how to drive.
type Manager string

const (
	ManagerNone   Manager = ""
	ManagerDNF    Manager = "dnf"
	ManagerAPT    Manager = "apt"
	ManagerZypper Manager = "zypper"
	ManagerPacman Manager = "pacman"
	ManagerBrew   Manager = "brew"
	ManagerWinget Manager = "winget"
)

// packageNames maps a tool binary to its package name per manager.
//
// These differ, and getting one wrong fails in the worst available way: a weak
// dependency or an install command naming a package that does not exist is
// dropped in silence. That is exactly how the rpm shipped Debian's
// `tesseract-ocr` to Fedora — which calls it `tesseract` — and installed no OCR
// for the package's entire life without ever printing an error.
var packageNames = map[Manager]map[string]string{
	ManagerDNF: {
		"python3": "python3", "rg": "ripgrep",
		"pdftotext": "poppler-utils", "tesseract": "tesseract",
	},
	ManagerAPT: {
		"python3": "python3", "rg": "ripgrep",
		"pdftotext": "poppler-utils", "tesseract": "tesseract-ocr",
	},
	ManagerZypper: {
		"python3": "python3", "rg": "ripgrep",
		"pdftotext": "poppler-tools", "tesseract": "tesseract-ocr",
	},
	ManagerPacman: {
		"python3": "python", "rg": "ripgrep",
		"pdftotext": "poppler", "tesseract": "tesseract",
	},
	ManagerBrew: {
		"python3": "python3", "rg": "ripgrep",
		"pdftotext": "poppler", "tesseract": "tesseract",
	},
	// All four are on winget, which is what lets Windows reach full feature
	// coverage without Chocolatey or Scoop. Poppler's id is the one nobody
	// guesses: oschwartz10612.Poppler is the maintained Windows build, and it
	// is what ships pdftotext.exe.
	ManagerWinget: {
		"python3": "Python.Python.3.13", "rg": "BurntSushi.ripgrep.MSVC",
		"pdftotext": "oschwartz10612.Poppler", "tesseract": "UB-Mannheim.TesseractOCR",
	},
}

// PackageFor returns the package name a manager uses for a tool binary, and
// whether it knows one at all.
func PackageFor(m Manager, bin string) (string, bool) {
	names, ok := packageNames[m]
	if !ok {
		return "", false
	}
	name, ok := names[bin]
	return name, ok
}

// DetectManager picks the host package manager. Order matters only on Linux,
// where a system can carry more than one; the first found is the native one on
// every distribution family we map.
func DetectManager(look LookPath) Manager {
	if look == nil {
		look = DefaultLookPath
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := look("brew"); err == nil {
			return ManagerBrew
		}
		return ManagerNone
	case "windows":
		if _, err := look("winget"); err == nil {
			return ManagerWinget
		}
		return ManagerNone
	default:
		for _, m := range []Manager{ManagerDNF, ManagerAPT, ManagerZypper, ManagerPacman} {
			bin := string(m)
			if m == ManagerAPT {
				bin = "apt-get"
			}
			if _, err := look(bin); err == nil {
				return m
			}
		}
		return ManagerNone
	}
}

// InstallCommands returns the shell commands that install the given tools.
//
// winget takes one package per invocation, so Windows gets a command per tool
// while every other manager gets a single line. Returning a slice rather than
// one string keeps that difference from having to be special-cased by callers.
func InstallCommands(m Manager, tools []HostTool) []string {
	if m == ManagerNone || len(tools) == 0 {
		return nil
	}

	var pkgs []string
	for _, t := range tools {
		if name, ok := PackageFor(m, t.Bin); ok {
			pkgs = append(pkgs, name)
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	sort.Strings(pkgs)

	switch m {
	case ManagerWinget:
		out := make([]string, 0, len(pkgs))
		for _, p := range pkgs {
			out = append(out, "winget install -e --id "+p+
				" --accept-source-agreements --accept-package-agreements")
		}
		return out
	case ManagerBrew:
		// Homebrew must not be run under sudo; it refuses, and doing it anyway
		// leaves root-owned files in the prefix that break later installs.
		return []string{"brew install " + join(pkgs)}
	case ManagerPacman:
		return []string{"sudo pacman -S --needed --noconfirm " + join(pkgs)}
	case ManagerAPT:
		return []string{"sudo apt-get update && sudo apt-get install -y " + join(pkgs)}
	case ManagerZypper:
		return []string{"sudo zypper install -y " + join(pkgs)}
	default: // dnf
		return []string{"sudo dnf install -y " + join(pkgs)}
	}
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
