package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type fakeStopper struct {
	running []string
	stopped []string
}

func (f *fakeStopper) RunningAgentNames(string) []string { return f.running }

func (f *fakeStopper) StopAgentByName(_, name string) bool {
	for _, n := range f.running {
		if strings.EqualFold(n, name) {
			f.stopped = append(f.stopped, n)
			return true
		}
	}
	return false
}

func stopReply(t *testing.T, r *Router, arg string) string {
	t.Helper()
	var replies []string
	if err := r.handleAgent(context.Background(), Message{WorkspaceID: "ws1"}, arg,
		func(s string) { replies = append(replies, s) }); err != nil {
		t.Fatalf("handleAgent: %v", err)
	}
	return strings.Join(replies, " ~ ")
}

func TestAgentStop(t *testing.T) {
	cases := []struct {
		name        string
		running     []string
		arg         string
		wantStopped []string
		wantInReply string
	}{
		{name: "named and running", running: []string{"Looper"}, arg: "stop Looper", wantStopped: []string{"Looper"}, wantInReply: "Looper"},
		{name: "name ignores case", running: []string{"Looper"}, arg: "stop looper", wantStopped: []string{"Looper"}, wantInReply: "looper"},
		{name: "multi-word name", running: []string{"Price Tracker"}, arg: "stop Price Tracker", wantStopped: []string{"Price Tracker"}, wantInReply: "Price Tracker"},
		{name: "named but idle", running: nil, arg: "stop Calm", wantInReply: "isn't running"},
		{name: "no name, nothing running", running: nil, arg: "stop", wantInReply: "No agent is running"},
		{name: "no name, one running", running: []string{"Looper"}, arg: "stop", wantStopped: []string{"Looper"}, wantInReply: "Looper"},
		{name: "no name, several running", running: []string{"Alpha", "Beta"}, arg: "stop", wantInReply: "Alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stopper := &fakeStopper{running: tc.running}
			r := NewRouter(nil, nil, nil, nil, nil).WithRunStopper(stopper)
			reply := stopReply(t, r, tc.arg)
			if !strings.Contains(reply, tc.wantInReply) {
				t.Errorf("reply %q should contain %q", reply, tc.wantInReply)
			}
			if fmt.Sprint(stopper.stopped) != fmt.Sprint(tc.wantStopped) {
				t.Errorf("stopped %v, want %v", stopper.stopped, tc.wantStopped)
			}
		})
	}
}

func TestAgentStopWithoutARunner(t *testing.T) {
	r := NewRouter(nil, nil, nil, nil, nil)
	if reply := stopReply(t, r, "stop Looper"); !strings.Contains(reply, "not available") {
		t.Errorf("reply = %q", reply)
	}
}

// A run stopped from elsewhere must not report that it finished.
func TestStoppedChatRunDoesNotReportFinished(t *testing.T) {
	h := &runHarness{}
	r := newRunRouter(func(context.Context, string, string, func(string), func(string)) error {
		return fmt.Errorf("wrapped: %w", agentrunner.ErrRunStopped)
	})
	if err := r.handleRun(context.Background(), Message{WorkspaceID: "ws1"}, "weather", h.send, h.progress); err != nil {
		t.Fatalf("a stop is not an error for the chat: %v", err)
	}
	joined := h.all()
	if strings.Contains(joined, "finished") {
		t.Errorf("a stopped run must not read as finished: %s", joined)
	}
	if !strings.Contains(joined, "stopped") || !strings.Contains(joined, "weather") {
		t.Errorf("the reply should name the stopped agent: %s", joined)
	}
}

func TestChatRunFailureIsStillAnError(t *testing.T) {
	h := &runHarness{}
	boom := errors.New("boom")
	r := newRunRouter(func(context.Context, string, string, func(string), func(string)) error { return boom })
	if err := r.handleRun(context.Background(), Message{WorkspaceID: "ws1"}, "weather", h.send, h.progress); !errors.Is(err, boom) {
		t.Fatalf("handleRun = %v, want the failure", err)
	}
}

// The Slack loop used to dispatch inline, so a /run blocked every later event and its
// own /agent stop could not arrive until the run ended.
func TestSlackReadLoopKeepsReadingWhileAHandlerBlocks(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	seen := make(chan string, 2)
	g := &SlackGateway{
		ownerWorkspaceID: "ws1",
		sm:               socketmode.New(slack.New("xoxb-test")),
		dispatch: func(_ context.Context, m Message) {
			seen <- m.Text
			if m.Text == "/run looper" {
				<-release
			}
		},
		dmChannels: map[string]string{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.readLoop(ctx)

	event := func(text string) socketmode.Event {
		inner := slackevents.EventsAPIInnerEvent{Type: "message"}
		inner.Data = &slackevents.MessageEvent{User: "U1", ChannelType: "im", Text: text, TimeStamp: "1.2"}
		return socketmode.Event{
			Type: socketmode.EventTypeEventsAPI,
			Data: slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: inner},
		}
	}
	g.sm.Events <- event("/run looper")
	if got := <-seen; got != "/run looper" {
		t.Fatalf("first event = %q", got)
	}
	g.sm.Events <- event("/agent stop looper")
	select {
	case got := <-seen:
		if got != "/agent stop looper" {
			t.Fatalf("second event = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked handler stopped the read loop")
	}
}
