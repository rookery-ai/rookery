package vault

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestKBCLIRoundTrip exercises the REAL `rookery kb convert|search` CLI
// path a CLI coder uses: build the binary, stand up a bridge, and invoke both
// subcommands against it with the run-scoped env the runner injects (mirrors
// connectors/bridge_cli_test.go's TestConnectorExecSubcommandEndToEnd, the
// proven pattern this test follows). Skips gracefully if the build fails
// (e.g. no network/module cache in a constrained sandbox) rather than failing
// the suite for an unrelated reason.
func TestKBCLIRoundTrip(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "rookery")
	build := exec.Command("go", "build", "-o", bin, "./cmd/rookery")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("could not build rookery binary, skipping CLI round-trip: %v\n%s", err, out)
	}

	v := New(t.TempDir())
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b := NewBridge(v)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if _, err := b.Start(ctx); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	defer b.Close()
	env := append(os.Environ(), "ROOKERY_KB_URL="+b.URL(), "ROOKERY_KB_TOKEN="+b.Register("ws1", false))

	doc := filepath.Join(t.TempDir(), "report.csv")
	const marker = "zq-unicorn-marker-42"
	if err := os.WriteFile(doc, []byte("marker\n"+marker+"\n"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	convOut, err := run(bin, env, "kb", "convert", doc)
	if err != nil {
		t.Fatalf("kb convert failed: %v\n%s", err, convOut)
	}
	if !strings.Contains(convOut, "note_path") {
		t.Fatalf("kb convert did not report a note_path: %s", convOut)
	}

	searchOut, err := run(bin, env, "kb", "search", marker)
	if err != nil {
		t.Fatalf("kb search failed: %v\n%s", err, searchOut)
	}
	if !strings.Contains(searchOut, marker) {
		t.Fatalf("kb search did not find the converted note: %s", searchOut)
	}
}

func run(bin string, env []string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}
