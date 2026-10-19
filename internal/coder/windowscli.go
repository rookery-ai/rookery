package coder

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unicode/utf16"
)

// Windows-specific pieces of driving a CLI coder. They are pure functions of a
// GOOS string rather than build-tagged files, because there is no Windows host
// to run tests on: written this way, every branch is exercised on Linux, and a
// non-Windows caller gets exactly the behaviour it had before.

// windowsMaxCommandLine is CreateProcess's limit on the whole command line, in
// UTF-16 code units, terminating NUL included.
const windowsMaxCommandLine = 32767

// checkCommandLineLength refuses an invocation whose command line Windows would
// reject. Without it the exec fails with "The filename or extension is too
// long", which used to be reported as a usage limit and is still a sentence
// that names neither the prompt nor the fix. It is a no-op off Windows.
//
// Only backends that cannot take the prompt on stdin reach this (see
// stdinPrompter), so the message points at the ones that can.
func checkCommandLineLength(goos, bin string, args []string) error {
	if goos != "windows" {
		return nil
	}
	n := windowsCommandLineLen(append([]string{bin}, args...))
	if n < windowsMaxCommandLine {
		return nil
	}
	return fmt.Errorf("the prompt is too long to pass to %q on Windows: its command line would be %d characters and Windows allows %d. "+
		"This coder takes its prompt as a command-line argument; Claude Code reads it from standard input and has no such limit",
		baseName(bin), n, windowsMaxCommandLine-1)
}

// windowsCommandLineLen is the length, in UTF-16 units, of the command line Go
// builds for argv on Windows: each argument escaped by syscall.EscapeArg,
// joined by single spaces.
func windowsCommandLineLen(argv []string) int {
	n := 0
	for i, a := range argv {
		if i > 0 {
			n++
		}
		n += windowsEscapedLen(a)
	}
	return n
}

// windowsEscapedLen is the UTF-16 length of syscall.EscapeArg(s) (which exists
// only on Windows), so the total above is the one CreateProcess will actually
// see — including the backslash Go adds before every double quote, of which a
// prompt has many.
//
// It counts rather than builds the escaped string on purpose: only the length
// is ever needed, and constructing a quoted copy of the prompt is exactly the
// pattern code scanning flags as unsafe quoting.
func windowsEscapedLen(s string) int {
	if s == "" {
		return 2 // ""
	}
	n := len(utf16.Encode([]rune(s)))
	needsBackslash, hasSpace := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			needsBackslash = true
		case ' ', '\t':
			hasSpace = true
		}
	}
	if !hasSpace && !needsBackslash {
		return n
	}
	if hasSpace {
		n += 2 // surrounding quotes
	}
	if !needsBackslash {
		return n
	}
	// A run of backslashes is doubled when a quote follows it, and the quote
	// itself gains one; with surrounding quotes, a trailing run is doubled too.
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			slashes++
		case '"':
			n += slashes + 1
			slashes = 0
		default:
			slashes = 0
		}
	}
	if hasSpace {
		n += slashes
	}
	return n
}

// BridgeBin is how a CLI coder should spell the rookery binary when it runs
// `<bin> connector exec …`, `<bin> kb …`, `<bin> mcp exec …` or
// `<bin> browser read …`. The same string goes into the prompt AND into the
// `Bash(<bin> …:*)` grant, so the two can never disagree. Empty means
// os.Executable failed, and callers fall back to the bare name.
func BridgeBin() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return bridgeBinFor(runtime.GOOS, p)
}

// bridgeBinFor returns exe unchanged off Windows. On Windows it returns the bare
// command name, because the absolute path is unusable there in two ways:
// Claude Code's Bash tool runs Git Bash, where the unquoted backslashes in
// `C:\Users\…\rookery.exe` are escape characters and the command the model
// types verbatim names a file that does not exist; and a model that quotes the
// path or flips the slashes to repair it produces a command that no longer
// prefix-matches the grant, which a non-interactive run denies silently. A user
// folder with a space in it breaks a forward-slash path too. The bare name has
// none of these problems; buildEnv puts the binary's directory on PATH so it
// resolves.
//
// A name that is itself unsafe for a shell falls back to the forward-slash
// path, which is still strictly better than the backslash one.
func bridgeBinFor(goos, exe string) string {
	if goos != "windows" {
		return exe
	}
	name := baseName(exe)
	if i := strings.LastIndexByte(name, '.'); i > 0 && strings.EqualFold(name[i:], ".exe") {
		name = name[:i]
	}
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "" {
		return strings.ReplaceAll(exe, `\`, "/")
	}
	return name
}

// baseName is filepath.Base for either separator, so a Windows path can be
// handled correctly by code running on Linux.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// prependPathDir puts dir first on the PATH in env. The key is matched without
// regard to case because Windows spells it "Path", and a second, differently
// cased entry would leave which one the child sees up to the C runtime.
func prependPathDir(env []string, dir, sep string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && !found && strings.EqualFold(k, "PATH") {
			found = true
			if v == "" {
				kv = k + "=" + dir
			} else {
				kv = k + "=" + dir + sep + v
			}
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}
