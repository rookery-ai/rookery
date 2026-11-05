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
	last := got[len(got)-1]
	for _, want := range []string{
		"✓ 📚 Searched knowledge base: `report`",
		"⏳ 📄 Reading `notes/a.md`",
		"_step 2 · 0:00_",
	} {
		if !strings.Contains(last, want) {
			t.Errorf("missing %q in:\n%s", want, last)
		}
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
	if !strings.Contains(text, "_+3 earlier_") {
		t.Errorf("older steps not collapsed:\n%s", text)
	}
	if n := strings.Count(text, "knowledge base"); n != stepTraceVisible {
		t.Errorf("showed %d steps, want %d", n, stepTraceVisible)
	}
	if !strings.Contains(text, "_step 8 ·") {
		t.Errorf("count should cover every step:\n%s", text)
	}
}

func TestStepTraceCoalescesWithinInterval(t *testing.T) {
	r := &editRecorder{}
	tr := newStepTrace("", r.edit)
	tr.interval = 100 * time.Millisecond
	for i := 0; i < 20; i++ {
		tr.Step(searchLine)
	}
	got := r.waitFor(t, 1)
	time.Sleep(250 * time.Millisecond)
	got = r.all()
	if len(got) > 2 {
		t.Errorf("20 quick steps produced %d edits", len(got))
	}
	if !strings.Contains(got[len(got)-1], "_step 20 ·") {
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
// a flush finishing after it would post the old list below the answer.
func TestSummaryWaitsForInFlightFlushAndNothingFollows(t *testing.T) {
	release := make(chan struct{})
	r := &editRecorder{block: release}
	tr := quickTrace("", r)
	tr.Step(searchLine)
	time.Sleep(10 * time.Millisecond)
	tr.Step(readLine) // scheduled while the first flush is stuck

	summarised := make(chan string)
	go func() { summarised <- tr.Summary() }()
	select {
	case <-summarised:
		t.Fatal("Summary returned while a flush was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if got := <-summarised; !strings.Contains(got, "2 steps") {
		t.Errorf("summary = %q", got)
	}
	n := len(r.all())
	time.Sleep(30 * time.Millisecond)
	if after := len(r.all()); after != n {
		t.Errorf("%d edits landed after Summary", after-n)
	}
}

func TestStepAfterSummaryResumes(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Step(searchLine)
	r.waitFor(t, 1)
	if tr.Summary() == "" {
		t.Fatal("no summary after a step")
	}
	if got := tr.Summary(); got != "" {
		t.Errorf("a second summary with no new step = %q", got)
	}
	tr.Step(readLine)
	if got := r.waitFor(t, 2); !strings.Contains(got[1], "_step 2 ·") {
		t.Errorf("trace did not resume: %q", got[1])
	}
	if got := tr.Summary(); !strings.Contains(got, "2 steps") {
		t.Errorf("summary should be cumulative: %q", got)
	}
}

func TestNoStepsMeansNoSummary(t *testing.T) {
	tr := quickTrace("", &editRecorder{})
	if got := tr.withSummary("answer"); got != "answer" {
		t.Errorf("got %q", got)
	}
}

func TestSummaryUsesSingularForOneStep(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Step(searchLine)
	if got := tr.Summary(); !strings.HasPrefix(got, "_✓ 1 step ·") {
		t.Errorf("summary = %q", got)
	}
}

func TestStepAfterCloseIsIgnored(t *testing.T) {
	r := &editRecorder{}
	tr := quickTrace("", r)
	tr.Close()
	tr.Step(searchLine)
	time.Sleep(20 * time.Millisecond)
	if got := r.all(); len(got) != 0 {
		t.Errorf("edits after Close: %v", got)
	}
}

func TestNilTraceIsSafe(t *testing.T) {
	var tr *stepTrace
	tr.Step(searchLine)
	tr.Close()
	if got := tr.withSummary("x"); got != "x" {
		t.Errorf("got %q", got)
	}
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
	got := r.waitFor(t, 1)
	time.Sleep(10 * time.Millisecond)
	got = r.all()
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
		lines := strings.Split(out, "\n")
		if len(lines) < 3 {
			t.Errorf("%s collapsed the list onto one line:\n%s", platform, out)
		}
	}
}
