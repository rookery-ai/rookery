package approval

import (
	"strings"
	"testing"
)

func TestSummarizeShowsTheActualContent(t *testing.T) {
	// The owner approves from a chat message; the action name alone is not enough to
	// decide with, so the content has to be in the summary.
	got := Summarize("linkedin_create_post", map[string]any{"text": "Shipping v2 today"})
	if !strings.Contains(got, "Shipping v2 today") {
		t.Errorf("summary must show the content, got %q", got)
	}
	if !strings.Contains(got, "linkedin_create_post") {
		t.Errorf("summary must name the action, got %q", got)
	}
}

func TestSummarizeFallsBackToActionName(t *testing.T) {
	got := Summarize("youtube_upload_video", map[string]any{"video_id": "abc"})
	if got != "youtube_upload_video" {
		t.Errorf("with no textual arg the summary should be just the action, got %q", got)
	}
}

// A long post must not flood a chat message.
func TestSummarizeTruncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := Summarize("bluesky_create_post", map[string]any{"text": long})
	if len([]rune(got)) > 320 {
		t.Errorf("summary is %d runes, expected it capped near 280", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated summary should say so: %q", got)
	}
}

// Summarize checks several arg names because providers disagree; "message" and
// "title" are as common as "text".
func TestSummarizePrefersKnownTextKeys(t *testing.T) {
	for _, key := range []string{"text", "message", "title", "caption", "body", "comment", "status"} {
		got := Summarize("act", map[string]any{key: "the content"})
		if !strings.Contains(got, "the content") {
			t.Errorf("arg %q was not summarised: %q", key, got)
		}
	}
}

// A non-string value for a known key must not panic or print Go syntax at the owner.
func TestSummarizeIgnoresNonStringValues(t *testing.T) {
	got := Summarize("act", map[string]any{"text": 42})
	if got != "act" {
		t.Errorf("non-string content should be skipped, got %q", got)
	}
}

func TestFirstURLPrefersPermalinkKeys(t *testing.T) {
	// A nested avatar URL must not beat the post's own link, or the owner clicks
	// through to the wrong thing.
	body := `{"author":{"avatar_url":"https://cdn.example.com/a.png"},"html_url":"https://example.com/post/1"}`
	if got := firstURL(body); got != "https://example.com/post/1" {
		t.Errorf("firstURL = %q, want the permalink", got)
	}
}

func TestFirstURLHandlesNoURL(t *testing.T) {
	for _, in := range []string{``, `{"ok":true}`, `not json`, `null`} {
		if got := firstURL(in); got != "" {
			t.Errorf("firstURL(%q) = %q, want empty", in, got)
		}
	}
}

// Only https links are surfaced: an http link in a provider payload is as likely to
// be a schema URL as a permalink.
func TestFirstURLIgnoresNonHTTPS(t *testing.T) {
	if got := firstURL(`{"url":"http://example.com/x"}`); got != "" {
		t.Errorf("firstURL = %q, want empty for a non-https link", got)
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault("", "An agent") != "An agent" {
		t.Error("empty should fall back")
	}
	if orDefault("   ", "An agent") != "An agent" {
		t.Error("whitespace-only should fall back")
	}
	if orDefault("poster", "An agent") != "poster" {
		t.Error("a real name should win")
	}
}
