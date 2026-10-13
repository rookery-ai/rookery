package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// The agent page's Connections card rendered the raw provider slug, so a demo
// install showed "Google_calendar" and "Github" beside a services page that says
// "Google Calendar" and "GitHub". The DTO carries the registry's label; an
// unknown provider falls back to its slug rather than to nothing.
func TestAPIAgentDetailCarriesProviderLabels(t *testing.T) {
	s, d := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	for _, c := range []db.ServiceConnection{
		{ID: "c1", WorkspaceID: wsID, Provider: "google_calendar", AccountLabel: "work"},
		{ID: "c2", WorkspaceID: wsID, Provider: "no_such_provider", AccountLabel: "x"},
	} {
		if err := d.InsertServiceConnection(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"provider_label":"Google Calendar"`) {
		t.Errorf("registry label missing for google_calendar: %s", body)
	}
	if !contains(body, `"provider_label":"no_such_provider"`) {
		t.Errorf("an unknown provider must fall back to its slug: %s", body)
	}
}
