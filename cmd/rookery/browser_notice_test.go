package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/auth"
	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/db"
)

func noticeDB(t *testing.T) (*db.DB, []string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "rookery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var ids []string
	for _, name := range []string{"Alpha", "Beta"} {
		w, err := auth.CreateWorkspace(d, name, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, w.ID)
	}
	return d, ids
}

func inboxCount(t *testing.T, d *db.DB, workspaceID string) int {
	t.Helper()
	msgs, err := d.ListInboxMessages(workspaceID, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(msgs)
}

var stranded = browser.Staleness{
	Outdated: true,
	Reason:   "the browser on this machine was installed for Playwright 1.62.1, but this Rookery build needs 1.64.1 — run `rookery browser install`",
	Needed:   "1.64.1",
}

// Every workspace hears once, and a restart before the owner acts does not
// repeat it — otherwise the inbox fills with one copy per restart.
func TestOutdatedBrowserNoticeReachesEveryWorkspaceOnce(t *testing.T) {
	d, ids := noticeDB(t)
	noticeOutdatedBrowser(d, stranded)
	noticeOutdatedBrowser(d, stranded)
	for _, id := range ids {
		if n := inboxCount(t, d, id); n != 1 {
			t.Fatalf("workspace %s got %d notices, want exactly 1", id, n)
		}
	}
	msgs, _ := d.ListInboxMessages(ids[0], 50, 0)
	if msgs[0].Source != "system" || !strings.Contains(msgs[0].Body, "rookery browser install") {
		t.Errorf("notice should be a system message naming the fix, got source=%q body=%q", msgs[0].Source, msgs[0].Body)
	}
}

// A later upgrade that strands the browser again is a new event.
func TestALaterStrandingIsAnnouncedAgain(t *testing.T) {
	d, ids := noticeDB(t)
	noticeOutdatedBrowser(d, stranded)
	next := stranded
	next.Needed = "1.66.0"
	noticeOutdatedBrowser(d, next)
	if n := inboxCount(t, d, ids[0]); n != 2 {
		t.Fatalf("got %d notices across two strandings, want 2", n)
	}
}

func TestACurrentBrowserSendsNothing(t *testing.T) {
	d, ids := noticeDB(t)
	noticeOutdatedBrowser(d, browser.Staleness{})
	if n := inboxCount(t, d, ids[0]); n != 0 {
		t.Fatalf("a current browser produced %d notices", n)
	}
}
