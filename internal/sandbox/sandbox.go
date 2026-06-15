// Package sandbox wraps Firejail for isolated subprocess execution.
// If Firejail is not available (e.g. in CI), Run/RunCommand fall back to a
// plain exec with a timeout — callers should not rely on isolation in that case.
//
// Secret injection: when Firejail is in use and a per-user home dir is
// configured, secrets are written to a 0600 temp file inside the user's
// sandbox home (which is bind-mounted read-write into the jail). A shell
// wrapper sources the file and deletes it before exec-ing the real command.
// This means the firejail binary itself — a host-side process — never carries
// secret values in its own environment. They exist only inside the jail (in
// the sourcing shell's env, then inherited by the final process via exec).
package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTimeout = 60 * time.Second
	firejailBin    = "firejail"
	maxOutputBytes = 1 << 20 // 1 MB cap on stdout+stderr
)

// Result holds the outcome of one sandbox execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// RunOptions configures a single sandbox invocation.
type RunOptions struct {
	Timeout      time.Duration // 0 → defaultTimeout
	Env          []string      // KEY=VALUE secret pairs (injected only inside the jail)
	WorkDir      string        // working directory for the process (host path)
	AllowNet     bool          // if false, --net=none is added
	UserHomeDir  string        // per-user persistent dir to mount as home (--private=dir)
	BlacklistDir string        // host path to blacklist inside sandbox (e.g. <data_dir>)
}

// Run executes a Python script inside Firejail (or bare exec if unavailable).
func Run(ctx context.Context, scriptPath string, opts RunOptions) (*Result, error) {
	return run(ctx, []string{"python3", scriptPath}, opts)
}

// RunCommand executes an arbitrary command inside Firejail (or bare exec if unavailable).
func RunCommand(ctx context.Context, command []string, opts RunOptions) (*Result, error) {
	return run(ctx, command, opts)
}

// SandboxHomeDir returns the path that Firejail uses as the home directory
// inside the sandbox — the OS user's actual home.
func SandboxHomeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// Available returns true if the firejail binary is in PATH.
func Available() bool {
	_, err := exec.LookPath(firejailBin)
	return err == nil
}

// ParseChatLines extracts [CHAT] lines from agent stdout.
func ParseChatLines(stdout string) []string {
	var lines []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[CHAT]") {
			msg := strings.TrimSpace(strings.TrimPrefix(line, "[CHAT]"))
			if msg != "" {
				lines = append(lines, msg)
			}
		}
	}
	return lines
}

// ─── Internal ─────────────────────────────────────────────────────────────────

func run(ctx context.Context, command []string, opts RunOptions) (*Result, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	var cmd *exec.Cmd

	if Available() {
		if len(opts.Env) > 0 && opts.UserHomeDir != "" {
			// Secure injection path: write secrets to a 0600 file inside the
			// user's sandbox home dir (bind-mounted RW into the jail). A shell
			// wrapper sources the file, deletes it, then exec-s the real command.
			// The firejail binary itself never receives secrets in its env.
			sandboxHome := SandboxHomeDir()
			hostSecretsFile, jailSecretsPath, err := writeSecretsFile(opts.UserHomeDir, sandboxHome, opts.Env)
			if err == nil {
				defer os.Remove(hostSecretsFile) // safety net if shell rm fails
				shellCmd := ". " + shellQuote(jailSecretsPath) +
					" && rm -f " + shellQuote(jailSecretsPath) +
					" && exec " + shellJoin(command)
				wrapped := []string{"/bin/sh", "-c", shellCmd}
				cmd = buildFirejailCmd(ctx, wrapped, timeout, opts)
				cmd.Env = os.Environ() // no secrets in firejail's own env
			} else {
				// writeSecretsFile failed — fall back to env var injection
				cmd = buildFirejailCmd(ctx, command, timeout, opts)
				cmd.Env = append(os.Environ(), opts.Env...)
			}
		} else {
			cmd = buildFirejailCmd(ctx, command, timeout, opts)
			cmd.Env = append(os.Environ(), opts.Env...)
		}
	} else {
		cmd = buildFallbackCmd(ctx, command)
		cmd.Env = append(os.Environ(), opts.Env...)
	}

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	stdoutStr := capString(stdout.String(), maxOutputBytes/2)
	stderrStr := capString(stderr.String(), maxOutputBytes/2)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return &Result{
				Stdout:   stdoutStr,
				Stderr:   stderrStr + "\n[SANDBOX] timed out",
				ExitCode: -1,
				Duration: time.Since(start),
			}, fmt.Errorf("agent timed out after %s", timeout)
		} else {
			return nil, fmt.Errorf("sandbox exec: %w", err)
		}
	}

	return &Result{
		Stdout:   stdoutStr,
		Stderr:   stderrStr,
		ExitCode: exitCode,
		Duration: time.Since(start),
	}, nil
}

func buildFirejailCmd(ctx context.Context, command []string, timeout time.Duration, opts RunOptions) *exec.Cmd {
	h := int(timeout.Hours())
	m := int(timeout.Minutes()) % 60
	s := int(timeout.Seconds()) % 60
	timeoutStr := fmt.Sprintf("%02d:%02d:%02d", h, m, s)

	args := []string{
		"--quiet",
		"--private-tmp",
		"--noroot",
		"--caps.drop=all",
		"--seccomp",
		"--timeout=" + timeoutStr,
	}

	if opts.UserHomeDir != "" {
		args = append(args, "--private="+opts.UserHomeDir)
		if opts.BlacklistDir != "" {
			args = append(args, "--blacklist="+opts.BlacklistDir)
		}
	} else {
		args = append(args, "--private")
	}

	if !opts.AllowNet {
		args = append(args, "--net=none")
	}

	args = append(args, command...)
	return exec.CommandContext(ctx, firejailBin, args...)
}

func buildFallbackCmd(ctx context.Context, command []string) *exec.Cmd {
	return exec.CommandContext(ctx, command[0], command[1:]...)
}

// writeSecretsFile writes KEY=VALUE pairs as a POSIX shell source file to
// <userHomeDir>/<random-name> (mode 0600). Returns both the host path (for
// defer-cleanup) and the path as it appears inside the sandbox (under sandboxHome).
func writeSecretsFile(userHomeDir, sandboxHome string, envVars []string) (hostPath, jailPath string, err error) {
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("writeSecretsFile rand: %w", err)
	}
	name := fmt.Sprintf(".sa-run-%x", b)
	hostPath = userHomeDir + "/" + name
	jailPath = sandboxHome + "/" + name

	var buf strings.Builder
	for _, kv := range envVars {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		key := kv[:idx]
		val := kv[idx+1:]
		// POSIX single-quote escaping: ' → '\''
		escapedVal := strings.ReplaceAll(val, "'", "'\\''")
		buf.WriteString(key + "='" + escapedVal + "'\n")
		buf.WriteString("export " + key + "\n")
	}

	if err = os.WriteFile(hostPath, []byte(buf.String()), 0o600); err != nil {
		return "", "", err
	}
	return hostPath, jailPath, nil
}

// shellJoin returns args as a single shell command string with each argument
// individually single-quoted so spaces and special chars are safe.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func capString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n[...output truncated]"
}
