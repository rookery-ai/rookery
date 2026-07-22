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
