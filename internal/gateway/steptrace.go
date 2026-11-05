package gateway

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rookery-ai/rookery/internal/coder"
)

const (
	// stepTraceInterval is the minimum gap between edits. Telegram allows about
	// 20 messages a minute in a group, and a rejected edit is simply lost.
	stepTraceInterval = 3 * time.Second
	// stepTraceVisible is how many steps the message shows.
	stepTraceVisible = 5
)

type tracedStep struct{ doing, did string }

// stepTrace keeps a chat-app placeholder showing the tool steps of one turn.
//
// Two locks, because the two jobs conflict. A step arrives inline in the
// engine's tool loop and must not wait on a network call, so Step takes only mu
// and leaves the edit to a timer. A flush holds flushMu across its edit, which
// is what lets Summary wait one out: the final send clears the placeholder id,
// so an edit landing after it would post the stale list as a NEW message below
// the answer.
//
// All methods are safe on a nil receiver, for a caller with no way to edit.
type stepTrace struct {
	header   string
	edit     func(string)
	interval time.Duration
	started  time.Time

	flushMu sync.Mutex

	mu         sync.Mutex
	steps      []tracedStep
	summarised int // len(steps) at the last Summary
	timer      *time.Timer
	lastFlush  time.Time
	paused     bool
	closed     bool
}

func newStepTrace(header string, edit func(string)) *stepTrace {
	if edit == nil {
		return nil
	}
	return &stepTrace{header: header, edit: edit, interval: stepTraceInterval, started: time.Now()}
}

// Step records a milestone and schedules an edit. It resumes a trace paused by
// Summary: a message sent mid-run (a notify from browser_wait) consumes the
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
	defer func() {
		if r := recover(); r != nil {
			slog.Error("gateway: step trace flush panicked", "panic", r)
		}
	}()
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
	for i, s := range shown {
		if i == len(shown)-1 {
			b.WriteString("⏳ " + s.doing + "\n")
		} else {
			b.WriteString("✓ " + s.did + "\n")
		}
	}
	fmt.Fprintf(&b, "_step %d · %s_", len(t.steps), formatElapsed(time.Since(t.started)))
	return b.String()
}

// halt stops future flushes and returns once any in-flight one has finished.
func (t *stepTrace) halt(closing bool) {
	t.mu.Lock()
	t.paused = true
	t.closed = t.closed || closing
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()

	t.flushMu.Lock()
	defer t.flushMu.Unlock() //nolint:staticcheck // the empty section is the barrier
}

// Summary pauses the trace and returns the line to show above a message, or ""
// when no step has happened since the last Summary. After it returns no earlier
// step can reach the platform, so the caller may send its message.
func (t *stepTrace) Summary() string {
	if t == nil {
		return ""
	}
	t.halt(false)
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.steps)
	if n == t.summarised {
		return ""
	}
	t.summarised = n
	noun := "steps"
	if n == 1 {
		noun = "step"
	}
	return fmt.Sprintf("_✓ %d %s · %s_", n, noun, formatElapsed(time.Since(t.started)))
}

// Close ends the trace for good. Handlers defer it so an error returned to the
// dispatcher, which sends after the handler has gone, cannot race a flush.
func (t *stepTrace) Close() {
	if t == nil {
		return
	}
	t.halt(true)
}

// withSummary puts the trace's summary above text, if there is one.
func (t *stepTrace) withSummary(text string) string {
	if s := t.Summary(); s != "" {
		return s + "\n\n" + text
	}
	return text
}

func formatElapsed(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
