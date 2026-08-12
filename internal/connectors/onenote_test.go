package connectors

import (
	"strings"
	"testing"
)

// A OneNote page is an HTML DOCUMENT, not JSON — the only request body in this layer
// that is neither. Two things about it are load-bearing and neither is visible in the
// YAML, so they are pinned here.
func TestOneNotePageBuildsATitledHTMLDocument(t *testing.T) {
	body, ct, err := onenotePage(map[string]any{
		"title":   "Standup 11 Aug",
		"content": "<ul><li>Deploy is green</li></ul>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ct != "text/html" {
		t.Fatalf("content type = %q, want text/html", ct)
	}
	// Graph reads the <title> ELEMENT as the page title; there is no title field to
	// send. Without the wrapper the page arrives untitled no matter what was passed.
	if !strings.Contains(string(body), "<title>Standup 11 Aug</title>") {
		t.Errorf("page title is not in a <title> element: %s", body)
	}
	if !strings.Contains(string(body), "<li>Deploy is green</li>") {
		t.Errorf("HTML content was not passed through: %s", body)
	}
}

func TestOneNotePageEscapesPlainTextButNotMarkup(t *testing.T) {
	// The argument is documented as HTML so an agent can write a list or a table. But
	// a plain sentence containing an ampersand is overwhelmingly the common case, and
	// emitting it raw would produce a malformed document.
	body, _, _ := onenotePage(map[string]any{"title": "R&D", "content": "Sales & marketing"})
	if !strings.Contains(string(body), "<p>Sales &amp; marketing</p>") {
		t.Errorf("plain text was not escaped and wrapped: %s", body)
	}
	if !strings.Contains(string(body), "<title>R&amp;D</title>") {
		t.Errorf("title was not escaped: %s", body)
	}

	body, _, _ = onenotePage(map[string]any{"title": "x", "content": "<p>a &amp; b</p>"})
	if strings.Contains(string(body), "&amp;amp;") {
		t.Errorf("markup was double-escaped: %s", body)
	}
}
