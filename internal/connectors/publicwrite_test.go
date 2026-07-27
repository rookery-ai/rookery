package connectors

import "testing"

// A public_write action publishes irreversibly to a public audience, so it is by
// definition also a write. If a data file ever sets public_write without mutating,
// the build-time guard would let it run during agent generation — posting for real
// while the user believes they are only testing.
//
// Currently vacuous: no bundled action sets public_write yet. It becomes load-bearing
// the moment the social publishing providers land, which is exactly when a mistake
// here would be most expensive.
func TestPublicWriteImpliesMutating(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, p := range reg.ProviderNames() {
		for _, a := range reg.Actions(p) {
			if a.PublicWrite && !a.Mutating {
				t.Errorf("%s: action %q is public_write but not mutating — the build-time "+
					"guard would not block it during generation", p, a.Name)
			}
		}
	}
}

// Existing team-tool comment actions (Jira, Trello, Zendesk, ClickUp…) are
// deliberately NOT public_write: they are mutating, but the audience is a private
// workspace and the action is reversible. Marking them would make enabling the gate
// silently change behaviour for integrations that have nothing to do with social
// publishing. This test pins that scoping decision so it is not "fixed" by accident.
func TestTeamCommentActionsAreNotPublicWrite(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range []string{
		"jira_add_comment", "trello_add_comment", "zendesk_add_comment",
		"clickup_add_comment", "asana_add_comment",
	} {
		found := false
		for _, p := range reg.ProviderNames() {
			a, ok := reg.Action(p, name)
			if !ok {
				continue
			}
			found = true
			if a.PublicWrite {
				t.Errorf("%q is marked public_write — team comments are private and "+
					"reversible; the gate is for public broadcast", name)
			}
		}
		if !found {
			t.Errorf("action %q not found — this test's premise has drifted", name)
		}
	}
}
