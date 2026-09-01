package convert

import (
	"strings"
	"testing"
)

// A table in a PDF used to arrive as one run-on paragraph with the figures
// interleaved — worse than useless, because it still reads as prose. pdftotext
// is asked for -layout precisely so the columns survive; paragraphize discarded
// that one function later.
func TestPDFLayoutTableBecomesAMarkdownTable(t *testing.T) {
	// The shape pdftotext -layout emits: columns padded out with runs of spaces.
	in := "Region      Revenue     Growth\n" +
		"EMEA        1200        12%\n" +
		"APAC         900         8%\n"

	got := paragraphize(in)
	for _, want := range []string{
		"| Region | Revenue | Growth |",
		"| --- | --- | --- |",
		"| EMEA | 1200 | 12% |",
		"| APAC | 900 | 8% |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// The conservative half, and the more important one: a false positive replaces
// readable prose with a nonsense table, which is harder to repair than the
// run-on paragraph it displaced.
func TestPDFLayoutLeavesProseAlone(t *testing.T) {
	cases := []struct{ name, in string }{
		{
			name: "ordinary wrapped prose",
			in: "Revenue grew twelve percent this quarter across every region,\n" +
				"driven by renewals rather than new business, and the trend\n" +
				"is expected to continue into the next period.\n",
		},
		{
			name: "only two lines share a gap",
			in:   "Name        Value\nAda         42\n",
		},
		{
			name: "ragged column counts",
			in:   "Alpha   one\nBeta   two   three\nGamma   four\n",
		},
		{
			name: "single column with indentation",
			in:   "    Indented one\n    Indented two\n    Indented three\n",
		},
		{
			name: "trailing gap only, no second column content",
			in:   "Alpha   \nBeta   \nGamma   \n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := paragraphize(tc.in)
			if strings.Contains(got, "| --- |") {
				t.Errorf("prose was misread as a table:\n%s", got)
			}
		})
	}
}

// A pipe or an angle bracket inside a PDF table cell must not break the row or
// the editor round trip; recovered cells go through the same escapeCell every
// other table uses.
func TestPDFLayoutTableEscapesCells(t *testing.T) {
	in := "Name        Note\n" +
		"Alpha       a | b\n" +
		"Beta        x < y\n"

	got := paragraphize(in)
	if !strings.Contains(got, `| Alpha | a \| b |`) {
		t.Errorf("pipe not escaped in:\n%s", got)
	}
	if !strings.Contains(got, "| Beta | x &lt; y |") {
		t.Errorf("angle bracket not escaped in:\n%s", got)
	}
}

func TestLooksTabularBounds(t *testing.T) {
	t.Run("rejects too many columns", func(t *testing.T) {
		var line []string
		for i := 0; i < maxTableCols+1; i++ {
			line = append(line, "c")
		}
		row := strings.Join(line, "   ")
		if _, ok := looksTabular([]string{row, row, row}); ok {
			t.Error("accepted a block with more columns than the cap")
		}
	})
	t.Run("accepts the minimum shape", func(t *testing.T) {
		rows, ok := looksTabular([]string{"a   b", "c   d", "e   f"})
		if !ok {
			t.Fatal("rejected a well-formed minimal table")
		}
		if len(rows) != 3 || len(rows[0]) != 2 {
			t.Errorf("got %v, want 3 rows of 2 cells", rows)
		}
	})
}
