// Package sandbox wraps Firejail for isolated Python agent execution.
// If Firejail is not available (e.g. in CI), Run falls back to a plain
// exec with a timeout — callers should not rely on isolation in that case.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTimeout     = 60 * time.Second
	firejailBin        = "firejail"
	maxOutputBytes     = 1 << 20 // 1 MB cap on stdout+stderr
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
	Timeout    time.Duration // 0 → defaultTimeout
	Env        []string      // extra KEY=VALUE pairs injected into the child
	WorkDir    string        // working directory for the script
	AllowNet   bool          // if false, --net=none is added
}

// Run executes script (a path to a Python file) inside Firejail and returns
// the combined stdout/stderr output plus exit code.
// The env slice is injected as-is (no shell expansion — safe for secrets).
func Run(ctx context.Context, scriptPath string, opts RunOptions) (*Result, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	var cmd *exec.Cmd
	if Available() {
		cmd = buildFirejailCmd(ctx, scriptPath, timeout, opts)
	} else {
		cmd = buildFallbackCmd(ctx, scriptPath)
	}

	// Inject caller-supplied environment variables (secrets proxy output).
	cmd.Env = append(os.Environ(), opts.Env...)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Enforce output cap
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

// Available returns true if the firejail binary is in PATH.
func Available() bool {
	_, err := exec.LookPath(firejailBin)
	return err == nil
}

// ─── Internal ─────────────────────────────────────────────────────────────────

func buildFirejailCmd(ctx context.Context, scriptPath string, timeout time.Duration, opts RunOptions) *exec.Cmd {
	// Format timeout as HH:MM:SS for firejail --timeout flag.
	h := int(timeout.Hours())
	m := int(timeout.Minutes()) % 60
	s := int(timeout.Seconds()) % 60
	timeoutStr := fmt.Sprintf("%02d:%02d:%02d", h, m, s)

	args := []string{
		"--quiet",                          // suppress firejail banner/warnings
		"--private",                        // private /home
		"--private-tmp",                    // private /tmp
		"--noroot",                         // no setuid root inside sandbox
		"--caps.drop=all",                  // drop all Linux capabilities
		"--seccomp",                        // seccomp filter (default policy)
		"--timeout=" + timeoutStr,
	}
	if !opts.AllowNet {
		args = append(args, "--net=none")
	}
	args = append(args, "python3", scriptPath)

	return exec.CommandContext(ctx, firejailBin, args...)
}

func buildFallbackCmd(ctx context.Context, scriptPath string) *exec.Cmd {
	return exec.CommandContext(ctx, "python3", scriptPath)
}

func capString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n[...output truncated]"
}

// ParseChatLines extracts [CHAT] lines from agent stdout.
// Agents write chat output as: print("[CHAT] message here")
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
