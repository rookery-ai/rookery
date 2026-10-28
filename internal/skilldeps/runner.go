package skilldeps

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/sandbox"
)

// pypiIndex is the only index pip is ever pointed at.
const pypiIndex = "https://pypi.org/simple"

// defaultInstallTimeout bounds one install. It is independent of the caller's deadline
// so a stuck resolve fails on its own instead of silently eating a whole run's budget.
const defaultInstallTimeout = 15 * time.Minute

// maxOutput caps the pip/script output returned to a model; the tail is kept, since
// that is where pip reports what went wrong.
const maxOutput = 8 << 10

// Runner installs into and runs from environments. The zero value is usable: no
// sandbox, host python3, and no malware check.
type Runner struct {
	SelfExe        string
	Sandbox        bool
	Python         string // "" → python3 on PATH
	Checker        MalwareChecker
	InstallTimeout time.Duration
}

// InstallRequest names what to install where.
type InstallRequest struct {
	Home, Env, Agent string
	Specs            []string
}

// InstallResult reports a completed install.
type InstallResult struct {
	Env          string
	Resolved     []Package
	MalwareCheck string
	Output       string
}

// RunRequest names a script to run with an environment's interpreter.
type RunRequest struct {
	Home, Env, WorkDir, VaultRoot, Script, Stdin string
	Args                                         []string
	Secrets                                      map[string]string
}

func (r Runner) python() (string, error) {
	if r.Python != "" {
		return r.Python, nil
	}
	p, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("python3 is not installed on this host")
	}
	return p, nil
}

