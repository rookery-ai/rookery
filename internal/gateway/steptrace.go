package gateway

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/rookery-ai/rookery/internal/coder"
)

const (
	// stepTraceInterval is the minimum gap between step edits. Telegram allows
	// about 20 messages a minute in a group, and a rejected edit is simply lost.
	stepTraceInterval = 3 * time.Second
	// stepTraceVisible is how many steps the message shows.
	stepTraceVisible = 5
	// waitingInterval is how often the waiting phrase changes before the first
	// step. A model answering without tools can take minutes, so this has to
	// stay well inside the same limit for the whole of that time.
	waitingInterval = 5 * time.Second
)

// waitingPhrases rotate in the placeholder until there is a real step to show.
var waitingPhrases = []string{
	"Thinking",
	"Reasoning",
	"Taking a look",
	"Researching",
	"Figuring something out",
}

func waitingLine(i int) string {
	return "⏳ _" + waitingPhrases[i%len(waitingPhrases)] + "..._"
}

// randomWaitingLine is what a placeholder opens with.
func randomWaitingLine() string {
	return waitingLine(rand.IntN(len(waitingPhrases)))
}

type tracedStep struct{ doing, did string }

// stepTrace keeps a chat-app placeholder showing what one turn is doing: a
// rotating phrase until the first tool call, then a running list of steps.
//
// Two locks, because the two jobs conflict. A step arrives inline in the
// engine's tool loop and must not wait on a network call, so Step takes only mu
// and leaves the edit to a timer. A flush holds flushMu across its edit, which
// is what lets Pause wait one out: the final send clears the placeholder id, so
// an edit landing after it would post the stale list as a NEW message below the
// answer.
//
// All methods are safe on a nil receiver, for a caller with no way to edit.
type stepTrace struct {
	header          string
	edit            func(string)
	interval        time.Duration
	waitingInterval time.Duration

	flushMu sync.Mutex

	mu        sync.Mutex
	steps     []tracedStep
	timer     *time.Timer
	lastFlush time.Time
	phrase    int
	waiting   bool
	paused    bool
	closed    bool
}

func newStepTrace(header string, edit func(string)) *stepTrace {
	if edit == nil {
		return nil
	}
	return &stepTrace{
		header:          header,
		edit:            edit,
		interval:        stepTraceInterval,
		waitingInterval: waitingInterval,
		phrase:          rand.IntN(len(waitingPhrases)),
	}
}

// StartWaiting begins rotating the waiting phrase. The first Step ends it.
func (t *stepTrace) StartWaiting() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.paused || len(t.steps) > 0 {
		return
	}
	t.waiting = true
	t.timer = time.AfterFunc(t.waitingInterval, t.rotate)
}

func (t *stepTrace) rotate() {
	defer recoverFlush()
	t.flushMu.Lock()
	defer t.flushMu.Unlock()

	t.mu.Lock()
	if !t.waiting || t.paused || t.closed {
		t.mu.Unlock()
		return
	}
	t.phrase++
	text := waitingLine(t.phrase)
	if t.header != "" {
		text = t.header + "\n\n" + text
	}
	t.timer = time.AfterFunc(t.waitingInterval, t.rotate)
	t.mu.Unlock()

	t.edit(text)
}

// Step records a milestone and schedules an edit. It resumes a trace stopped by
// Pause: a message sent mid-run (a notify from browser_wait) consumes the
// placeholder, and the steps after it start a new one.
func (t *stepTrace) Step(line string) {
	if t == nil {
		return
	}
	doing, did := coder.FriendlyStep(line)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || doing == "" {
		return
	}
	t.steps = append(t.steps, tracedStep{doing, did})
	t.paused = false
	if t.waiting {
		// The pending timer would rotate the phrase, not show this step.
		t.waiting = false
		t.stopTimer()
	}
	if t.timer != nil {
		return // a pending flush will carry this step too
	}
	wait := t.interval - time.Since(t.lastFlush)
	if wait < 0 {
		wait = 0
	}
	t.timer = time.AfterFunc(wait, t.flush)
}

func (t *stepTrace) flush() {
	defer recoverFlush()
	t.flushMu.Lock()
	defer t.flushMu.Unlock()

	t.mu.Lock()
	t.timer = nil
	if t.paused || t.closed {
		t.mu.Unlock()
		return
	}
	t.lastFlush = time.Now()
	text := t.render()
	t.mu.Unlock()

	t.edit(text)
}

func recoverFlush() {
	if r := recover(); r != nil {
		slog.Error("gateway: step trace edit panicked", "panic", r)
	}
}

// render requires mu.
func (t *stepTrace) render() string {
	var b strings.Builder
	if t.header != "" {
		b.WriteString(t.header + "\n\n")
	}
	shown := t.steps
	if hidden := len(shown) - stepTraceVisible; hidden > 0 {
		fmt.Fprintf(&b, "_+%d earlier_\n", hidden)
		shown = shown[hidden:]
	}
	lines := make([]string, len(shown))
	for i, s := range shown {
		lines[i] = "✓ " + s.did
	}
	lines[len(lines)-1] = "⏳ " + shown[len(shown)-1].doing
	b.WriteString(strings.Join(lines, "\n"))
	return b.String()
}

// stopTimer requires mu.
func (t *stepTrace) stopTimer() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// halt stops future edits and returns once any in-flight one has finished.
func (t *stepTrace) halt(closing bool) {
	t.mu.Lock()
	t.paused = true
	t.waiting = false
	t.closed = t.closed || closing
	t.stopTimer()
	t.mu.Unlock()

	t.flushMu.Lock()
	defer t.flushMu.Unlock() //nolint:staticcheck // the empty section is the barrier
}

// Pause stops the trace. After it returns no earlier edit can reach the platform,
// so the caller may send its message.
func (t *stepTrace) Pause() {
	if t == nil {
		return
	}
	t.halt(false)
}

// Close ends the trace for good. Handlers defer it so an error returned to the
// dispatcher, which sends after the handler has gone, cannot race an edit.
func (t *stepTrace) Close() {
	if t == nil {
		return
	}
	t.halt(true)
}
