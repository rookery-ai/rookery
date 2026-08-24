package web

import "testing"

// The reported bug: the new-file "Location" picker rendered agents/<agentID>
// verbatim, so every agent folder read as a UUID. The tree had already solved
// this; the picker had no label at all.
func TestKBFolderLabelNamesAgentsAndSystemDirs(t *testing.T) {
	agents := map[string]string{
		"9f3c1b2a-0000-4000-8000-000000000001": "Weather Digest",
	}
	cases := []struct{ path, want string }{
		{"", ""},
		{"notes", "Notes"},
		{"agents", "Agents"},
		{
			"agents/9f3c1b2a-0000-4000-8000-000000000001",
			"Agents/Weather Digest",
		},
		{
			"agents/9f3c1b2a-0000-4000-8000-000000000001/logs",
			"Agents/Weather Digest/logs",
		},
		// A folder the user made is theirs — no label, no capitalisation.
		{"Project Plans", "Project Plans"},
		// Only the TOP level is a system dir. A user folder nested under their
		// own tree that happens to be called "notes" must not be relabelled.
		{"Project Plans/notes", "Project Plans/notes"},
	}
	for _, c := range cases {
		if got := kbFolderLabel(c.path, agents); got != c.want {
			t.Errorf("kbFolderLabel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// A deleted agent leaves its directory behind. Keeping the raw id is better
// than inventing a name: the label is only ever a hint about which folder the
// path refers to, and a wrong hint is worse than an ugly one.
func TestKBFolderLabelKeepsUnknownAgentIDs(t *testing.T) {
	got := kbFolderLabel("agents/deleted-agent-id/logs", map[string]string{})
	if want := "Agents/deleted-agent-id/logs"; got != want {
		t.Errorf("kbFolderLabel = %q, want %q", got, want)
	}
}
