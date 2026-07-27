package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fakeParker records what it was asked to park and returns a fixed ticket.
type fakeParker struct {
	calls  int
	action string
	args   map[string]any
	err    error
}

func (f *fakeParker) Park(_ context.Context, _ ConnRef, action string, args map[string]any) (string, error) {
	f.calls++
	f.action, f.args = action, args
	if f.err != nil {
		return "", f.err
	}
	return "pa-123", nil
}

// parkTestRegistry builds a registry with one public_write action and one ordinary
// mutating action on a synthetic provider, so these tests do not depend on which real
// providers happen to publish.
func parkTestRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := &Registry{
		providers: map[string]Provider{"fake": {Name: "fake"}},
		actions: map[string][]Action{"fake": {
			{
				Name: "fake_post", Mutating: true, PublicWrite: true,
				ParamsRaw: map[string]any{"type": "object",
					"properties": map[string]any{"text": map[string]any{"type": "string"}},
					"required":   []any{"text"}},
				Request: RequestTemplate{Method: "POST", URL: "https://example.invalid/post"},
			},
			{
				Name: "fake_pause", Mutating: true,
				ParamsRaw: map[string]any{"type": "object", "properties": map[string]any{}},
				Request:   RequestTemplate{Method: "POST", URL: "https://example.invalid/pause"},
			},
		}},
	}
	for p := range reg.actions {
		for i := range reg.actions[p] {
			raw, err := json.Marshal(reg.actions[p][i].ParamsRaw)
			if err != nil {
				t.Fatal(err)
			}
			reg.actions[p][i].Params = raw
		}
	}
	return reg
}

// panicStore fails the test if a token is ever fetched: parking must not require a
// live token, since approval can arrive hours after the park.
type panicStore struct{ t *testing.T }

func (p panicStore) AccessToken(context.Context, ConnRef) (string, error) {
	p.t.Error("parked call must not fetch a token — approval can arrive hours later")
	return "", errors.New("unexpected token fetch")
}

func TestPublicWriteIsParkedNotSent(t *testing.T) {
	reg := parkTestRegistry(t)
	fp := &fakeParker{}

	res, err := Execute(context.Background(), reg, panicStore{t}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_post",
		map[string]any{"text": "hello world"}, Policy{Parker: fp})
	if err != nil {
		t.Fatalf("a parked call must be a SUCCESS, not an error: %v", err)
	}
	if fp.calls != 1 {
		t.Fatalf("Park called %d times, want 1", fp.calls)
	}
	if fp.action != "fake_post" || fp.args["text"] != "hello world" {
		t.Errorf("Park got action=%q args=%v", fp.action, fp.args)
	}

	var got ParkedResult
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("parked result is not valid JSON: %v — %s", err, res.Data)
	}
	if got.Status != "queued_for_approval" || got.ID != "pa-123" {
		t.Errorf("unexpected parked payload: %+v", got)
	}
	// The wording is the ONLY thing stopping an agent recording a queued post as
	// published, so assert it says so unmistakably.
	low := strings.ToLower(got.Note)
	if !strings.Contains(low, "not yet published") {
		t.Errorf("note must state the post is not published: %q", got.Note)
	}
	if !strings.Contains(low, "do not record") {
		t.Errorf("note must tell the model not to record it as posted: %q", got.Note)
	}
}

// The parked payload must never read as an error — the coder's tool loop treats an
// `error:` result as a failing call worth retrying or blocking on.
func TestParkedResultIsNotAnErrorString(t *testing.T) {
	reg := parkTestRegistry(t)
	res, err := Execute(context.Background(), reg, panicStore{t}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_post",
		map[string]any{"text": "hi"}, Policy{Parker: &fakeParker{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(res.Data)), "error:") {
		t.Errorf("parked result must not look like an error: %s", res.Data)
	}
}

// A mutating-but-private action is NOT gated: enabling the gate must not make an
// agent wait on pausing an ad campaign.
func TestNonPublicWriteIsNotParked(t *testing.T) {
	reg := parkTestRegistry(t)
	fp := &fakeParker{}

	// No token store call is expected either way here, because the request will fail
	// at the network; we only care that Park was not consulted.
	_, _ = Execute(context.Background(), reg, stubStore{}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_pause",
		map[string]any{}, Policy{Parker: fp})

	if fp.calls != 0 {
		t.Errorf("a non-public_write action must not be parked (Park called %d times)", fp.calls)
	}
}

// With no Parker the gate is entirely absent — the default path is unchanged.
func TestNoParkerMeansNoGate(t *testing.T) {
	reg := parkTestRegistry(t)
	_, err := Execute(context.Background(), reg, failingStore{}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_post",
		map[string]any{"text": "hi"}, Policy{})
	// It should have proceeded far enough to try for a token, which our store denies.
	if err == nil {
		t.Fatal("expected the ungated path to proceed to the token fetch")
	}
	if strings.Contains(err.Error(), "approval") {
		t.Errorf("ungated call must not mention approval: %v", err)
	}
}

// Invalid args are rejected BEFORE parking: a human should never be asked to approve
// a call that could not have worked.
func TestInvalidArgsRejectedBeforeParking(t *testing.T) {
	reg := parkTestRegistry(t)
	fp := &fakeParker{}

	_, err := Execute(context.Background(), reg, panicStore{t}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_post",
		map[string]any{}, Policy{Parker: fp}) // missing required "text"
	if err == nil {
		t.Fatal("missing required arg must be rejected")
	}
	if fp.calls != 0 {
		t.Error("a call with invalid args must not be parked for a human to discover is broken")
	}
}

// A build must not park either — the build guard fires first, so generation never
// fills the owner's approval queue with test posts.
func TestBuildPhaseBeatsTheGate(t *testing.T) {
	reg := parkTestRegistry(t)
	fp := &fakeParker{}

	_, err := Execute(context.Background(), reg, panicStore{t}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_post",
		map[string]any{"text": "hi"}, Policy{BuildPhase: true, Parker: fp})

	var ce *ConnectorError
	if !errors.As(err, &ce) || ce.Kind != KindBuildBlocked {
		t.Fatalf("want a build-blocked error, got %v", err)
	}
	if fp.calls != 0 {
		t.Error("a build must not enqueue approvals")
	}
}

// A parker failure surfaces as an error rather than silently sending the post.
func TestParkFailureDoesNotFallThroughToSending(t *testing.T) {
	reg := parkTestRegistry(t)
	fp := &fakeParker{err: errors.New("db is down")}

	_, err := Execute(context.Background(), reg, panicStore{t}, &http.Client{},
		ConnRef{ID: "c1", Provider: "fake"}, "fake_post",
		map[string]any{"text": "hi"}, Policy{Parker: fp})
	if err == nil {
		t.Fatal("a failed park must not fall through and publish")
	}
	if !strings.Contains(err.Error(), "queue for approval") {
		t.Errorf("error should name the queueing failure: %v", err)
	}
}

type stubStore struct{}

func (stubStore) AccessToken(context.Context, ConnRef) (string, error) { return "tok", nil }

type failingStore struct{}

func (failingStore) AccessToken(context.Context, ConnRef) (string, error) {
	return "", &ConnectorError{KindNeedsReauth, "no token"}
}
