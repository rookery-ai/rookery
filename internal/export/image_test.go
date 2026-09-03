package export

import "testing"

// SplitAltWidth must agree with the editor's TypeScript splitAltWidth
// (web/ui/src/pages/kb/imageResize.ts) case for case.
//
// The two cannot share an implementation across the language boundary, so the
// cases below are lifted from that function's own contract: split on the LAST
// pipe, and only when the tail is a bare integer, so an alt that genuinely
// contains a pipe survives. Getting this wrong in the lenient direction would
// eat real alt text; getting it wrong in the strict direction silently drops
// every width, which is the bug this exists to fix.
func TestSplitAltWidthAgreesWithTheEditor(t *testing.T) {
	cases := []struct {
		in        string
		wantAlt   string
		wantWidth int
	}{
		{"before|420", "before", 420},
		{"", "", 0},
		{"plain alt", "plain alt", 0},
		// No width: the whole string is alt text.
		{"a|b", "a|b", 0},
		{"trailing pipe|", "trailing pipe|", 0},
		{"not a number|12px", "not a number|12px", 0},
		{"negative|-40", "negative|-40", 0},
		{"decimal|4.5", "decimal|4.5", 0},
		{"spaced| 420", "spaced| 420", 0},
		// The LAST pipe wins, so an alt containing a pipe keeps it.
		{"a|b|300", "a|b", 300},
		// An empty alt with a width is what a resized image with no
		// description actually serialises as, and is the common case.
		{"|420", "", 420},
	}
	for _, tc := range cases {
		alt, width := SplitAltWidth(tc.in)
		if alt != tc.wantAlt || width != tc.wantWidth {
			t.Errorf("SplitAltWidth(%q) = (%q, %d), want (%q, %d)",
				tc.in, alt, width, tc.wantAlt, tc.wantWidth)
		}
	}
}

// A width in pixels becomes EMU for OOXML. 9525 EMU per pixel at 96 DPI is
// fixed by the format, not a tunable.
func TestPixelsToEMU(t *testing.T) {
	if got := pixelsToEMU(420); got != 420*9525 {
		t.Errorf("pixelsToEMU(420) = %d, want %d", got, 420*9525)
	}
	if got := pixelsToEMU(0); got != 0 {
		t.Errorf("pixelsToEMU(0) = %d, want 0", got)
	}
}
