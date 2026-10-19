package coder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
)

// fakeCLI writes a shell script that records its argv (one per line) and its
// stdin, then answers in the Claude JSON shape. Named by the caller, so a name
// containing "claude" auto-selects the Claude backend exactly as a real install.
func fakeCLI(t *testing.T, name string) (bin, argsFile, stdinFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake coder")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	stdinFile = filepath.Join(dir, "stdin")
	bin = filepath.Join(dir, name)
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"cat > " + stdinFile + "\n" +
		"printf '{\"result\":\"ok\",\"is_error\":false}\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile, stdinFile
}

// designerSizedPrompt is larger than Windows' 32,767-character command line and
// full of the double quotes Go escapes on Windows — the shape of the agent
// designer's ~41k system prompt that made its first turn fail there.
func designerSizedPrompt() string {
	return strings.Repeat(`Say "hello" to the user, then stop. `, 1500)
}

// The regression itself: a CLI coder's prompt must reach the binary on stdin and
// never as an argument, whatever its size. Passing it as `-p <prompt>` is what
// broke the designer on Windows.
func TestClaudePromptTravelsOnStdinNotTheCommandLine(t *testing.T) {
	bin, argsFile, stdinFile := fakeCLI(t, "claude")
	prompt := designerSizedPrompt()

	res, err := New(bin, time.Minute, t.TempDir(), t.TempDir()).
		WithReadOnlyTools().WithDir(t.TempDir()).
		Generate(context.Background(), "ws", prompt)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text != "ok" {
		t.Fatalf("text = %q, want ok", res.Text)
	}
	gotStdin, _ := os.ReadFile(stdinFile)
	if string(gotStdin) != prompt {
		t.Fatalf("stdin carried %d bytes, want the %d-byte prompt", len(gotStdin), len(prompt))
	}
	argv, _ := os.ReadFile(argsFile)
	if strings.Contains(string(argv), "hello") {
		t.Fatal("the prompt was passed on the command line as well as stdin")
	}
	if n := len(argv); n > 1000 {
		t.Fatalf("argv is %d bytes; the prompt must not be in it", n)
	}
}

// Chat flattens system context and history into the prompt; that whole string is
// what grows with a conversation, so it is the part that must use stdin.
func TestCLIChatHistoryTravelsOnStdin(t *testing.T) {
	bin, argsFile, stdinFile := fakeCLI(t, "claude")
	history := []db.ChatMessage{{Role: "user", Content: "first question"}, {Role: "assistant", Content: "first answer"}}

	if _, err := New(bin, time.Minute, t.TempDir(), t.TempDir()).WithNoTools().
		Chat(context.Background(), "ws", history, designerSizedPrompt(), "second question"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	gotStdin, _ := os.ReadFile(stdinFile)
	for _, want := range []string{"first answer", "second question", `Say "hello"`} {
		if !strings.Contains(string(gotStdin), want) {
			t.Errorf("stdin is missing %q", want)
		}
	}
	argv, _ := os.ReadFile(argsFile)
	if strings.Contains(string(argv), "second question") {
		t.Fatal("chat message was passed on the command line")
	}
}

// The flags are unchanged by moving the prompt: `-p` stays (it is print mode),
// the grant stays, and the prompt is simply absent.
func TestClaudeStdinArgsKeepEveryFlagButThePrompt(t *testing.T) {
	b := &claudeBackend{}
	got := b.stdinArgs(false, "Read,Glob")
	want := []string{"-p", "--output-format", "json", "--setting-sources", "", "--allowedTools", "Read,Glob"}
	if !slices.Equal(got, want) {
		t.Fatalf("stdinArgs = %q, want %q", got, want)
	}
	if full := b.buildArgs("PROMPT", false, "Read,Glob"); !slices.Equal(full, append([]string{"-p", "PROMPT"}, want[1:]...)) {
		t.Fatalf("buildArgs = %q", full)
	}
	if got := b.stdinArgs(true, ""); !slices.Equal(got[len(got)-2:], []string{"--allowedTools", ""}) {
		t.Fatalf("noTools must still emit an empty grant, got %q", got)
	}
}

// --setting-sources "" hides the model chosen inside Claude Code itself, so the
// workspace's Model field is the only way to pick one — and it used to be
// dropped for Claude while every other CLI received it.
func TestClaudeReceivesTheWorkspaceModel(t *testing.T) {
	bin, argsFile, _ := fakeCLI(t, "claude")
	w := &db.Workspace{ID: "ws", CoderKind: "local", CoderBin: bin, CoderModel: "opus"}
	c := ForWorkspace(w, t.TempDir(), t.TempDir(), nil, bin, time.Minute, false, true)
	if _, err := c.WithNoTools().Generate(context.Background(), "ws", "hi"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	argv, _ := os.ReadFile(argsFile)
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	i := slices.Index(lines, "--model")
	if i < 0 || i+1 >= len(lines) || lines[i+1] != "opus" {
		t.Fatalf("argv %q does not carry --model opus", lines)
	}

	if got := (&claudeBackend{}).stdinArgs(false, ""); slices.Contains(got, "--model") {
		t.Fatalf("an empty Model must leave Claude Code's default alone, got %q", got)
	}
}

// A process that never started has no output, which is exactly the Claude
// backend's usage-limit signature. Launch failures must be classified first, or
// "the binary is missing" and "Windows refused the command line" both read as
// "claude hit its usage limit".
func TestLaunchFailureIsNeverAUsageLimit(t *testing.T) {
	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "claude.exe")
	if err := os.Mkdir(notExecutable, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, bin := range map[string]string{
		"missing binary":    "claude-not-installed-anywhere",
		"cannot be started": notExecutable,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(bin, time.Minute, t.TempDir(), t.TempDir()).WithNoTools().
				Generate(context.Background(), "ws", "hi")
			if errors.Is(err, ErrUsageLimit) {
				t.Fatalf("launch failure reported as a usage limit: %v", err)
			}
			if !errors.Is(err, ErrCoderUnreachable) {
				t.Fatalf("err = %v, want ErrCoderUnreachable", err)
			}
		})
	}
}

