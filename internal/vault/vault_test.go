package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsUserMutationProtected(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"agents/abc/AGENT.md", true},
		{"agents", true},
		{"/chats/x.md", true},
		{"inbox/n.md", true},
		{"skills/s/SKILL.md", true},
		{"reminders/r.md", true},
		{".kb/db-export/x.json", true},
		{"notes/../chats/x.md", true}, // resolves into chats/
		{"notes/mine.md", false},
		{"memory/USER.md", false},
		{"assets/pic.png", false},
		{"README.md", false},
		{"", false},
		{"agentsomething/x.md", false}, // not the agents dir
	}
	for _, c := range cases {
		if got := IsUserMutationProtected(c.rel); got != c.want {
			t.Errorf("IsUserMutationProtected(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestResolveRejectsEscapes(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	root := v.Root(user)

	bad := []string{
		"../escape.md",
		"../../etc/passwd",
		"notes/../../escape.md",
		"a/b/../../../escape.md",
		"/../escape.md",
	}
	for _, p := range bad {
		got, err := v.Resolve(user, p)
		if err == nil {
			t.Errorf("Resolve(%q) = %q, want escape error", p, got)
		} else if !errors.Is(err, ErrEscapes) {
			t.Errorf("Resolve(%q) error = %v, want ErrEscapes", p, err)
		}
	}

	// Well-formed paths resolve inside the root, including ones with interior
	// "../" that stays contained and a leading slash treated as vault-relative.
	good := map[string]string{
		"notes/x.md":         filepath.Join(root, "notes", "x.md"),
		"/notes/x.md":        filepath.Join(root, "notes", "x.md"),
		"a/b/../c.md":        filepath.Join(root, "a", "c.md"),
		"./README.md":        filepath.Join(root, "README.md"),
		"agents/x/logs/r.md": filepath.Join(root, "agents", "x", "logs", "r.md"),
	}
	for in, want := range good {
		got, err := v.Resolve(user, in)
		if err != nil {
			t.Errorf("Resolve(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteReadDeleteRoundTrip(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"

	if err := v.WriteNote(user, "notes/hello.md", []byte("# hi")); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	got, err := v.ReadNote(user, "notes/hello.md")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if string(got) != "# hi" {
		t.Fatalf("ReadNote = %q, want %q", got, "# hi")
	}
	if err := v.Delete(user, "notes/hello.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := v.ReadNote(user, "notes/hello.md"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadNote after delete: err = %v, want NotExist", err)
	}
}

func TestListHidesInternalDir(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	if err := v.EnsureScaffold(user); err != nil {
		t.Fatalf("EnsureScaffold: %v", err)
	}
	nodes, err := v.List(user, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, n := range nodes {
		if n.Name == InternalDir {
			t.Fatalf("List exposed internal dir %q", InternalDir)
		}
	}
	// README.md should be present and directories should sort first.
	var sawReadme, sawDirFirst bool
	for i, n := range nodes {
		if n.Name == "README.md" {
			sawReadme = true
		}
		if i == 0 && n.IsDir {
			sawDirFirst = true
		}
	}
	if !sawReadme {
		t.Errorf("README.md not listed; got %v", nodeNames(nodes))
	}
	if !sawDirFirst {
		t.Errorf("expected a directory first; got %v", nodeNames(nodes))
	}
}

func TestDeleteRefusesProtectedPaths(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	if err := v.EnsureScaffold(user); err != nil {
		t.Fatalf("EnsureScaffold: %v", err)
	}
	if err := v.Delete(user, ""); err == nil {
		t.Errorf("Delete(root) succeeded, want refusal")
	}
	if err := v.Delete(user, InternalDir); err == nil {
		t.Errorf("Delete(.kb) succeeded, want refusal")
	}
}

func nodeNames(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Name)
		b.WriteString(" ")
	}
	return b.String()
}
