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
	// A zip-magic buffer named .xlsx detects as KindXLSX, which has no
	// converter yet.
	data := []byte("PK\x03\x04something")
	res, err := ToMarkdown(data, Options{Filename: "report.xlsx"})
	if err == nil {
		t.Fatalf("ToMarkdown() error = nil, want an error naming the unsupported format")
	}
	if !strings.Contains(err.Error(), "xlsx") {
		t.Errorf("ToMarkdown() error = %q, want it to contain %q", err.Error(), "xlsx")
	}
	if res.Markdown != "" {
		t.Errorf("ToMarkdown() Markdown = %q, want empty on error", res.Markdown)
	}
}
