package agentrunner

import (
	"encoding/json"
	"sync"
	"time"
)

// maxTranscriptBytes bounds one run's stored transcript. Runs are kept
// indefinitely, so an uncapped transcript makes the database grow with the
// square of how chatty the agent is.
//
// Over the budget the OLDEST events are dropped and a marker takes their place,
// the same policy the live progress buffer uses: the end of a run is where it
// went wrong, and a truncated tail is the half you were reading it for.
const maxTranscriptBytes = 64 * 1024

// Event kinds. Deliberately few — this is a debugging record, not a structured
// log with a schema to maintain.
const (
	// EventProgress is a milestone the coder reported as it worked: a tool call,
	// a verification nudge, delivered [CHAT] output.
	EventProgress = "progress"
	// EventCoder is one turn of the coder's own raw response.
	EventCoder = "coder"
	// EventTruncated stands in for events dropped to the byte cap.
	EventTruncated = "truncated"
	// EventSummary is the closing note about the run's tool usage.
	EventSummary = "summary"
)

// RunEvent is one entry in a run's transcript.
type RunEvent struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// transcriptCollector accumulates a run's events in the order they happened.
//
// Progress milestones and coder turns are interleaved into ONE list rather than
// kept as two, because the question this record answers is "what did it do, in
// what order" — and two lists reunited by timestamp afterwards is the same
// thing with a way to get it wrong.
//
// Guarded by a mutex. Today every caller runs on the run goroutine, so it is
// not strictly required; it costs three lines and removes the need for whoever
// next wires a progress sink to reason about which goroutine calls it.
type transcriptCollector struct {
	mu     sync.Mutex
	events []RunEvent
}

func (t *transcriptCollector) add(kind, text string) {
	if text == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, RunEvent{Kind: kind, At: time.Now().UTC(), Text: text})
}

// wrap returns a progress sink that records every milestone and then forwards
// it to `next`. Forwarding is what keeps the live SSE view working; recording
// is what makes the same information survive the run.
//
// Returns a non-nil func even when next is nil: a scheduled run supplies no
// OnProgress at all, and that is exactly the run whose transcript is most
// wanted, since nobody was watching it happen.
func (t *transcriptCollector) wrap(next SendFunc) SendFunc {
	return func(msg string) {
		t.add(EventProgress, msg)
		if next != nil {
			next(msg)
		}
	}
}

// progressLines returns just the milestones, for the vault run note's "Tool
// calls" section. The note is markdown a person (or an agent) reads, so it gets
// the human-readable line the coder already produced rather than the JSON the
// database stores.
func (t *transcriptCollector) progressLines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for _, e := range t.events {
		if e.Kind == EventProgress {
			out = append(out, e.Text)
		}
	}
	return out
}

// activityLines renders the WHOLE transcript for the run's knowledge-base note
// — every kind, not just the tool calls.
//
// The note is what the run panel now links to as "the full log", having stopped
// reprinting the activity inline. That link has to be true: the note used to
// carry progress milestones only, so a coder turn was visible in the panel and
// nowhere else, and moving the panel to a link would have made those turns
// unreachable rather than relocated.
//
// A progress line already carries its own 🔧 marker, so it is emitted bare;
// the other kinds are labelled, because "coder" and "summary" are otherwise
// indistinguishable from each other in a flat list.
func (t *transcriptCollector) activityLines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.events))
	for _, e := range t.events {
		switch e.Kind {
		case EventProgress:
			out = append(out, e.Text)
		case EventCoder:
			out = append(out, "**coder:** "+e.Text)
		case EventSummary:
			out = append(out, "**summary:** "+e.Text)
		default:
			out = append(out, e.Text)
		}
	}
	return out
}

// encode renders the transcript as JSON, dropping the oldest events if it does
// not fit the budget.
//
// Returns "" for an empty transcript rather than "[]": the column defaults to
// empty and a run that recorded nothing should be indistinguishable from one
// that predates the column, since in both cases there is nothing to show.
func (t *transcriptCollector) encode() string {
	t.mu.Lock()
	events := append([]RunEvent(nil), t.events...)
	t.mu.Unlock()

	if len(events) == 0 {
		return ""
	}
	for {
		out, err := json.Marshal(events)
		if err != nil {
			return ""
		}
		if len(out) <= maxTranscriptBytes {
			return string(out)
		}
		// Drop from the front. The marker is rebuilt rather than accumulated so
		// repeated trimming leaves one truncation notice, not a stack of them.
		trimmed := events
		if trimmed[0].Kind == EventTruncated {
			trimmed = trimmed[1:]
		}
		if len(trimmed) <= 1 {
			// A single event larger than the whole budget: keep the marker and
			// a clipped copy rather than returning nothing at all.
			return clippedSingleEvent(trimmed)
		}
		drop := len(trimmed)/4 + 1
		if drop >= len(trimmed) {
			drop = len(trimmed) - 1
		}
		kept := trimmed[drop:]
		events = append([]RunEvent{{
			Kind: EventTruncated,
			At:   kept[0].At,
			Text: "earlier events dropped to fit the transcript size limit",
		}}, kept...)
	}
}

// clippedSingleEvent handles the pathological case of one event that alone
// exceeds the budget — a coder turn that emitted an enormous blob. Cutting the
// text is honest here in a way that returning "" would not be.
func clippedSingleEvent(events []RunEvent) string {
	if len(events) == 0 {
		return ""
	}
	e := events[0]
	// Leave headroom for the JSON envelope and the notice.
	limit := maxTranscriptBytes / 2
	if len(e.Text) > limit {
		e.Text = e.Text[:limit] + "\n… truncated to fit the transcript size limit"
	}
	out, err := json.Marshal([]RunEvent{e})
	if err != nil {
		return ""
	}
	return string(out)
}
