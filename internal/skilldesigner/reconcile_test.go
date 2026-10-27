package skilldesigner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSkillMD = "---\nname: my-skill\ndescription: Does a thing. Use when asked to do the thing.\n---\nBody.\n"

func TestReconcileSkillBlockKeepsABuildOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(validSkillMD), 0o640); err != nil {
		t.Fatal(err)
	}
	proceed, caveat := reconcileSkillBlock(dir, "the live run needs the real package")
	if !proceed {
		t.Fatal("a build with a valid SKILL.md on disk must proceed despite the block")
	}
	if !strings.Contains(caveat, "the live run needs the real package") {
		t.Errorf("caveat must carry the builder's reason, got %q", caveat)
	}
}

func TestReconcileSkillBlockDiscardsWhenNothingBuilt(t *testing.T) {
	if proceed, _ := reconcileSkillBlock(t.TempDir(), "stuck"); proceed {
		t.Fatal("an empty staging dir has nothing to keep")
	}
}

func TestReconcileSkillBlockDiscardsUnparseableSkill(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("just prose, no frontmatter\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if proceed, _ := reconcileSkillBlock(dir, "stuck"); proceed {
		t.Fatal("a SKILL.md without frontmatter is not a finished skill")
	}
}

// The reported failure: the builder wrote the skill, then emitted a [BLOCKED] it
// argued against itself. The skill must reach review, caveated, not be deleted.
func TestRunGeneration_BlockWithSkillOnDiskReachesReview(t *testing.T) {
	fake := newFakeSkillCoder(t, `import sys
if 'auditing a newly generated skill' in ' '.join(sys.argv):
    print("Verdict: ✅ safe to save")
    sys.exit(0)
with open('SKILL.md', 'w') as f:
    f.write("---\nname: my-skill\ndescription: Does a thing. Use when asked to do the thing.\n---\nBody.\n")
print("[BLOCKED]\nA live run needs the real package at call time.\n[/BLOCKED]")
`)
	flow, workspaceID := newGenSkillFlow(t, fake)
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateVerifying {
		t.Fatalf("state = %+v, want StateVerifying", sess)
	}
	if sess.PendingSkillMD == "" {
		t.Error("the built SKILL.md must be kept as the pending skill")
	}
	if !strings.HasPrefix(msg, "Note: the builder reported a problem it could not resolve: A live run needs the real package") {
		t.Errorf("review message must open with the block as a caveat, got %q", msg)
	}
}

// One build is reconstructable from the log: every line carries the same build_id,
// including the coder result and the decision in between start and finish.
func TestRunGenerationLogged_LinesShareABuildID(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	fake := newFakeSkillCoder(t, `print("[BLOCKED]I couldn't build this.[/BLOCKED]")
`)
	flow, workspaceID := newGenSkillFlow(t, fake)
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")
	if _, _, _, err := flow.runGenerationLogged(context.Background(), workspaceID); err != nil {
		t.Fatal(err)
	}

	// "what did the build call, and why did it take nine minutes" must be answerable
	// from the log: the coder-returned line carries the tool summary, as chat's does.
	if !strings.Contains(buf.String(), `"msg":"skilldesigner: coder returned"`) || !strings.Contains(buf.String(), `"tools":`) {
		t.Errorf("coder-returned line lacks the tools summary:\n%s", buf.String())
	}

	ids := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		msg, _ := rec["msg"].(string)
		if id, ok := rec["build_id"].(string); ok && strings.HasPrefix(msg, "skilldesigner: ") {
			ids[msg] = id
		}
	}
	want := []string{"skilldesigner: build start", "skilldesigner: coder returned", "skilldesigner: build decision", "skilldesigner: build finished"}
	for _, m := range want {
		if ids[m] == "" {
			t.Fatalf("missing %q with a build_id; got %v", m, ids)
		}
		if ids[m] != ids[want[0]] {
			t.Errorf("%q has build_id %q, want %q", m, ids[m], ids[want[0]])
		}
	}
}

// The install tools refuse during a skill build by reading ROOKERY_BUILD_KIND from the
// coder's env; the skill designer is the one place that must set it.
func TestRunGeneration_SetsTheSkillBuildMarker(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "kind.txt")
	fake := newFakeSkillCoder(t, fmt.Sprintf("import os\nopen(%q, 'w').write(os.environ.get('ROOKERY_BUILD_KIND', ''))\n", probe))
	flow, workspaceID := newGenSkillFlow(t, fake)
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")
	if _, _, _, err := flow.runGeneration(context.Background(), workspaceID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "skill" {
		t.Fatalf("ROOKERY_BUILD_KIND = %q in the build coder's env, want \"skill\"", got)
	}
}
