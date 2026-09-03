package web

import "testing"

// Only vault-relative non-image LINKS become attachments.
//
// The exclusions matter as much as the inclusions. An image is inlined into the
// document, so listing it as well would tell the reader to go and find a file
// they are already looking at. An http link is already portable. And a fragment
// points inside the document itself.
func TestCollectAttachments(t *testing.T) {
	md := []byte(
		"See the [Q3 report](uploads/q3.pdf) and [figures](uploads/figures.xlsx).\n\n" +
			"![a diagram](uploads/diagram.png)\n\n" +
			"[the website](https://example.com/thing.pdf)\n\n" +
			"[jump](#section)\n\n" +
			"[mail us](mailto:someone@example.com)\n\n" +
			"[absolute](/etc/passwd)\n\n" +
			"[the same report again](uploads/q3.pdf)\n\n" +
			"[](uploads/unnamed.csv)\n")

	got := collectAttachments(md)

	want := []struct{ name, path string }{
		{"Q3 report", "uploads/q3.pdf"},
		{"figures", "uploads/figures.xlsx"},
		// An empty label falls back to the file name, so a list entry is never
		// blank.
		{"unnamed.csv", "uploads/unnamed.csv"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d attachments %v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Path != w.path {
			t.Errorf("attachment %d = {%q, %q}, want {%q, %q}", i, got[i].Name, got[i].Path, w.name, w.path)
		}
	}
}

// A note with nothing linked produces no list, so an ordinary note exports
// exactly as it did before.
func TestCollectAttachmentsIgnoresAPlainNote(t *testing.T) {
	if got := collectAttachments([]byte("# Title\n\nJust prose, and an ![image](uploads/x.png).\n")); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}
