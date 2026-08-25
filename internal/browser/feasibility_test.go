package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubBrowser struct {
	available bool
	calls     int
	byURL     map[string]Result
	err       error
}

func (s *stubBrowser) Available() Availability {
	return Availability{OK: s.available}
}

func (s *stubBrowser) Render(_ context.Context, r Request) (Result, error) {
	s.calls++
	if s.err != nil {
		return Result{}, s.err
	}
	return s.byURL[r.URL], nil
}

func (s *stubBrowser) Act(context.Context, ActRequest) (Result, error) {
	return Result{}, nil
}
func (s *stubBrowser) CloseSession(context.Context, string) {}

func TestFeasibilityIsSilentWithoutAURL(t *testing.T) {
	var cache *FeasibilityCache
	got := Feasibility(context.Background(), &stubBrowser{available: true}, &cache, "watch my electricity bill each month")
	if got != "" {
		t.Fatalf("probed with no URL mentioned: %q", got)
	}
}

func TestFeasibilityIsSilentWithoutABrowser(t *testing.T) {
	var cache *FeasibilityCache
	got := Feasibility(context.Background(), &stubBrowser{available: false}, &cache, "check https://example.com daily")
	if got != "" {
		t.Fatalf("claimed to probe with no runtime: %q", got)
	}
}

// The whole point: a bot wall must be named during the CONVERSATION, and the
// designer must be told it cannot be worked around. Anything softer produces a
// plan that spends a six-minute build failing.
func TestFeasibilityReportsABotWallAsUnworkaroundable(t *testing.T) {
	br := &stubBrowser{
		available: true,
		byURL: map[string]Result{
			"https://shop.example.com": {Blocked: "cloudflare", BlockedNote: "the site is behind a Cloudflare browser check"},
		},
	}
	var cache *FeasibilityCache
	got := Feasibility(context.Background(), br, &cache, "track prices on https://shop.example.com")
	if !strings.Contains(got, "BLOCKED") || !strings.Contains(got, "Cloudflare") {
		t.Fatalf("blocker not reported: %q", got)
	}
	if !strings.Contains(got, "cannot get past") {
		t.Errorf("does not say it is unworkaroundable: %q", got)
	}
	if !strings.Contains(got, "suggest another route") {
		t.Errorf("does not steer toward an alternative: %q", got)
	}
}

// A login wall is a DIFFERENT answer from a bot wall: it is something the owner
// can actually fix by storing credentials, so conflating the two would talk
// them out of a perfectly buildable agent.
func TestFeasibilityDistinguishesALoginFromABotWall(t *testing.T) {
	br := &stubBrowser{
		available: true,
		byURL:     map[string]Result{"https://bank.example.com": {Blocked: "login"}},
	}
	var cache *FeasibilityCache
	got := Feasibility(context.Background(), br, &cache, "pay my bill at https://bank.example.com")
	if strings.Contains(got, "cannot get past") {
		t.Errorf("a login wall was reported as unworkaroundable: %q", got)
	}
	if !strings.Contains(got, "secrets") {
		t.Errorf("does not mention storing credentials: %q", got)
	}
}

func TestFeasibilityDescribesAWorkableSite(t *testing.T) {
	br := &stubBrowser{
		available: true,
		byURL: map[string]Result{
			"https://ok.example.com": {
				Title:    "Invoices",
				Elements: []Element{{Role: "textbox", Name: "Account"}, {Role: "button", Name: "Go"}},
			},
		},
	}
	var cache *FeasibilityCache
	got := Feasibility(context.Background(), br, &cache, "read https://ok.example.com")
	if !strings.Contains(got, "reachable and readable") {
		t.Fatalf("workable site not described: %q", got)
	}
	if !strings.Contains(got, "form field") {
		t.Errorf("form fields not reported: %q", got)
	}
}

// A design turn is a blocking request with no progress stream, so an unbounded
// probe loop would leave the user watching a spinner.
func TestFeasibilityBoundsHowManySitesItProbes(t *testing.T) {
	sb := &stubBrowser{available: true, byURL: map[string]Result{}}
	var cache *FeasibilityCache
	msg := "check https://a.example.com https://b.example.com https://c.example.com https://d.example.com https://e.example.com"
	Feasibility(context.Background(), sb, &cache, msg)
	if sb.calls > maxProbesPerSession {
		t.Fatalf("probed %d sites, cap is %d", sb.calls, maxProbesPerSession)
	}
}

// The same site named twice in one conversation must cost one render.
func TestFeasibilityCachesWithinASession(t *testing.T) {
	sb := &stubBrowser{available: true, byURL: map[string]Result{"https://x.example.com": {Title: "X"}}}
	var cache *FeasibilityCache
	Feasibility(context.Background(), sb, &cache, "look at https://x.example.com")
	Feasibility(context.Background(), sb, &cache, "and again https://x.example.com")
	if sb.calls != 1 {
		t.Fatalf("rendered %d times, want 1", sb.calls)
	}
}

// A probe failure must not fail the design turn — the conversation is worth
// more than the hint.
func TestFeasibilityDegradesWhenTheProbeFails(t *testing.T) {
	var cache *FeasibilityCache
	br := &stubBrowser{available: true, err: errors.New("net::ERR_NAME_NOT_RESOLVED\nstack line\nanother")}
	got := Feasibility(context.Background(), br, &cache, "read https://nope.example.com")
	if !strings.Contains(got, "could not be opened") {
		t.Fatalf("failure not reported: %q", got)
	}
	if strings.Contains(got, "stack line") {
		t.Errorf("dumped a multi-line error into the prompt: %q", got)
	}
}

func TestExtractURLsTrimsTrailingPunctuation(t *testing.T) {
	got := extractURLs("see https://example.com/page. and https://other.example.com, too")
	want := []string{"https://example.com/page", "https://other.example.com"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
