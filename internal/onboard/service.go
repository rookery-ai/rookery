package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ServiceSupport says whether this platform can have the server installed as a
// managed service, and what to tell the operator when it cannot.
//
// The honest report matters more than uniformity here. Linux ships a systemd
// user unit in the deb, the rpm and every archive; launchd and the Windows
// service control manager are Tier 2 and not yet built. Pretending otherwise
// would mean writing a plist that half works, and a half-working service is
// harder to diagnose than none.
type ServiceSupport struct {
	// Managed reports whether onboard can install and enable a service itself.
	Managed bool
	// Kind names the mechanism, for messages ("systemd user unit").
	Kind string
	// Foreground is how to run the server by hand on this platform.
	Foreground string
	// Restart is the command that restarts the managed service, and is empty
	// when Managed is false.
	//
	// It is a field rather than a string built at the call site because there is
	// exactly one call site — `rookery upgrade` — and it used to hardcode
	// systemctl behind an `if svc.Managed` test. That was correct only while
	// Linux was the sole managed platform: the moment Windows became one, the
	// same branch would have told a Windows operator to run a command that does
	// not exist there, which is the precise bug the comment at that call site
	// records having already fixed once.
	Restart string
	// Note explains the situation when Managed is false.
	Note string
}

// ServiceFor describes service support for a GOOS value.
func ServiceFor(goos string) ServiceSupport {
	switch goos {
	case "linux":
		return ServiceSupport{
			Managed:    true,
			Kind:       "systemd user unit",
			Foreground: "rookery serve",
			Restart:    "systemctl --user restart rookery.service",
		}
	case "darwin":
		return ServiceSupport{
			Kind:       "launchd",
			Foreground: "rookery serve",
			Note: "launchd registration is not built yet, so Rookery does not start at login on macOS. " +
				"Run it in a terminal, or keep it alive with your own launchd agent.",
		}
	case "windows":
		// A Task Scheduler task triggered at logon, not a Service Control
		// Manager service.
		//
		// The constraint that decides this is not neatness: it has to work for a
		// standard non-administrator, with no stored credentials, and reach a
		// data directory under the user's own profile. An SCM service needs
		// administrator rights to install and then runs as a different
		// principal, which reintroduces exactly the problem the Linux side
		// avoids by using a systemd USER unit. Relying on S4U would need a
		// batch-logon right a standard user may not hold.
		//
		// Accepted cost, recorded rather than engineered around: a logon task
		// running a console application shows a console window. Every way of
		// hiding it trades a cosmetic problem for a credential prompt or an
		// administrator requirement.
		return ServiceSupport{
			Managed:    true,
			Kind:       "Windows logon task",
			Foreground: "rookery serve",
			Restart:    `schtasks /End /TN ` + TaskName + ` && schtasks /Run /TN ` + TaskName,
		}
	default:
		return ServiceSupport{
			Kind:       "none",
			Foreground: "rookery serve",
			Note:       "No service integration exists for this platform. Run the server in the foreground.",
		}
	}
}

// SystemdUnitPath is where a per-user unit belongs. The unit is per-user by
// design: the server owns a data directory under $HOME, so a system unit would
// have to run as some other user and could not reach it.
func SystemdUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", "rookery.service")
}

// PackagedUnitPaths are where the deb and rpm drop the unit. A package must not
// write into a user's home directory, so it ships the file to /usr/share and
// leaves installing it to the user — which is the step `onboard` performs.
var PackagedUnitPaths = []string{
	"/usr/share/rookery/rookery.service",
	"/usr/local/share/rookery/rookery.service",
}

// FindPackagedUnit returns the shipped unit file, if this install has one.
func FindPackagedUnit() (string, bool) {
	for _, p := range PackagedUnitPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// UnitFileFor renders a systemd user unit pointing at a specific binary.
//
// It is generated rather than copied whenever the packaged copy is absent —
// someone who installed via install.sh or an archive has a binary in
// ~/.local/bin, and the packaged unit hardcodes /usr/bin/rookery, which would
// start nothing.
func UnitFileFor(binary, dataDir string) string {
	return fmt.Sprintf(`[Unit]
Description=Rookery control plane
Documentation=https://github.com/rookery-ai/rookery
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s serve
Restart=on-failure
RestartSec=5s

# The data directory holds the SQLite database, every workspace vault, and the
# claude-homes credential trees. Keep it out of the binary's install prefix so a
# package upgrade cannot touch user data.
Environment=ROOKERY_DATA_DIR=%s
# Set this when the app sits behind a reverse proxy or is reached by a name
# other than the one the browser used, otherwise OAuth redirect URIs are wrong.
#Environment=ROOKERY_PUBLIC_URL=https://agents.example.com

# Hardening. NOT a substitute for the Landlock confinement the app applies to
# coder subprocesses itself — this protects the host from the server process,
# Landlock protects one workspace from another.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%s

[Install]
WantedBy=default.target
`, binary, dataDir, dataDir)
}

// CurrentService describes service support for the running platform.
func CurrentService() ServiceSupport { return ServiceFor(runtime.GOOS) }
