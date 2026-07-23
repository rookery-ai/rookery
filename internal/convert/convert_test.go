package convert

import (
	"strings"
	"testing"
)

// TestToMarkdownNeverEmptyOnSuccess pins a Global Constraint: a tool must never
// return an empty string. ToMarkdown must never return a Result whose Markdown
// is empty alongside a nil error.
func TestToMarkdownNeverEmptyOnSuccess(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		opt  Options
	}{
		{"plain text", []byte("just some words"), Options{}},
		{"markdown", []byte("# Title\n\nbody"), Options{Filename: "notes.md"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ToMarkdown(tc.data, tc.opt)
			if err != nil {
				t.Fatalf("ToMarkdown() error = %v, want nil", err)
			}
			if res.Markdown == "" {
				t.Errorf("ToMarkdown() Markdown = %q, want non-empty", res.Markdown)
			}
		})
	}
}

// TestToMarkdownUnsupportedNamesFormat pins the other Global Constraint: an
// unsupported format must return an error that names what happened, never a
// silent empty result.
func TestToMarkdownUnsupportedNamesFormat(t *testing.T) {
	// PDF was this test's stand-in "unsupported" format before Task 11 (which
	// also gave xlsx a real converter in Task 10). Task 11 wires PDF, image,
	// and JSON all at once, so every named Kind now dispatches to a real
	// converter — none of them fall through to the generic "not supported
	// yet" branch anymore. The one input ToMarkdown still refuses is bytes
	// Detect cannot classify as anything at all (KindUnknown): no magic, no
	// usable extension/MIME, not text. That case must still error and say so,
	// never return a silent empty result.
	data := []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd, 0x10, 0x11}
	res, err := ToMarkdown(data, Options{Filename: "mystery.bin"})
	if err == nil {
		t.Fatalf("ToMarkdown() error = nil, want an error naming the unrecognized format")
	}
	if !strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("ToMarkdown() error = %q, want it to mention the format is unrecognized", err.Error())
	}
	if res.Markdown != "" {
		t.Errorf("ToMarkdown() Markdown = %q, want empty on error", res.Markdown)
	}
}

// KindJSON is classified from the extension/MIME hint alone with no JSON
// validation, so the content here is arbitrary uploaded bytes — including,
// deliberately, an invalid-JSON line that is exactly three backticks by
// itself (the CommonMark closing-fence trigger) and no trailing newline
// (the other half of the same defect: appending "```" directly onto the
// content's last line without a newline never produces a real closing-fence
// line at all).
func TestJSONFenceNotBrokenByContent(t *testing.T) {
	data := []byte("{\n\"before\": 1\n```\nfence-breaking line above\n```\n\"after\": 2")
	got, err := ToMarkdown(data, Options{Filename: "payload.json"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	lines := strings.Split(strings.TrimRight(got.Markdown, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("markdown too short:\n%s", got.Markdown)
	}
	if !strings.HasSuffix(lines[0], "json") {
		t.Fatalf("expected opening fence line to end in \"json\", got %q", lines[0])
	}
	openFence := strings.TrimSuffix(lines[0], "json")
	closeFence := lines[len(lines)-1]
	if closeFence != openFence {
		t.Errorf("closing fence %q does not match opening fence %q — content broke out early, got:\n%s", closeFence, openFence, got.Markdown)
	}
	if len(openFence) < 4 {
		t.Errorf("fence %q is not longer than the embedded ``` run", openFence)
	}
	// The whole body — including the embedded ``` lines and the un-terminated
	// last line — must be preserved as fenced content, not split into content
	// plus a second stray fence.
	if strings.Count(got.Markdown, openFence) != 2 {
		t.Errorf("expected exactly 2 fence lines (open+close), got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "fence-breaking line above") || !strings.Contains(got.Markdown, "\"after\": 2") {
		t.Errorf("embedded content lost, got:\n%s", got.Markdown)
	}
}
