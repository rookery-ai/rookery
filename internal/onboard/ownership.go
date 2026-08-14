package onboard

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// Ownership reports whether a system package manager owns a file.
//
// This exists for exactly one reason: `rm /usr/bin/rookery` under a deb or rpm
// install leaves the package database claiming a file that is gone. Nothing
// warns, and the only repair is `dnf reinstall` / `apt reinstall` — which
// nobody thinks to run, because from the outside it looks like the uninstall
// worked. So uninstall asks the system who owns the binary before touching it,
// and hands the user their package manager's own command instead.
type Ownership struct {
	// Managed is true only when a package manager positively claimed the file.
	// A probe that could not run, or that returned nothing, is NOT managed —
	// see the failure policy below.
	Managed bool
	// Manager names the owner ("rpm", "dpkg") when Managed.
	Manager string
	// Package is the owning package's name, for the message.
	Package string
	// RemoveCommand is what the user should run instead, e.g.
	// "sudo dnf remove rookery".
	RemoveCommand string
}

// Runner executes a command and returns its combined output. Indirected for the
// same reason onboard's LookPath is: the hosts this logic is about — a Fedora
// box with rpm, a Debian box with dpkg — are not the host it is developed on,
// and the package-name mapping is precisely what shipped wrong in the rpm
// before. A test must be able to describe a machine it cannot run.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// DefaultRunner runs the real command.
func DefaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// OwnerOf asks the host's package manager whether it owns path.
//
// The failure policy is deliberately asymmetric, and it is the opposite of the
// one that feels safe. An inconclusive probe reports NOT managed, so uninstall
// proceeds and removes the binary. Reporting "managed" on a failed probe would
// mean an archive or install.sh user — the majority, and the only people who
// have no package manager to fall back on — could never uninstall at all, and
// would be told to run a command their system does not have. The cost of the
// other direction is a package database entry to repair; the cost of this one
// is an uninstall that cannot work and cannot be explained.
func OwnerOf(ctx context.Context, run Runner, look LookPath, path string) Ownership {
	if run == nil {
		run = DefaultRunner
	}
	if look == nil {
		look = DefaultLookPath
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	// rpm -qf answers with the owning package, or exits non-zero with "file ...
	// is not owned by any package". dpkg -S is the Debian equivalent. Both are
	// asked only when present, so a Fedora box is never asked about dpkg.
	if _, err := look("rpm"); err == nil {
		if out, err := run(ctx, "rpm", "-qf", abs); err == nil {
			if pkg := firstField(string(out)); pkg != "" {
				return Ownership{
					Managed: true, Manager: "rpm", Package: packageBase(pkg),
					RemoveCommand: "sudo dnf remove " + packageBase(pkg),
				}
			}
		}
	}
	if _, err := look("dpkg"); err == nil {
		if out, err := run(ctx, "dpkg", "-S", abs); err == nil {
			// "rookery: /usr/bin/rookery"
			if name, _, ok := strings.Cut(strings.TrimSpace(string(out)), ":"); ok && name != "" {
				return Ownership{
					Managed: true, Manager: "dpkg", Package: name,
					RemoveCommand: "sudo apt remove " + name,
				}
			}
		}
	}
	return Ownership{}
}

// firstField returns the first whitespace-separated token of the first
// non-empty line, or "".
func firstField(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

// packageBase strips rpm's version-release-arch suffix so the message names
// something the user can actually pass to dnf: "rookery-0.1.4-1.x86_64" is a
// valid answer from `rpm -qf` and NOT a valid argument to `dnf remove`.
func packageBase(nvra string) string {
	// Trim from the last two dashes: name-version-release.arch.
	for i := 0; i < 2; i++ {
		if idx := strings.LastIndex(nvra, "-"); idx > 0 {
			nvra = nvra[:idx]
		}
	}
	return nvra
}
