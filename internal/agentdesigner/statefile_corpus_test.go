package agentdesigner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestStateTemplateMatchesFidelityCorpusPin is the drift guard between
// RenderStateTemplate and the KB editor's fidelity corpus
// (web/ui/src/pages/kb/corpus.test.ts, entry "agent-state-md-template"), which pins
// the template's output as CLEAN — safe to open in the WYSIWYG editor — so a future
// tiptap-markdown upgrade that breaks state.md's round-trip fails loudly instead of
// silently forcing every agent's state.md into raw mode.
//
// A hand-copied TS literal would silently drift from the real template and stop
// testing anything real (the corpus test would keep "passing" against a string that
// no agent's state.md ever actually contains). Since the corpus is TypeScript and
// this template is Go, there is no import path linking them directly — so instead
// this test reads corpus.test.ts's CURRENT source at Go test time, extracts that
// exact pinned literal, and asserts it equals a live call to RenderStateTemplate. A
// change on EITHER side — the Go template's wording, or the TS literal drifting out
// of sync with it — fails this test.
func TestStateTemplateMatchesFidelityCorpusPin(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	corpusPath := filepath.Join(wd, "..", "..", "web", "ui", "src", "pages", "kb", "corpus.test.ts")
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("reading corpus.test.ts: %v (path %s)", err, corpusPath)
	}

	re := regexp.MustCompile(`name:\s*"agent-state-md-template",\s*md:\s*('(?:[^'\\]|\\.)*')`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("could not find the \"agent-state-md-template\" corpus entry in %s — has it been renamed or removed? Update this test's regex to match.", corpusPath)
	}
	pinned, err := decodeJSSingleQuoted(string(m[1]))
	if err != nil {
		t.Fatalf("decoding pinned corpus literal: %v", err)
	}

	want := RenderStateTemplate("Gmail Digest", "{\n  \"a\": 1\n}")
	if pinned != want {
		t.Errorf("web/ui/src/pages/kb/corpus.test.ts's \"agent-state-md-template\" literal no longer "+
			"matches RenderStateTemplate's live output — either the Go template changed or the TS "+
			"literal drifted out of sync. Update the corpus.test.ts literal to match.\n\n"+
			"RenderStateTemplate output:\n%q\n\ncorpus.test.ts pin:\n%q", want, pinned)
	}
}

// decodeJSSingleQuoted decodes a single-quoted JavaScript string literal (including
// its surrounding quotes) into its runtime string value. Only the escapes the
// corpus.test.ts pin actually uses (\n, \', \\, \") are handled — this is a narrow
// helper for the drift guard above, not a general JS string parser.
func decodeJSSingleQuoted(s string) (string, error) {
	if len(s) < 2 || !strings.HasPrefix(s, "'") || !strings.HasSuffix(s, "'") {
		return "", fmt.Errorf("not a single-quoted literal: %q", s)
	}
	body := s[1 : len(s)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			i++
			switch body[i] {
			case 'n':
				out.WriteByte('\n')
			case '\'':
				out.WriteByte('\'')
			case '\\':
				out.WriteByte('\\')
			case '"':
				out.WriteByte('"')
			default:
				return "", fmt.Errorf("unhandled escape \\%c in pinned literal", body[i])
			}
			continue
		}
		out.WriteByte(c)
	}
	return out.String(), nil
}
