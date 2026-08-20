//go:build livecheck

package chat

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Manual verification against a real install's chat_messages rows, excluded from
// the normal run (build tag) because it depends on an exported fixture that is
// not in the repository. Run with:
//
//	go test -tags livecheck ./internal/chat/ -run TestAgainstLiveRows -v
//
// ROOKERY_LIVE_ROWS points at a JSON array of assistant message bodies.
func TestAgainstLiveRows(t *testing.T) {
	path := os.Getenv("ROOKERY_LIVE_ROWS")
	if path == "" {
		t.Skip("ROOKERY_LIVE_ROWS not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}

	markers := []string{"[CHAT]", "[/CHAT]", "[SILENT]", "[STATE]", "[CALL:"}
	residual := regexp.MustCompile(`(?m)^[ \t]*\[/?(CHAT|STATE|SILENT|CALL)`)

	var withMarkers, mangled, leaks, placeheld int
	for _, r := range rows {
		hasMarker := false
		for _, m := range markers {
			if strings.Contains(r, m) {
				hasMarker = true
				break
			}
		}
		got := CleanReply(r)
		if !hasMarker {
			if got != strings.TrimSpace(r) {
				mangled++
				t.Errorf("clean prose rewritten:\n%.200q\n -> %.200q", r, got)
			}
			continue
		}
		withMarkers++
		if residual.MatchString(got) {
			leaks++
			t.Errorf("marker survived cleaning:\n%.300q", got)
		}
		if got == markerOnlyPlaceholder {
			placeheld++
		}
	}
	t.Logf("rows=%d withMarkers=%d residualLeaks=%d mangledProse=%d placeholders=%d",
		len(rows), withMarkers, leaks, mangled, placeheld)
}