// exec runs spec, confined when the sandbox is on and available, otherwise directly
// with the same scrubbed environment.
func (r Runner) exec(ctx context.Context, spec sandbox.Spec, stdin string) (string, string, error) {
	var cmd *exec.Cmd
	if r.Sandbox && r.SelfExe != "" && sandbox.Supported() {
		if argv, err := sandbox.Wrap(r.SelfExe, spec); err == nil {
			cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		}
	}
	if cmd == nil {
		cmd = exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	}
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.WaitDelay = 5 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// ensureVenv creates envDir's virtual environment when it has none.
func (r Runner) ensureVenv(ctx context.Context, envDir, python string) error {
	if _, err := os.Stat(filepath.Join(envDir, "bin", "python")); err == nil {
		return nil
	}
	for _, d := range []string{envDir, filepath.Join(envDir, ".tmp"), filepath.Join(envDir, ".cache")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	_, stderr, err := r.exec(ctx, installSpec(envDir, []string{python, "-m", "venv", envDir}, python, ""), "")
	if err != nil {
		return fmt.Errorf("create environment: %w: %s", err, tail(stderr))
	}
	return nil
}

// Install validates every requirement, resolves the full dependency set, refuses it if
// any resolved package is known to be malicious, installs it and records the install.
func (r Runner) Install(ctx context.Context, req InstallRequest) (InstallResult, error) {
	envDir, err := EnvDir(req.Home, req.Env)
	if err != nil {
		return InstallResult{}, err
	}
	if len(req.Specs) == 0 {
		return InstallResult{}, errors.New("name at least one package to install")
	}
	for _, s := range req.Specs {
		if err := ValidateRequirement(s); err != nil {
			return InstallResult{}, err
		}
	}
	python, err := r.python()
	if err != nil {
		return InstallResult{}, err
	}
	timeout := r.InstallTimeout
	if timeout == 0 {
		timeout = defaultInstallTimeout
	}
	// Own deadline, but the caller's CANCEL still stops it (a cancelled build must stay
	// cancelled); only the caller's deadline is not inherited.
	parent := ctx
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	stop := context.AfterFunc(parent, func() {
		if errors.Is(parent.Err(), context.Canceled) {
			cancel()
		}
	})
	defer stop()

	// One install at a time per environment: two concurrent pip runs corrupt a venv,
	// and AppendInstall is read-modify-write.
	unlock, err := lockEnv(envDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("lock environment: %w", err)
	}
	defer unlock()

	if err := r.ensureVenv(ctx, envDir, python); err != nil {
		return InstallResult{}, err
	}
	git, _ := exec.LookPath("git")
	envPython := filepath.Join(envDir, "bin", "python")
	pip := []string{envPython, "-m", "pip", "install", "--index-url", pypiIndex}

	// Per-call name as well as the lock: the malware check must read the report THIS
	// resolve wrote, whatever else shares the environment.
	report := filepath.Join(envDir, ".tmp", "resolve-"+randomHex()+".json")
	defer os.Remove(report)
	dry := append(append(append([]string{}, pip...), "--dry-run", "--ignore-installed", "--report", report), req.Specs...)
	if _, stderr, err := r.exec(ctx, installSpec(envDir, dry, python, git), ""); err != nil {
		return InstallResult{}, fmt.Errorf("resolve %s: %w: %s", strings.Join(req.Specs, " "), timeoutAware(ctx, err), tail(stderr))
	}
	resolved, err := readReport(report)
	if err != nil {
		return InstallResult{}, err
	}

	check := "clean"
	if r.Checker == nil {
		check = "skipped: no malware checker configured"
	} else if findings, err := r.Checker.Malicious(ctx, resolved); err != nil {
		check = "skipped: " + err.Error()
	} else if len(findings) > 0 {
		f := findings[0]
		return InstallResult{}, fmt.Errorf("refused: %s %s is flagged as malicious (%s); nothing was installed", f.Package.Name, f.Package.Version, f.ID)
	}

	stdout, stderr, err := r.exec(ctx, installSpec(envDir, append(append([]string{}, pip...), req.Specs...), python, git), "")
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: %w: %s", strings.Join(req.Specs, " "), timeoutAware(ctx, err), tail(stderr))
	}
	if err := AppendInstall(envDir, InstallRecord{At: time.Now().UTC(), Agent: req.Agent, Requested: req.Specs, Resolved: resolved, MalwareCheck: check}); err != nil {
		return InstallResult{}, fmt.Errorf("record install: %w", err)
	}
	return InstallResult{Env: req.Env, Resolved: resolved, MalwareCheck: check, Output: tail(stdout)}, nil
}

// Run executes req.Script with the environment's interpreter, passing exactly the named
// secrets and a private TMPDIR that is removed afterwards. A missing environment is
// created bare, so a script can run (e.g. to show --help) before anything is installed.
func (r Runner) Run(ctx context.Context, req RunRequest) (string, string, error) {
	envDir, err := EnvDir(req.Home, req.Env)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(req.Script) == "" {
		return "", "", errors.New("name the script to run")
	}
	python, err := r.python()
	if err != nil {
		return "", "", err
	}
	// Creating the venv takes the install lock so a run cannot race an install into
	// a half-built environment; the run itself does not hold it.
	unlock, err := lockEnv(envDir)
	if err != nil {
		return "", "", fmt.Errorf("lock environment: %w", err)
	}
	err = r.ensureVenv(ctx, envDir, python)
	unlock()
	if err != nil {
		return "", "", err
	}
	tmp := filepath.Join(envDir, ".tmp", randomHex())
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	env := []string{
		"PATH=" + filepath.Join(envDir, "bin") + ":/usr/bin:/bin",
		"HOME=" + envDir,
		"TMPDIR=" + tmp,
		"VIRTUAL_ENV=" + envDir,
		"LANG=C.UTF-8",
	}
	// The knowledge base root is a path, not a secret. Without it a script searched for
	// the nearest notes/ folder and found the agent's own agents/<id>/notes/.
	if req.VaultRoot != "" {
		env = append(env, "ROOKERY_VAULT_ROOT="+req.VaultRoot)
	}
	names := make([]string, 0, len(req.Secrets))
	for k := range req.Secrets {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		env = append(env, k+"="+req.Secrets[k])
	}
	envPython := filepath.Join(envDir, "bin", "python")
	cmd := append([]string{envPython, req.Script}, req.Args...)
	stdout, stderr, err := r.exec(ctx, runSpec(envDir, req.WorkDir, req.VaultRoot, cmd, env, python), req.Stdin)
	return tail(stdout), tail(stderr), err
}

// readReport extracts name/version pairs from pip's --report JSON.
func readReport(path string) ([]Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read resolve report: %w", err)
	}
	var rep struct {
		Install []struct {
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("parse resolve report: %w", err)
	}
	out := make([]Package, 0, len(rep.Install))
	for _, i := range rep.Install {
		out = append(out, Package{Name: i.Metadata.Name, Version: i.Metadata.Version})
	}
	return out, nil
}

func timeoutAware(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out")
	}
	return err
}

func tail(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return "…" + s[len(s)-maxOutput:]
}

func randomHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
