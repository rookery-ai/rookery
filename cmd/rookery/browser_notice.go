package main

import (
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/db"
)

// browserNoticeKey records the driver version an outdated-browser notice was
// last sent for, so a restart does not repeat it while the owner has not yet
// acted — and a LATER stranding, by another upgrade, is announced again.
const browserNoticeKey = "browser.outdated_notice"

// noticeOutdatedBrowser tells every workspace's inbox that the browser an
// upgrade stranded needs reinstalling.
//
// `serve` cannot reinstall it: the packaged systemd unit runs with
// ProtectHome=read-only, so the cache the server reads is not one it can
// write. Package-manager and container upgrades also never run `rookery
// upgrade`, which is where the repair happens. So the server's job is to make
// sure the owner hears about it — a startup log line is read by nobody, and the
// symptom (agents quietly losing their browser tools) is invisible otherwise.
//
// Inbox only, not chat: it is a maintenance notice about the install, not
// about anything a workspace did, and every workspace would otherwise receive
// the same chat message at once. Failures are logged, never returned — a
// notice must not stop the server starting.
func noticeOutdatedBrowser(d *db.DB, st browser.Staleness) {
	if !st.Outdated {
		return
	}
	if sent, err := d.GetSystemSetting(browserNoticeKey); err == nil && sent == st.Needed {
		return
	} else if err != nil && !errors.Is(err, db.ErrNotFound) {
		slog.Warn("browser: could not read notice state", "err", err)
		return
	}

	workspaces, err := d.ListWorkspaces()
	if err != nil {
		slog.Warn("browser: could not list workspaces for the outdated notice", "err", err)
		return
	}
	body := "⚠️ Action required — " + st.Reason + ". Until then agents cannot read " +
		"JavaScript-rendered pages or use the browser to sign in, fill forms or click through a site."
	for _, w := range workspaces {
		if err := d.CreateInboxMessage(&db.InboxMessage{
			ID:          uuid.New().String(),
			WorkspaceID: w.ID,
			Source:      "system",
			Body:        body,
		}); err != nil {
			slog.Warn("browser: outdated notice not written", "workspace_id", w.ID, "err", err)
		}
	}
	if err := d.SetSystemSetting(browserNoticeKey, st.Needed); err != nil {
		slog.Warn("browser: could not record the outdated notice", "err", err)
	}
}
