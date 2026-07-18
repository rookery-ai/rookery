package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v4"
)

func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<html>SPA-INDEX</html>")},
		"assets/app-x.js": {Data: []byte("console.log(1)")},
	}
}

// spaHandler now serves the SPA at the site root (no /app prefix): real assets
// by path, index.html fallback for client-side routes.
func TestSPAHandlerServesAssetsAndFallsBack(t *testing.T) {
	e := echo.New()
	s := &Server{echo: e}
	h := s.spaHandler(spaTestFS(), true)

	cases := []struct {
		path     string
		wantBody string
	}{
		{"/", "SPA-INDEX"},              // root → index
		{"/assets/app-x.js", "console"}, // real asset → served
		{"/kb", "SPA-INDEX"},            // client route → index fallback
		{"/agents/123", "SPA-INDEX"},    // unknown-to-backend deep route → index
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := h(c); err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Fatalf("%s: got %d %q", tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestSPAHandlerNotBuilt(t *testing.T) {
	e := echo.New()
	s := &Server{echo: e}
	h := s.spaHandler(nil, false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	if err := h(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d want 503", rec.Code)
	}
}

// redirectAppPath 301s legacy /app paths to their /app-stripped equivalents,
// preserving the query string. It is a pure path rewrite — independent of
// whether the UI is built — so it's unit-tested directly.
func TestRedirectAppPath(t *testing.T) {
	e := echo.New()
	cases := []struct {
		path, wantLoc string
	}{
		{"/app", "/"},
		{"/app/agents", "/agents"},
		{"/app/kb?path=z", "/kb?path=z"},
		{"/app/kb?x=1&y=2", "/kb?x=1&y=2"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := redirectAppPath(c); err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("%s: got %d want 301", tc.path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != tc.wantLoc {
			t.Fatalf("%s: Location %q want %q", tc.path, loc, tc.wantLoc)
		}
	}
}

// TestSPARoutesRegistered proves the ordering-sensitive routes exist: the /app
// legacy paths (301 redirects) and the root catch-all. All are dist-agnostic.
func TestSPARoutesRegistered(t *testing.T) {
	s, _ := newAPITestServer(t)
	have := map[string]bool{}
	for _, r := range s.echo.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, w := range []string{"GET /app", "GET /app/*", "GET /*"} {
		if !have[w] {
			t.Fatalf("missing route %s", w)
		}
	}
	// The OAuth callback path is a registered redirect URI in external provider
	// consoles — it must remain an explicit route (never shadowed by the /*
	// catch-all). Constraint (e).
	if !have["GET /dashboard/connectors/services/callback/:provider"] {
		t.Fatalf("OAuth callback route missing/renamed — external redirect URIs would break")
	}
}

// TestSPACatchAllPrecedence drives the full Echo router (via ServeHTTP) to prove
// the last-registered GET /* catch-all does NOT shadow explicit routes, and that
// legacy /app paths 301. Every assertion here keys off a route's own signature,
// not the SPA index / 503 (which depend on whether `make ui` ran), so it holds
// in every build environment.
func TestSPACatchAllPrecedence(t *testing.T) {
	s, _ := newAPITestServer(t)

	// (c) legacy /app paths 301 to the stripped path, preserving query.
	for _, tc := range []struct{ path, wantLoc string }{
		{"/app", "/"},
		{"/app/agents", "/agents"},
		{"/app/kb?path=z", "/kb?path=z"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, req)
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("%s: got %d want 301", tc.path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != tc.wantLoc {
			t.Fatalf("%s: Location %q want %q", tc.path, loc, tc.wantLoc)
		}
	}

	// (d) a registered API route still hits its own handler (JSON), NOT the
	// catch-all. The session endpoint is unauthenticated → 200 {authenticated:false}.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "authenticated") {
			t.Fatalf("/api/v1/auth/session hit catch-all instead of handler: %d %q", rec.Code, rec.Body.String())
		}
	}

	// (d′) an UNREGISTERED /api/v1/* path must NOT backtrack into the SPA
	// catch-all and return index.html — Echo can fall an unmatched sub-path to
	// an ancestor * route, so assert the API namespace stays JSON (here: the
	// group's auth middleware answers 401 JSON), never SPA HTML.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/__nope__", nil)
		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, req)
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") || strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("/api/v1/* leaked into the SPA catch-all: %d ct=%q body=%.60q", rec.Code, ct, rec.Body.String())
		}
	}

	// (f) the template UI is gone — /login now falls through to the SPA
	// catch-all and serves index.html (or 503 when the UI isn't built). The old
	// login template that used to win this exact path no longer exists.
	{
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("/login did not reach the SPA handler (got %d) — expected SPA index or 503", rec.Code)
		}
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "Sign in") {
			t.Fatalf("/login still served a login template heading — template UI should be deleted: %q", rec.Body.String())
		}
	}

	// Root (/) is the SPA now, not the old template redirect (302 → /login).
	// Echo's /* param route does NOT match bare "/", so GET / is registered
	// explicitly → spaHandler: 200 (index) when the UI is built, 503 when not.
	// Assert it reached the SPA handler positively (dist-agnostic) — this pins
	// the exact bare-root wiring bug that produced the stray 302.
	{
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET / did not reach the SPA handler (got %d) — root wiring regressed", rec.Code)
		}
	}
}
