package agentdesigner

import (
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

func TestAutoBindTargets(t *testing.T) {
	avail := []db.ServiceConnection{
		{ID: "g", Provider: "google", AccountLabel: "personal", AccountIdentity: "me@x.com"},
		{ID: "s", Provider: "stripe", AccountLabel: "kroute"},
		{ID: "n", Provider: "notion", AccountLabel: "personal"},
	}
	// explicit header wins
	if ids, apply := AutoBindTargets("# Connections: google/personal\n", avail, nil, []string{"s"}); !apply || len(ids) != 1 || ids[0] != "g" {
		t.Fatalf("explicit header: %v %v", ids, apply)
	}
	// no header + nothing bound + build used google+stripe → bind those two
	ids, apply := AutoBindTargets("no header here", avail, nil, []string{"g", "s", "unknown"})
	if !apply || len(ids) != 2 {
		t.Fatalf("auto-bind from build usage: %v %v", ids, apply)
	}
	// no header + already bound → leave untouched
	if _, apply := AutoBindTargets("no header", avail, []db.ServiceConnection{{ID: "n"}}, []string{"g"}); apply {
		t.Fatal("must not clobber existing bindings")
	}
	// no header + build used nothing → don't touch
	if _, apply := AutoBindTargets("no header", avail, nil, nil); apply {
		t.Fatal("nothing to bind")
	}
	// header "none" → apply empty (bind none)
	if ids, apply := AutoBindTargets("# Connections: none\n", avail, nil, []string{"g"}); !apply || len(ids) != 0 {
		t.Fatalf("none header should bind nothing: %v %v", ids, apply)
	}
}