// A coder that really ran and exited silently keeps the old classification —
// that empirical signature is why looksLikeLimit exists.
func TestSilentNonZeroExitIsStillAUsageLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake coder")
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := New(bin, time.Minute, t.TempDir(), t.TempDir()).WithNoTools().
		Generate(context.Background(), "ws", "hi")
	if !errors.Is(err, ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit", err)
	}
}

// A backend that still takes its prompt as an argument gets an explanation on
// Windows instead of an exec failure, and nothing changes anywhere else.
func TestCommandLineLengthGuard(t *testing.T) {
	long := []string{"-p", designerSizedPrompt()}
	if err := checkCommandLineLength("linux", "gemini", long); err != nil {
		t.Fatalf("guard must be a no-op off Windows: %v", err)
	}
	err := checkCommandLineLength("windows", `C:\tools\gemini.exe`, long)
	if err == nil || !strings.Contains(err.Error(), "too long") || !strings.Contains(err.Error(), "gemini.exe") {
		t.Fatalf("want a named too-long error on Windows, got %v", err)
	}
	if err := checkCommandLineLength("windows", `C:\tools\gemini.exe`, []string{"-p", "hi"}); err != nil {
		t.Fatalf("short prompt refused: %v", err)
	}
}

// windowsEscapeArg must agree with syscall.EscapeArg, or the guard misjudges how
// close a prompt is to the limit. These are the documented CommandLineToArgvW
// cases plus the empty argument Rookery passes for --setting-sources.
func TestWindowsEscapeArgMatchesGo(t *testing.T) {
	for in, want := range map[string]string{
		"":          `""`,
		"plain":     `plain`,
		"two words": `"two words"`,
		`a"b`:       `a\"b`,
		`a\b`:       `a\b`,
		`a b\`:      `"a b\\"`,
		`a\"b c`:    `"a\\\"b c"`,
	} {
		if got := windowsEscapeArg(in); got != want {
			t.Errorf("windowsEscapeArg(%q) = %q, want %q", in, got, want)
		}
	}
	if got := windowsCommandLineLen([]string{"a", "b c"}); got != len(`a "b c"`) {
		t.Errorf("windowsCommandLineLen = %d", got)
	}
}

// The bridge binary must be spelled so that Git Bash can run it verbatim and a
// grant can match it: the absolute backslash path does neither on Windows.
// Everywhere else it is the absolute path, exactly as before.
func TestBridgeBinFor(t *testing.T) {
	for _, tc := range []struct{ goos, exe, want string }{
		{"linux", "/usr/bin/rookery", "/usr/bin/rookery"},
		{"darwin", "/opt/homebrew/bin/rookery", "/opt/homebrew/bin/rookery"},
		{"windows", `C:\Users\Ilija Dimitrovski\AppData\Local\Programs\rookery\rookery.exe`, "rookery"},
		{"windows", `C:\tools\rookery.EXE`, "rookery"},
		{"windows", `C:\tools\my rookery.exe`, "C:/tools/my rookery.exe"},
	} {
		if got := bridgeBinFor(tc.goos, tc.exe); got != tc.want {
			t.Errorf("bridgeBinFor(%s, %q) = %q, want %q", tc.goos, tc.exe, got, tc.want)
		}
	}
}

func TestPrependPathDir(t *testing.T) {
	got := prependPathDir([]string{"A=1", `Path=C:\Windows`, "B=2"}, `C:\rk`, ";")
	want := []string{"A=1", `Path=C:\rk;C:\Windows`, "B=2"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := prependPathDir([]string{"A=1"}, `C:\rk`, ";"); !slices.Equal(got, []string{"A=1", `PATH=C:\rk`}) {
		t.Fatalf("missing PATH: got %q", got)
	}
}
