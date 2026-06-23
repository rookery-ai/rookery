//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
	"golang.org/x/sys/unix"
)

// landlockCreateRulesetVersion is LANDLOCK_CREATE_RULESET_VERSION (flag bit 0).
const landlockCreateRulesetVersion = 1

// Supported reports whether the running kernel supports Landlock. It performs a
// read-only ABI probe (landlock_create_ruleset with the VERSION flag) and does
// NOT restrict the calling process, so it is safe to call from the parent.
func Supported() bool {
	ret, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return false
	}
	return int(ret) > 0
}

// defaultReadOnlySystemPaths is the broad-but-safe set of system locations the
// coder CLI (node) and the bash/python it spawns need to read and execute. Each
// is applied with IgnoreIfMissing so the list stays portable across distros
// (e.g. /etc/pki on Fedora vs /etc/ssl on Debian). It deliberately omits the
// data directory, so vaults, the DB, config.yaml and other users' homes stay
// unreadable by default-deny.
//
// NOTE: intentionally generous so agents "just run". Tighten after an
// strace-based audit of a real run (see the implementation plan's step 4).
func defaultReadOnlySystemPaths() []string {
	return []string{
		"/usr", "/bin", "/sbin", "/lib", "/lib64",
		"/etc", "/opt", "/proc", "/sys",
	}
}

// defaultReadWriteFiles are device files the subprocess tree writes to.
func defaultReadWriteFiles() []string {
	return []string{"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty"}
}

// SystemReadOnlyPaths exposes the default RO system list to callers building a Spec.
func SystemReadOnlyPaths() []string { return defaultReadOnlySystemPaths() }

// SystemReadWriteFiles exposes the default device-file RW list to callers.
func SystemReadWriteFiles() []string { return defaultReadWriteFiles() }

// Exec is the helper entrypoint. It applies resource limits and Landlock
// confinement to the current process, then exec()s spec.Command. On success it
// never returns (the process image is replaced); it only returns on error.
func Exec(spec Spec) error {
	if len(spec.Command) == 0 {
		return fmt.Errorf("sandbox: empty command")
	}

	applyRlimits(spec)

	if spec.Dir != "" {
		if err := os.Chdir(spec.Dir); err != nil {
			return fmt.Errorf("sandbox chdir %q: %w", spec.Dir, err)
		}
	}

	rules := make([]landlock.Rule, 0, len(spec.ReadOnlyPaths)+len(spec.ReadWritePaths)+len(spec.ReadWriteFiles))
	for _, p := range spec.ReadOnlyPaths {
		rules = append(rules, landlock.RODirs(p).IgnoreIfMissing())
	}
	for _, p := range spec.ReadWritePaths {
		rules = append(rules, landlock.RWDirs(p).IgnoreIfMissing())
	}
	for _, f := range spec.ReadWriteFiles {
		rules = append(rules, landlock.RWFiles(f).IgnoreIfMissing())
	}

	// BestEffort downgrades to the best Landlock ABI the kernel offers (or a
	// no-op if Landlock is entirely absent) rather than failing the run.
	if err := landlock.V5.BestEffort().RestrictPaths(rules...); err != nil {
		return fmt.Errorf("sandbox landlock: %w", err)
	}

	bin := spec.Command[0]
	if !filepath.IsAbs(bin) {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			return fmt.Errorf("sandbox lookpath %q: %w", bin, err)
		}
		bin = resolved
	}
	return syscall.Exec(bin, spec.Command, spec.Env)
}

// applyRlimits sets the resource limits requested in the spec. Each limit is
// only applied when its value is > 0, so the caller can opt out (notably for
// RLIMIT_AS, which node/V8 dislikes because it reserves a huge address space).
func applyRlimits(spec Spec) {
	if spec.NoFile > 0 {
		lim := unix.Rlimit{Cur: spec.NoFile, Max: spec.NoFile}
		_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &lim)
	}
	if spec.CPUSeconds > 0 {
		lim := unix.Rlimit{Cur: uint64(spec.CPUSeconds), Max: uint64(spec.CPUSeconds)}
		_ = unix.Setrlimit(unix.RLIMIT_CPU, &lim)
	}
	if spec.MemoryMB > 0 {
		b := uint64(spec.MemoryMB) * 1024 * 1024
		lim := unix.Rlimit{Cur: b, Max: b}
		_ = unix.Setrlimit(unix.RLIMIT_AS, &lim)
	}
}
