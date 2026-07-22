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
// unsupported format must return an error that names the format, never a
// silent empty result.
func TestToMarkdownUnsupportedNamesFormat(t *testing.T) {
	// PDF magic bytes detect as KindPDF, which has no converter yet. (An
	// earlier version of this test used xlsx as the stand-in "unsupported"
	// format, but Task 10 gave xlsx a real converter, so it had to move to a
	// format that is still genuinely unsupported.)
	data := []byte("%PDF-1.7\nsomething")
	res, err := ToMarkdown(data, Options{Filename: "report.pdf"})
	if err == nil {
		t.Fatalf("ToMarkdown() error = nil, want an error naming the unsupported format")
	}
	if !strings.Contains(err.Error(), "pdf") {
		t.Errorf("ToMarkdown() error = %q, want it to contain %q", err.Error(), "pdf")
	}
	if res.Markdown != "" {
		t.Errorf("ToMarkdown() Markdown = %q, want empty on error", res.Markdown)
	}
}
