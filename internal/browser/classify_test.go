package browser

import (
	"strings"
	"testing"
)

func TestClassifyNamesCloudflareBeforeCaptcha(t *testing.T) {
	// A Cloudflare interstitial mentions captchas too. Reporting it as "a
	// captcha" would send the user looking for a captcha solver when the actual
	// obstacle is the site's edge protection.
	f := PageFacts{
		Title: "Just a moment...",
		Text:  "Checking your browser before accessing. Enable JavaScript and cookies to continue. recaptcha",
	}
	kind, note := Classify(f)
	if kind != "cloudflare" {
		t.Fatalf("kind = %q, want cloudflare (note %q)", kind, note)
	}
}

func TestClassifyDetectsACaptchaLivingInAnIframe(t *testing.T) {
	// The host page frequently says nothing; the widget is in a frame.
	f := PageFacts{
		Title:      "Sign in",
		Text:       "Please complete the step below.",
		FrameNames: []string{"https://www.google.com/recaptcha/api2/anchor?k=abc"},
	}
	if kind, _ := Classify(f); kind != "captcha" {
		t.Fatalf("kind = %q, want captcha", kind)
	}
}

// A login wall is judged on structure, never on prose. Almost every site has a
// "Sign in" link in its header, so phrase-matching would mark most of the web
// as blocked.
func TestClassifyJudgesLoginWallsStructurally(t *testing.T) {
	withLink := PageFacts{Title: "Docs", Text: "Home Sign in Log in Products"}
	if kind, _ := Classify(withLink); kind != "" {
		t.Fatalf("ordinary page with a sign-in LINK classified as %q", kind)
	}
	withField := PageFacts{Title: "Sign in", Text: "Sign in", HasPasswordField: true}
	if kind, _ := Classify(withField); kind != "login" {
		t.Fatalf("password field not classified as a login wall, got %q", kind)
	}
}

func TestClassifyReportsAnUnnamedBotBlock(t *testing.T) {
	f := PageFacts{Status: 403, Text: "Forbidden"}
	kind, note := Classify(f)
	if kind != "bot-check" {
		t.Fatalf("kind = %q, want bot-check", kind)
	}
	if !strings.Contains(note, "403") {
		t.Errorf("note should name the status, got %q", note)
	}
}

// A 403 that still returned a real page is not a bot block — plenty of sites
// serve readable content alongside an error status.
func TestClassifyLeavesAReadable403Alone(t *testing.T) {
	f := PageFacts{Status: 403, Text: strings.Repeat("real article content ", 40)}
	if kind, _ := Classify(f); kind != "" {
		t.Fatalf("readable 403 classified as %q", kind)
	}
}

func TestClassifyPassesAnOrdinaryPage(t *testing.T) {
	f := PageFacts{Title: "Weather in Skopje", Text: "25C, clear sky", Status: 200}
	if kind, note := Classify(f); kind != "" {
		t.Fatalf("ordinary page classified as %q (%s)", kind, note)
	}
}

func TestPageSlicesByRuneAndReportsTheNextOffset(t *testing.T) {
	// Multi-byte on purpose: slicing by byte would cut mid-rune, put U+FFFD in
	// the model's context, and hand back an offset the next call resumes from
	// incorrectly.
	text := strings.Repeat("ñ", 100)
	out, truncated, next := Page(text, 0, 10)
	if !truncated || next != 10 {
		t.Fatalf("truncated=%v next=%d", truncated, next)
	}
	if len([]rune(out)) != 10 {
		t.Fatalf("got %d runes, want 10", len([]rune(out)))
	}
	if strings.ContainsRune(out, '�') {
		t.Error("output contains a replacement rune — sliced mid-character")
	}
	rest, truncated, _ := Page(text, 10, 1000)
	if truncated {
		t.Error("final page reported as truncated")
	}
	if len([]rune(rest)) != 90 {
		t.Fatalf("tail = %d runes, want 90", len([]rune(rest)))
	}
}

func TestPageHandlesAnOffsetPastTheEnd(t *testing.T) {
	out, truncated, next := Page("short", 500, 100)
	if out != "" || truncated || next != 0 {
		t.Fatalf("got %q %v %d", out, truncated, next)
	}
}

// A silent truncation reads as "this page has nine controls", so the model
// concludes the button it needs does not exist and gives up or invents one.
func TestRenderElementsSaysHowManyItWithheld(t *testing.T) {
	var els []Element
	for i := 0; i < 20; i++ {
		els = append(els, Element{Ref: "e" + string(rune('a'+i)), Role: "button", Name: "b"})
	}
	out := RenderElements(els, 5)
	if !strings.Contains(out, "15 more interactive elements not shown") {
		t.Fatalf("withheld count missing from:\n%s", out)
	}
}

func TestRenderElementsIsExplicitWhenThereAreNone(t *testing.T) {
	if out := RenderElements(nil, 10); !strings.Contains(out, "no interactive elements") {
		t.Fatalf("got %q", out)
	}
}
