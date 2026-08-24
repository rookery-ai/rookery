package prompts

import (
	"strings"
	"testing"
)

// TestDesignToolsBlockAppearsInBothDesigners — the two designers share a surface
// and drift; a capability described to one and not the other is exactly the
// inconsistency that costs a build.
func TestDesignToolsBlockAppearsInBothDesigners(t *testing.T) {
	bodies := map[string]string{
		"agent": BuildDesignSystemPrompt(DesignSystemParams{AgentName: "a"}),
		"skill": BuildSkillDesignSystemPrompt(SkillDesignParams{SkillName: "s"}),
	}
	for name, body := range bodies {
		for _, want := range []string{"read_file", "kb_file_map", "web_fetch", "search_files"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s design prompt does not mention %q", name, want)
			}
		}
	}
}

// TestDesignToolsBlockMarksWebContentUntrusted.
//
// The designer's reply is read by a human AND appended to History, which
// BuildImplementationPrompt embeds verbatim as <design_conversation> for a
// builder holding Bash and real secrets. This is prompt-level steering rather
// than a boundary — but its absence is what makes a fetched page's instructions
// read like the user's own.
func TestDesignToolsBlockMarksWebContentUntrusted(t *testing.T) {
	body := strings.ToLower(designToolsBlock())
	for _, want := range []string{"untrusted", "never instructions"} {
		if !strings.Contains(body, want) {
			t.Errorf("design tools block does not say %q", want)
		}
	}
}

// TestDesignToolsBlockExplainsThePrivateAddressGuard. web_fetch cannot dial
// RFC1918/loopback, so without this the designer reports every self-hosted URL
// as a service being down rather than as one it cannot see from here.
func TestDesignToolsBlockExplainsThePrivateAddressGuard(t *testing.T) {
	body := strings.ToLower(designToolsBlock())
	if !strings.Contains(body, "private") || !strings.Contains(body, "self-hosted") {
		t.Error("design tools block does not explain the private-address guard")
	}
}

// TestDesignToolsBlockOffersNoWriteTool guards against the prompt advertising a
// capability the profile withholds: the model would try it, take an error, and
// have spent a turn instead of asking the user.
func TestDesignToolsBlockOffersNoWriteTool(t *testing.T) {
	body := designToolsBlock()
	for _, banned := range []string{"write_file", "edit_file", "save_to_kb", "run_script"} {
		if strings.Contains(body, banned) {
			t.Errorf("design tools block advertises %q, which the profile withholds", banned)
		}
	}
}

// TestDesignToolsBlockNamesEveryOfferedTool keeps the prompt and
// coder.WithReadOnlyTools in step. A tool offered but never described is one the
// model does not know it has — which is the whole defect this change set fixes.
func TestDesignToolsBlockNamesEveryOfferedTool(t *testing.T) {
	body := designToolsBlock()
	for _, want := range []string{
		"read_file", "list_dir", "search_files", "glob",
		"kb_file_map", "kb_table_query", "web_fetch", "web_search",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("design tools block does not name the offered tool %q", want)
		}
	}
}
