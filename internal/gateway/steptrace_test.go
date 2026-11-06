package gateway

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/gateway/render"
)

type editRecorder struct {
	mu    sync.Mutex
	edits []string
	block chan struct{} // when set, an edit waits on it
}

func (r *editRecorder) edit(text string) {
	r.mu.Lock()
	block := r.block
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.edits = append(r.edits, text)
}

func (r *editRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.edits...)
}

func (r *editRecorder) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.all(); len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wanted %d edits, got %v", n, r.all())
	return nil
}

func quickTrace(header string, r *editRecorder) *stepTrace {
	tr := newStepTrace(header, r.edit)
	tr.interval = time.Millisecond
	tr.waitingInterval = time.Millisecond
	return tr
}

const (
	searchLine = "🔧 search_files(report)"
	readLine   = "🔧 read_file(notes/a.md)"
)

func TestStepTraceRendersRunningList(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Step(searchLine)
	r.waitFor(t, 1)
	tr.Step(readLine)
	got := r.waitFor(t, 2)
	want := "✓ 📚 Searched knowledge base: `report`\n⏳ 📄 Reading `notes/a.md`"
	if last := got[len(got)-1]; last != want {
		t.Errorf("got:\n%s\nwant:\n%s", last, want)
	}
}

func TestStepTraceKeepsHeader(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("Running agent **x**...", r)
	tr.Step(searchLine)
	if got := r.waitFor(t, 1)[0]; !strings.HasPrefix(got, "Running agent **x**...\n\n") {
		t.Errorf("header lost: %q", got)
	}
}

func TestStepTraceCollapsesOlderSteps(t *testing.T) {
	tr := newStepTrace("", func(string) {})
	tr.interval = time.Hour
	for i := 0; i < 8; i++ {
		tr.Step(searchLine)
	}
	tr.mu.Lock()
	text := tr.render()
	tr.mu.Unlock()
	tr.Close()
	if !strings.HasPrefix(text, "_+3 earlier_\n") {
		t.Errorf("older steps not collapsed:\n%s", text)
	}
	if n := strings.Count(text, "knowledge base"); n != stepTraceVisible {
		t.Errorf("showed %d steps, want %d", n, stepTraceVisible)
	}
}

func TestStepTraceCoalescesWithinInterval(t *testing.T) {
	r := &editRecorder{}
	tr := newStepTrace("", r.edit)
	tr.interval = 100 * time.Millisecond
	tr.Step(searchLine)
	r.waitFor(t, 1)
	for i := 0; i < 20; i++ {
		tr.Step(readLine)
	}
	got := r.waitFor(t, 2)
	time.Sleep(250 * time.Millisecond)
	got = r.all()
	if len(got) != 2 {
		t.Errorf("20 quick steps produced %d edits", len(got)-1)
	}
	if !strings.HasSuffix(got[len(got)-1], "Reading `notes/a.md`") {
		t.Errorf("the newest state was never flushed: %q", got[len(got)-1])
	}
	tr.Close()
}

// Step runs inline in the engine's tool loop.
func TestStepDoesNotBlockOnSlowEdit(t *testing.T) {
	release := make(chan struct{})
	r := &editRecorder{block: release}
	tr := quickTrace("", r)
	defer close(release)
	tr.Step(searchLine)
	time.Sleep(10 * time.Millisecond) // the flush is now stuck in edit

	done := make(chan struct{})
	go func() {
		tr.Step(readLine)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Step blocked on an in-flight edit")
	}
}

