package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// End-to-end check of the gate against the REAL bundled registry and the real
// linkedin_create_post / youtube_post_comment actions — the other park tests use a
// synthetic fixture, so this is what proves the shipped data files carry the flag
// and that a read action alongside them is untouched.
func TestGateAgainstRealRegistry(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	ctx := context.Background()

	for _, tc := range []struct {
		provider, action string
		args             map[string]any
	}{
		{"linkedin", "linkedin_create_post", map[string]any{"person_id": "ABC", "text": "hello"}},
		{"youtube", "youtube_post_comment", map[string]any{"video_id": "v1", "text": "nice"}},
	} {
		fp := &fakeParker{}
		res, err := Execute(ctx, reg, panicStore{t}, &http.Client{},
			ConnRef{ID: "conn-1", Provider: tc.provider}, tc.action, tc.args,
			Policy{Parker: fp})
		if err != nil {
			t.Fatalf("%s: parked call must succeed, got %v", tc.action, err)
		}
		if fp.calls != 1 {
			t.Errorf("%s: Park called %d times, want 1", tc.action, fp.calls)
		}
		var pr ParkedResult
		if err := json.Unmarshal(res.Data, &pr); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		if pr.Status != "queued_for_approval" {
			t.Errorf("%s: status = %q", tc.action, pr.Status)
		}
		if !strings.Contains(strings.ToLower(pr.Note), "not yet published") {
			t.Errorf("%s: note must not read as success: %q", tc.action, pr.Note)
		}
	}

	// A read action on a gated provider must go straight through — the gate keys off
	// the ACTION, not the connection.
	fp := &fakeParker{}
	_, err = Execute(ctx, reg, failingStore{}, &http.Client{},
		ConnRef{ID: "conn-1", Provider: "linkedin"}, "linkedin_me",
		map[string]any{}, Policy{Parker: fp})
	if err == nil {
		t.Fatal("expected linkedin_me to reach the token fetch")
	}
	if fp.calls != 0 {
		t.Error("a read action must never be parked")
	}
}
