package skilldeps

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOSVReportsOnlyMaliciousAdvisories(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/querybatch" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
			map[string]any{"vulns": []any{map[string]any{"id": "MAL-2024-1234"}}},
			map[string]any{"vulns": []any{map[string]any{"id": "GHSA-xxxx-yyyy"}}},
			map[string]any{},
		}})
	}))
	defer srv.Close()

	got, err := NewOSVClient(srv.URL, nil).Malicious(context.Background(), []Package{{"evilpkg", "1.0"}, {"requests", "2.0"}, {"rich", "14.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Package.Name != "evilpkg" || got[0].ID != "MAL-2024-1234" {
		t.Fatalf("findings = %+v, want only evilpkg MAL-2024-1234", got)
	}
	if !strings.Contains(body, `"ecosystem":"PyPI"`) {
		t.Errorf("query must name the PyPI ecosystem, body: %s", body)
	}
}

func TestOSVServerErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := NewOSVClient(srv.URL, nil).Malicious(context.Background(), []Package{{"x", "1"}}); err == nil {
		t.Fatal("a 503 must be reported, so the installer can record the check as skipped")
	}
}
