package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveSchedulePass runs the pass against a real coder, because fake answers can only
// prove the guards hold, never that a model produces edits that get past them. It is a
// paid call, so it needs ROOKERY_LIVE_CODER=1.
func TestLiveSchedulePass(t *testing.T) {
	if os.Getenv("ROOKERY_LIVE_CODER") != "1" {
		t.Skip("set ROOKERY_LIVE_CODER=1 to run against a real coder")
	}
	const md = `# Suggested schedule: 0 9 * * *
# Skills: none

Every day at 9am, fetch the sales report from https://example.com/report and summarise it.

## Steps

1. Read the last 30 days of daily sales figures.
2. Compare today's total with yesterday's run, saved in state.
3. If the request fails, retry every 5 minutes, up to 3 times.
4. Send the summary.

The schedule (` + "`0 9 * * *`" + `) is read in the owner's local time.
`
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, md)
	s.EnableScheduleProseSync()

	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "0 8 * * 1"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(scheduleProseTimeout)
	for time.Now().Before(deadline) {
		if st := s.scheduleSyncStatus(a.ID); st != nil && st.State != "running" {
			got := readAgentMD(t, path)
			t.Logf("status: %+v\nfile:\n%s", *st, got)
			if st.State != "done" {
				t.Fatalf("pass failed: %s", st.Error)
			}
			if strings.Contains(got, "Every day at 9am") {
				t.Error("the sentence stating the old timing survived")
			}
			for _, keep := range []string{"last 30 days", "every 5 minutes", "`0 8 * * 1`", "# Skills: none"} {
				if !strings.Contains(got, keep) {
					t.Errorf("%q was lost", keep)
				}
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("pass did not finish")
}
