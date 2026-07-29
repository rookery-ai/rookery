//go:build linux

package sandbox_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/sandbox"
)

// TestLandlockBoundary verifies the core security property: a confined process
// can write only its granted RW dir, can read (but not write) a granted RO dir,
// and cannot read a path outside every grant. It re-executes the test binary as
// a helper (see TestSandboxHelperExec) which applies the sandbox to itself and
// then exec()s /bin/sh — mirroring exactly how the coder runs.
func TestLandlockBoundary(t *testing.T) {
	if !sandbox.Supported() {
		t.Skip("kernel has no Landlock support")
	}

	rw := t.TempDir()
	ro := t.TempDir()
	secretDir := t.TempDir() // outside every grant
	secret := secretDir + "/secret.txt"
	if err := os.WriteFile(secret, []byte("topsecret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ro+"/data.txt", []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `
if echo x > "$SB_RW/w" 2>/dev/null; then echo RW_WRITE_OK; else echo RW_WRITE_FAIL; fi
if cat "$SB_RO/data.txt" >/dev/null 2>&1; then echo RO_READ_OK; else echo RO_READ_FAIL; fi
if echo x > "$SB_RO/w" 2>/dev/null; then echo RO_WRITE_OK; else echo RO_WRITE_FAIL; fi
if cat "$SB_SECRET" >/dev/null 2>&1; then echo SECRET_READ_OK; else echo SECRET_READ_FAIL; fi
`
	spec := sandbox.Spec{
		Command:        []string{"/bin/sh", "-c", script},
		Dir:            rw,
		Env:            append(os.Environ(), "SB_RW="+rw, "SB_RO="+ro, "SB_SECRET="+secret),
		ReadWritePaths: []string{rw},
		ReadOnlyPaths:  append(sandbox.SystemReadOnlyPaths(), ro),
		ReadWriteFiles: sandbox.SystemReadWriteFiles(),
	}

	out := runSandboxHelper(t, spec)

	want := []string{"RW_WRITE_OK", "RO_READ_OK", "RO_WRITE_FAIL", "SECRET_READ_FAIL"}
	for _, m := range want {
		if !strings.Contains(out, m) {
			t.Errorf("expected %q in sandboxed output; got:\n%s", m, out)
		}
	}
	notWant := []string{"RW_WRITE_FAIL", "RO_READ_FAIL", "RO_WRITE_OK", "SECRET_READ_OK"}
	for _, m := range notWant {
		if strings.Contains(out, m) {
			t.Errorf("did not expect %q in sandboxed output; got:\n%s", m, out)
		}
	}
}

// TestLandlockNestedWorkdir mirrors the production layout: an agent's writable
// workdir nested inside a read-only vault root, with another user's data on a
// separate ungranted root. It asserts the agent can write its workdir, read the
// rest of its own vault, but cannot write elsewhere in the vault or read the
// other user's data — exactly the boundary coder.buildCommand relies on.
func TestLandlockNestedWorkdir(t *testing.T) {
	if !sandbox.Supported() {
		t.Skip("kernel has no Landlock support")
	}

	vault := t.TempDir()        // vaults/<user> — granted RO
	work := vault + "/agents/a" // the agent's own dir — granted RW (nested in RO vault)
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vault+"/note.md", []byte("own note"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherUser := t.TempDir() // a different user's vault — NOT granted
	if err := os.WriteFile(otherUser+"/secret.md", []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `
if echo x > "$SB_WORK/state.json" 2>/dev/null; then echo WORK_WRITE_OK; else echo WORK_WRITE_FAIL; fi
if cat "$SB_VAULT/note.md" >/dev/null 2>&1; then echo VAULT_READ_OK; else echo VAULT_READ_FAIL; fi
if echo x > "$SB_VAULT/note.md" 2>/dev/null; then echo VAULT_WRITE_OK; else echo VAULT_WRITE_FAIL; fi
if cat "$SB_OTHER/secret.md" >/dev/null 2>&1; then echo OTHER_READ_OK; else echo OTHER_READ_FAIL; fi
`
	spec := sandbox.Spec{
		Command:        []string{"/bin/sh", "-c", script},
		Dir:            work,
		Env:            append(os.Environ(), "SB_WORK="+work, "SB_VAULT="+vault, "SB_OTHER="+otherUser),
		ReadWritePaths: []string{work},
		ReadOnlyPaths:  append(sandbox.SystemReadOnlyPaths(), vault),
		ReadWriteFiles: sandbox.SystemReadWriteFiles(),
	}

	out := runSandboxHelper(t, spec)

	for _, m := range []string{"WORK_WRITE_OK", "VAULT_READ_OK", "VAULT_WRITE_FAIL", "OTHER_READ_FAIL"} {
		if !strings.Contains(out, m) {
			t.Errorf("expected %q in output; got:\n%s", m, out)
		}
	}
	for _, m := range []string{"WORK_WRITE_FAIL", "VAULT_READ_FAIL", "VAULT_WRITE_OK", "OTHER_READ_OK"} {
		if strings.Contains(out, m) {
			t.Errorf("did not expect %q in output; got:\n%s", m, out)
		}
	}
}

// runSandboxHelper re-executes this test binary so that TestSandboxHelperExec
// runs sandbox.Exec(spec) in the child and returns its combined output.
func runSandboxHelper(t *testing.T, spec sandbox.Spec) string {
	t.Helper()
	enc, err := sandbox.EncodeSpec(spec)
	if err != nil {
		t.Fatalf("encode spec: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSandboxHelperExec")
	cmd.Env = append(os.Environ(), "GO_SANDBOX_HELPER_SPEC="+enc)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// TestSandboxHelperExec is not a real test: when GO_SANDBOX_HELPER_SPEC is set it
// behaves as the sandbox helper (applies confinement and exec()s the command, so
// it never returns on success). When the env var is unset it is a no-op so a
// normal `go test` run passes.
func TestSandboxHelperExec(t *testing.T) {
	enc := os.Getenv("GO_SANDBOX_HELPER_SPEC")
	if enc == "" {
		return
	}
	spec, err := sandbox.DecodeSpec(enc)
	if err != nil {
		os.Stderr.WriteString("HELPER_DECODE_ERR: " + err.Error())
		os.Exit(1)
	}
	if err := sandbox.Exec(spec); err != nil {
		os.Stderr.WriteString("HELPER_EXEC_ERR: " + err.Error())
		os.Exit(1)
	}
}
