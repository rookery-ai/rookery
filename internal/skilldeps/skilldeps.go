// Package skilldeps installs and runs the Python dependencies a skill declares, in one
// virtual environment per name under the workspace home.
//
// The home is persistent on purpose — it is the same per-workspace cache
// cli-tool-installer uses for $HOME/.local/bin — so the first run that needs a package
// pays for it and later runs reuse it. What this package adds is CONTAINMENT, because a
// harmful package executes code twice: when it is installed (setup.py, a build backend)
// and when the script that imports it runs. Install gets a sandbox that can write only
// its own environment and holds no secrets; Run gets one that cannot see the workspace
// home (coder credentials, other environments) and holds only the secrets it was named.
//
// Both coder kinds reach the same code: the API engine in-process, a CLI coder through
// `rookery deps`. Neither needs a bridge — Landlock restrictions stack, so a process
// already confined by the CLI coder's sandbox can confine its own child further.
package skilldeps

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/buildphase"
)

// EnvsRel is where environments live, relative to the workspace home.
const EnvsRel = ".local/share/rookery/envs"

// ErrSkillBuild refuses an install during a skill build: the skill is written for an
// agent to install at run time, and the builder is told so.
var ErrSkillBuild = errors.New("packages are installed by the agent at run time, not during a skill build — write the install step into SKILL.md instead")

var envNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateEnvName rejects anything but a lowercase slug, so a name can never climb out
// of EnvsRel or reach the coder's credentials beside it.
func ValidateEnvName(name string) error {
	if !envNameRe.MatchString(name) {
		return fmt.Errorf("environment name %q must be lowercase letters, digits and hyphens (max 64), starting with a letter or digit", name)
	}
	return nil
}

// EnvDir returns the directory of the named environment under home.
func EnvDir(home, name string) (string, error) {
	if err := ValidateEnvName(name); err != nil {
		return "", err
	}
	return filepath.Join(home, EnvsRel, name), nil
}

// pep508Re is a deliberately narrow PEP 508 subset: a name, optional extras, and an
// optional comma-separated list of version clauses. No markers, no spaces.
var pep508Re = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9._,-]+\])?((==|>=|<=|~=|!=|>|<)[A-Za-z0-9.*+!_-]+(,(==|>=|<=|~=|!=|>|<)[A-Za-z0-9.*+!_-]+)*)?$`)

// ValidateRequirement accepts a package requirement or an https/git+https URL on a DNS
// host. Everything else is refused before pip sees it — above all anything pip would
// parse as an option, which is how an extra package index (dependency confusion) or a
// requirements file would be smuggled in.
func ValidateRequirement(spec string) error {
	if spec == "" || strings.TrimSpace(spec) != spec || strings.ContainsAny(spec, " \t\r\n") {
		return fmt.Errorf("requirement %q must be a single token with no whitespace", spec)
	}
	if strings.HasPrefix(spec, "-") {
		return fmt.Errorf("requirement %q looks like a pip option; only package names and https URLs are allowed", spec)
	}
	if pep508Re.MatchString(spec) {
		return nil
	}
	u, err := url.Parse(spec)
	if err != nil || (u.Scheme != "https" && u.Scheme != "git+https") {
		return fmt.Errorf("requirement %q is neither a package name nor an https/git+https URL", spec)
	}
	if u.User != nil {
		return fmt.Errorf("requirement %q must not carry credentials in the URL", spec)
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return fmt.Errorf("requirement %q must name a DNS host, not an IP address", spec)
	}
	return nil
}

// SkillBuildActive reports whether the calling environment is a skill build.
func SkillBuildActive(getenv func(string) string) bool {
	return getenv(buildphase.KindEnvVar) == buildphase.KindSkill
}
