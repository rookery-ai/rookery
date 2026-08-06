package prompts

import "strings"
import "testing"

func TestKBAssistActionsIsClosed(t *testing.T) {
	got := KBAssistActions()
	want := []string{"improve", "proofread", "explain", "reformat"}
	if len(got) != len(want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("actions = %v, want %v", got, want)
		}
	}
}

func TestBuildKBAssistPromptCarriesSelectionAndPath(t *testing.T) {
	p := BuildKBAssistPrompt("improve", "notes/ci.md", "the pipeline runs on merge")
	if !strings.Contains(p, "the pipeline runs on merge") {
		t.Error("prompt does not carry the selection")
	}
	if !strings.Contains(p, "notes/ci.md") {
		t.Error("prompt does not carry the note path")
	}
}

func TestBuildKBAssistPromptExplainDoesNotRewrite(t *testing.T) {
	// Explain is the one action whose output is not a replacement. If its
	// prompt reads like the other three, the model returns a rewrite and the
	// panel presents an explanation that is actually edited prose.
	p := strings.ToLower(BuildKBAssistPrompt("explain", "notes/ci.md", "release-please"))
	if !strings.Contains(p, "explain") {
		t.Error("explain prompt does not ask for an explanation")
	}
	if strings.Contains(p, "return only the rewritten") {
		t.Error("explain prompt asks for a rewrite")
	}
}

func TestBuildKBAssistPromptRewritesReturnOnlyTheText(t *testing.T) {
	for _, a := range []string{"improve", "proofread", "reformat"} {
		p := strings.ToLower(BuildKBAssistPrompt(a, "notes/ci.md", "x"))
		if !strings.Contains(p, "return only the rewritten") {
			t.Errorf("%s prompt does not constrain the output to the text alone", a)
		}
	}
}
