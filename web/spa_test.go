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

func TestSPAHandlerServesAssetsAndFallsBack(t *testing.T) {
	e := echo.New()
	s := &Server{echo: e}
	h := s.spaHandler(spaTestFS(), true)

	cases := []struct {
		path     string
		wantBody string
	}{
		{"/app", "SPA-INDEX"},                // root → index
		{"/app/assets/app-x.js", "console"}, // real asset → served
		{"/app/agents/123", "SPA-INDEX"},    // client route → index fallback
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
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	if err := h(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d want 503", rec.Code)
	}
}

func TestSPARoutesRegistered(t *testing.T) {
	s, _ := newAPITestServer(t)
	have := map[string]bool{}
	for _, r := range s.echo.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, w := range []string{"GET /app", "GET /app/*"} {
		if !have[w] {
			t.Fatalf("missing route %s", w)
		}
	}
}
