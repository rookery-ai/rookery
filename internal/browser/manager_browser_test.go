//go:build browser

// These tests need a real Chromium and are excluded from the normal run, the
// same way the livecheck tests are: CI must not depend on a ~500 MB download.
// Run them with:
//
//	go test -tags browser ./internal/browser/
package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildRookery builds the real binary, because the helper is a subcommand OF
// that binary — the test binary does not answer __browser-host, so pointing the
// manager at os.Executable() would exercise nothing.
func buildRookery(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rookery")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/rookery")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build rookery: %v\n%s", err, out)
	}
	return bin
}

// jsPage serves a page whose content exists only after JavaScript runs — the
// exact shape web_fetch cannot read and this whole subsystem exists for.
func jsPage(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<div id="out">loading…</div>
<button id="go">Continue</button>
<input type="password" id="pw">
<script>
  document.getElementById("out").textContent = "RENDERED-BY-JAVASCRIPT";
</script>
</body></html>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The guard is disabled here ONLY because httptest binds to loopback, which the
// guard refuses by design. The companion test below asserts that refusal.
func TestBrowserRendersJavaScriptContent(t *testing.T) {
	srv := jsPage(t)
	m := NewManager(buildRookery(t), true, false)
	defer m.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := m.Render(ctx, Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(res.Text, "RENDERED-BY-JAVASCRIPT") {
		t.Fatalf("JS-rendered content missing; got %q", res.Text)
	}
	// A plain HTTP fetch would have returned the placeholder instead.
	if strings.Contains(res.Text, "loading…") {
		t.Error("returned the pre-render placeholder — JS did not run")
	}
}

// The security property. A page must not be able to reach this install's own
// loopback bridges and their per-run bearer tokens.
//
// It asserts the BEHAVIOUR rather than the presence of --proxy-bypass-list,
// deliberately: that flag was measured to be redundant today (Playwright already
// routes loopback through the proxy), so a test checking the flag would pass
// even if the actual protection disappeared.
func TestBrowserCannotReachLoopbackWhenGuarded(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte("BRIDGE-TOKEN"))
	}))
	defer srv.Close()

	m := NewManager(buildRookery(t), true, true) // guarded
	defer m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := m.Render(ctx, Request{URL: srv.URL})
	if err == nil && strings.Contains(res.Text, "BRIDGE-TOKEN") {
		t.Fatal("the browser reached a loopback address through the guard")
	}
	if reached {
		t.Fatal("the loopback server was contacted despite the guard")
	}
}

// A login wall must be reported as one, so the user is told the actionable
// thing (store credentials) rather than "the page was empty".
func TestBrowserClassifiesAPasswordFormAsALoginWall(t *testing.T) {
	srv := jsPage(t)
	m := NewManager(buildRookery(t), true, false)
	defer m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := m.Render(ctx, Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if res.Blocked != "login" {
		t.Fatalf("blocked = %q, want login", res.Blocked)
	}
}

// Acting needs a live session, and the element list is what makes it usable.
func TestBrowserListsInteractiveElementsInASession(t *testing.T) {
	srv := jsPage(t)
	m := NewManager(buildRookery(t), true, false)
	defer m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const key = "test-session"
	res, err := m.Render(ctx, Request{URL: srv.URL, Session: key})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	defer m.CloseSession(ctx, key)

	var found bool
	for _, e := range res.Elements {
		if e.Role == "button" && e.Name == "Continue" {
			found = true
			if e.Ref == "" {
				t.Error("button offered without a ref — nothing could click it")
			}
		}
	}
	if !found {
		t.Fatalf("Continue button not in element list: %+v", res.Elements)
	}
}