// The race this type exists to close: the final send clears the placeholder, so
// an edit finishing after it would post the old list below the answer.
func TestPauseWaitsForInFlightEditAndNothingFollows(t *testing.T) {
	release := make(chan struct{})
	r := &editRecorder{block: release}
	tr := quickTrace("", r)
	tr.Step(searchLine)
	time.Sleep(10 * time.Millisecond)
	tr.Step(readLine) // scheduled while the first flush is stuck

	paused := make(chan struct{})
	go func() {
		tr.Pause()
		close(paused)
	}()
	select {
	case <-paused:
		t.Fatal("Pause returned while an edit was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-paused
	n := len(r.all())
	time.Sleep(30 * time.Millisecond)
	if after := len(r.all()); after != n {
		t.Errorf("%d edits landed after Pause", after-n)
	}
}

func TestStepAfterPauseResumes(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Step(searchLine)
	r.waitFor(t, 1)
	tr.Pause()
	tr.Step(readLine)
	if got := r.waitFor(t, 2); !strings.Contains(got[1], "Reading") {
		t.Errorf("trace did not resume: %q", got[1])
	}
	tr.Close()
}

func TestWaitingPhraseRotatesThroughEveryPhrase(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.StartWaiting()
	got := r.waitFor(t, len(waitingPhrases))[:len(waitingPhrases)]
	tr.Close()
	for i, line := range got {
		if i > 0 && line == got[i-1] {
			t.Errorf("phrase did not change: %q", got)
		}
	}
	for _, phrase := range waitingPhrases {
		if !strings.Contains(strings.Join(got, "\n"), phrase) {
			t.Errorf("%q never appeared in %q", phrase, got)
		}
	}
}

func TestWaitingPhraseKeepsHeader(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("Running agent **x**...", r)
	tr.StartWaiting()
	got := r.waitFor(t, 1)[0]
	tr.Close()
	if !strings.HasPrefix(got, "Running agent **x**...\n\n⏳ _") {
		t.Errorf("got %q", got)
	}
}

func TestFirstStepEndsTheWaitingPhrase(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.waitingInterval = 20 * time.Millisecond
	tr.StartWaiting()
	r.waitFor(t, 1)
	tr.Step(searchLine)
	time.Sleep(100 * time.Millisecond)
	got := r.all()
	tr.Close()
	if last := got[len(got)-1]; !strings.Contains(last, "Searching knowledge base") {
		t.Errorf("a phrase replaced the step: %q", got)
	}
}

func TestPauseStopsTheWaitingPhrase(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.waitingInterval = 5 * time.Millisecond
	tr.StartWaiting()
	r.waitFor(t, 1)
	tr.Pause()
	n := len(r.all())
	time.Sleep(30 * time.Millisecond)
	if after := len(r.all()); after != n {
		t.Errorf("%d phrases landed after Pause", after-n)
	}
}

func TestStepAfterCloseIsIgnored(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Close()
	tr.Step(searchLine)
	tr.StartWaiting()
	time.Sleep(20 * time.Millisecond)
	if got := r.all(); len(got) != 0 {
		t.Errorf("edits after Close: %v", got)
	}
}

func TestNilTraceIsSafe(t *testing.T) {
	var tr *stepTrace
	tr.StartWaiting()
	tr.Step(searchLine)
	tr.Pause()
	tr.Close()
	if newStepTrace("", nil) != nil {
		t.Error("no editor should mean no trace")
	}
}

// Model-written queries and paths reach all three markup dialects.
func TestTraceSurvivesEveryRenderer(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Step("🔧 read_file(notes/my_file_name.md)")
	tr.Step("🔧 search_files(a*b [c] <d> ~e~ |f| _g_)")
	r.waitFor(t, 1)
	time.Sleep(10 * time.Millisecond)
	got := r.all()
	last := got[len(got)-1]
	tr.Close()

	for _, platform := range []string{"telegram", "discord", "slack"} {
		out := render.For(platform).Render(last)
		if !strings.Contains(out, "my_file_name") {
			t.Errorf("%s corrupted a path:\n%s", platform, out)
		}
		if strings.Contains(out, `\_`) {
			t.Errorf("%s shows an escape:\n%s", platform, out)
		}
		if strings.Contains(out, "```") {
			t.Errorf("%s rendered a code block:\n%s", platform, out)
		}
		if len(strings.Split(out, "\n")) < 2 {
			t.Errorf("%s collapsed the list onto one line:\n%s", platform, out)
		}
	}
}

func TestEveryWaitingPhraseRendersCleanly(t *testing.T) {
	for i := range waitingPhrases {
		out := render.For("telegram").Render(waitingLine(i))
		if !strings.HasSuffix(out, `\.\.\._`) || !strings.HasPrefix(out, "⏳ _") {
			t.Errorf("phrase %d rendered as %q", i, out)
		}
	}
}
