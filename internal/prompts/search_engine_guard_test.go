package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPromptsNameNoSearchEngine keeps prompt text engine-neutral.
//
// Prompts are built without access to the coder's configured provider list, so
// any engine named here is a guess. It was wrong in exactly the way that
// matters: prompts.go claimed "web_search(query): search the public web
// (DuckDuckGo)" while the workspace had a Brave key, and the model repeated
// that to the user as fact — including telling them their Brave key "isn't
// wired into anything I can call".
//
// The engine belongs in two places only, both of which know the real answer:
// the generated web_search tool description, and the per-result provenance tag.
func TestPromptsNameNoSearchEngine(t *testing.T) {
	engines := []string{"DuckDuckGo", "Brave Search", "Mojeek", "Tavily"}

	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	checked := 0
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		checked++
		for _, engine := range engines {
			if strings.Contains(string(data), engine) {
				t.Errorf("%s names the search engine %q — prompt text must stay "+
					"engine-neutral; the engine is reported by the web_search tool "+
					"description and the per-result provenance tag, which know which "+
					"provider is actually configured", path, engine)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no prompt sources scanned — the guard would pass vacuously")
	}
}
