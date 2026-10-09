package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractNumericSegments(t *testing.T) {
	raw := []byte(`{"replies":[{"replaceAllText":{"occurrencesChanged":3}},{}]}`)
	v, ok := extractOK("$.replies.0.replaceAllText.occurrencesChanged", raw)
	if !ok || string(v) != "3" {
		t.Fatalf("got %s %v", v, ok)
	}
	if _, ok := extractOK("$.replies.5.x", raw); ok {
		t.Fatal("out-of-range index must not resolve")
	}
	if _, ok := extractOK("$.replies.1.replaceAllText", raw); ok {
		t.Fatal("missing key must not resolve")
	}
}

// A replace that matched nothing answers 200 with the count OMITTED (proto3 JSON drops
// zeros). Reading that as success is how a small model reports an edit that never
// happened, so Execute must turn it into an error naming what to do next.
func TestExpectChangeZeroIsFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"absent", `{"replies":[{"replaceAllText":{}}]}`, true},
		{"zero", `{"replies":[{"replaceAllText":{"occurrencesChanged":0}}]}`, true},
		{"three", `{"replies":[{"replaceAllText":{"occurrencesChanged":3}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			reg := testRegistry(t)
			a, ok := reg.Action("google_slides", "slides_replace_all_text")
			if !ok {
				t.Fatal("action missing")
			}
			if a.ExpectChange == "" {
				t.Fatal("slides_replace_all_text must declare expect_change")
			}
			a.Request.URL = srv.URL
			reg.actions["google_slides"] = []Action{a}
			_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
				ConnRef{ID: "c", Provider: "google_slides"}, "slides_replace_all_text",
				map[string]any{"presentation_id": "p", "find": "x", "replacement": "y"}, Policy{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "nothing changed") {
				t.Fatalf("error must say nothing changed: %v", err)
			}
		})
	}
}

func TestFindReplaceActionsDeclareExpectChange(t *testing.T) {
	reg := testRegistry(t)
	for _, pa := range [][2]string{{"google_slides", "slides_replace_all_text"}, {"google_sheets", "sheets_find_replace"}} {
		a, ok := reg.Action(pa[0], pa[1])
		if !ok || a.ExpectChange == "" {
			t.Errorf("%s.%s must declare expect_change", pa[0], pa[1])
		}
	}
}
