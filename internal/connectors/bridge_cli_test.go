package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/sandbox"
)

// TestConnectorExecSubcommandEndToEnd exercises the REAL `simple-agents connector exec`
// CLI path a CLI coder uses: build the binary, stand up a bridge against a fake provider,
// and invoke the subcommand with the run-scoped env the runner injects. Skips if the
// binary isn't built.
func TestConnectorExecSubcommandEndToEnd(t *testing.T) {
	bin, err := filepath.Abs("../../bin/simple-agents")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("binary not built at bin/simple-agents; run `go build -o bin/simple-agents ./cmd/simple-agents`")
	}

	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"messages":[{"id":"E2E-OK"}]}`))
	}))
	defer prov.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = prov.URL + "/messages"
	reg.SetActionsForTest("google", []Action{a})

	br := NewBridge(reg, fakeStore{tok: "AT"}, prov.Client())
	addr, err := br.Start(context.Background())
	if err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	tok := br.Register("ws1", []BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}}, false)

	cmd := exec.Command(bin, "connector", "exec", "gmail_search", "--args", `{"query":"hi"}`)
	cmd.Env = append(os.Environ(), "ROOKERY_CONNECTOR_URL="+addr, "ROOKERY_CONNECTOR_TOKEN="+tok)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subcommand failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "E2E-OK") {
		t.Fatalf("subcommand did not return provider data through the bridge; got: %s", out)
	}
}

// TestConnectorExecUnderSandbox is the load-bearing check the advisor flagged: a CLI coder
// runs SANDBOXED (Landlock), so the connector-exec binary must be both executable under the
// ruleset AND able to reach the loopback bridge. This wraps the real subcommand through the
// same sandbox helper a run uses, granting the binary dir RO+exec, and confirms it works.
func TestConnectorExecUnderSandbox(t *testing.T) {
	if !sandbox.Supported() {
		t.Skip("Landlock not supported on this kernel")
	}
	bin, _ := filepath.Abs("../../bin/simple-agents")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("binary not built at bin/simple-agents")
	}

	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"messages":[{"id":"SANDBOX-OK"}]}`))
	}))
	defer prov.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = prov.URL + "/messages"
	reg.SetActionsForTest("google", []Action{a})

	br := NewBridge(reg, fakeStore{tok: "AT"}, prov.Client())
	addr, _ := br.Start(context.Background())
	tok := br.Register("ws1", []BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}}, false)

	// Grant the binary's own dir RO+exec (mirrors coder.sandboxReadOnlyPaths) plus system
	// paths, and run the real subcommand through the Landlock helper.
	ro := append(sandbox.SystemReadOnlyPaths(), filepath.Dir(bin))
	spec := sandbox.Spec{
		Command:       []string{bin, "connector", "exec", "gmail_search", "--args", `{"query":"hi"}`},
		Dir:           t.TempDir(),
		Env:           append(os.Environ(), "ROOKERY_CONNECTOR_URL="+addr, "ROOKERY_CONNECTOR_TOKEN="+tok),
		ReadOnlyPaths: ro,
	}
	wargv, err := sandbox.Wrap(bin, spec)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	out, err := exec.Command(wargv[0], wargv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed subcommand failed (exec denied or bridge unreachable): %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "SANDBOX-OK") {
		t.Fatalf("sandboxed subcommand did not reach the bridge; got: %s", out)
	}
}
