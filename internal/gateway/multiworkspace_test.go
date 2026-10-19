package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// Reproduces the reported setup: one Telegram account, linked to the Demo
// workspace through Demo's bot, now also messaging the Personal workspace's bot.
const sharedAccount = "1843540314"

type twoBots struct {
	m        *GatewayManager
	db       *db.DB
	demo     *fakeTypingGateway
	personal *fakeTypingGateway
}

func newTwoBots(t *testing.T) *twoBots {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, w := range []struct{ id, name string }{{"demo", "Demo"}, {"personal", "Personal"}} {
		if err := database.CreateWorkspace(&db.Workspace{ID: w.id, Name: w.name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.LinkPlatformIdentity(&db.PlatformIdentity{
		ID: "demo-link", WorkspaceID: "demo", Platform: "telegram", PlatformUserID: sharedAccount,
	}); err != nil {
		t.Fatal(err)
	}
	b := &twoBots{
		m:        New(database, []byte("k"), NewRouter(database, nil, nil, nil, nil)),
		db:       database,
		demo:     &fakeTypingGateway{},
		personal: &fakeTypingGateway{},
	}
	b.m.gateways[key("telegram", "demo")] = b.demo
	b.m.gateways[key("telegram", "personal")] = b.personal
	return b
}

// The bug: /start sent to the Personal bot was routed into Demo and answered
// "You're already linked!" OUT OF DEMO'S BOT. It must link Personal and answer
// from the bot that was messaged.
func TestStartOnASecondWorkspacesBotLinksThatWorkspace(t *testing.T) {
	b := newTwoBots(t)

	b.m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: sharedAccount, WorkspaceID: "personal", Text: "/start",
	})

	if _, err := b.db.GetPlatformIdentity("personal", "telegram", sharedAccount); err != nil {
		t.Fatalf("Personal was not linked: %v", err)
	}
	if _, err := b.db.GetPlatformIdentity("demo", "telegram", sharedAccount); err != nil {
		t.Fatalf("Demo link was lost: %v", err)
	}
	demoSent, _ := b.demo.snapshot()
	if len(demoSent) != 0 {
		t.Fatalf("Demo's bot answered a message sent to Personal's: %q", demoSent)
	}
	personalSent, _ := b.personal.snapshot()
	if len(personalSent) != 1 || !strings.Contains(personalSent[0], "Personal") {
		t.Fatalf("Personal's bot replies = %q, want a link confirmation naming Personal", personalSent)
	}
}

// workspaceRecorder captures which workspace each routed message landed in.
type workspaceRecorder struct {
	mu  sync.Mutex
	got []string
}

func (r *workspaceRecorder) Handle(ctx context.Context, msg Message, send func(string),
	deleteIncoming func(), sendAutoDelete func(string), sendProgress func(string)) error {
	r.mu.Lock()
	r.got = append(r.got, msg.WorkspaceID)
	r.mu.Unlock()
	send("ok")
	return nil
}

// Once linked to both, each message belongs to the workspace of the bot that
// received it, and the reply leaves through that same bot.
func TestMessagesRouteByTheReceivingBot(t *testing.T) {
	b := newTwoBots(t)
	if err := b.db.LinkPlatformIdentity(&db.PlatformIdentity{
		ID: "personal-link", WorkspaceID: "personal", Platform: "telegram", PlatformUserID: sharedAccount,
	}); err != nil {
		t.Fatal(err)
	}
	rec := &workspaceRecorder{}
	b.m.router = rec

	for _, ws := range []string{"personal", "demo", "personal"} {
		b.m.dispatch(context.Background(), Message{
			Platform: "telegram", PlatformUserID: sharedAccount, WorkspaceID: ws, Text: "/help",
		})
	}

	want := []string{"personal", "demo", "personal"}
	if strings.Join(rec.got, ",") != strings.Join(want, ",") {
		t.Fatalf("routed to %v, want %v", rec.got, want)
	}
	demoSent, _ := b.demo.snapshot()
	personalSent, _ := b.personal.snapshot()
	if len(demoSent) != 1 || len(personalSent) != 2 {
		t.Fatalf("replies: demo=%d personal=%d, want 1 and 2", len(demoSent), len(personalSent))
	}
}

// Being linked to Demo grants nothing on Personal's bot: before /start there,
// the account is a stranger to it.
func TestALinkElsewhereDoesNotUnlockThisBot(t *testing.T) {
	b := newTwoBots(t)
	rec := &workspaceRecorder{}
	b.m.router = rec

	b.m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: sharedAccount, WorkspaceID: "personal", Text: "/help",
	})

	if len(rec.got) != 0 {
		t.Fatalf("unlinked message was routed: %v", rec.got)
	}
	personalSent, _ := b.personal.snapshot()
	if len(personalSent) != 1 || !strings.Contains(personalSent[0], "/start") {
		t.Fatalf("Personal's bot replies = %q, want the private-bot notice", personalSent)
	}
}

// A workspace's bot serves one chat: a second chat's /start is refused and the
// first link survives.
func TestStartFromASecondChatIsRefused(t *testing.T) {
	b := newTwoBots(t)

	b.m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: "stranger", WorkspaceID: "demo", Text: "/start",
	})

	if _, err := b.db.GetPlatformIdentity("demo", "telegram", "stranger"); err == nil {
		t.Fatal("stranger was linked to an already-linked bot")
	}
	if _, err := b.db.GetPlatformIdentity("demo", "telegram", sharedAccount); err != nil {
		t.Fatalf("owner's link was lost: %v", err)
	}
	demoSent, _ := b.demo.snapshot()
	if len(demoSent) != 1 || !strings.Contains(demoSent[0], "already linked to another chat") {
		t.Fatalf("Demo's bot replies = %q, want a refusal", demoSent)
	}
}

// An unlinked group stays silent for anything but /start: with privacy mode off
// the bot sees every message, and answering each would spam the group.
func TestAnUnlinkedGroupIsSilent(t *testing.T) {
	b := newTwoBots(t)
	b.m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: "-1001", WorkspaceID: "personal", Text: "hello all", Group: true,
	})
	personalSent, _ := b.personal.snapshot()
	if len(personalSent) != 0 {
		t.Fatalf("unlinked group got replies: %q", personalSent)
	}
}

// /start sent inside a group links the GROUP, so every member can use the bot.
func TestStartInAGroupLinksTheGroup(t *testing.T) {
	b := newTwoBots(t)
	b.m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: "-1001", WorkspaceID: "personal", Text: "/start", Group: true,
	})
	if _, err := b.db.GetPlatformIdentity("personal", "telegram", "-1001"); err != nil {
		t.Fatalf("group was not linked: %v", err)
	}
}

// A group upgraded to a supergroup gets a new chat id; the link must follow it.
func TestMigratedGroupKeepsItsLink(t *testing.T) {
	b := newTwoBots(t)
	if err := b.db.LinkPlatformIdentity(&db.PlatformIdentity{
		ID: "g", WorkspaceID: "personal", Platform: "telegram", PlatformUserID: "-1001",
	}); err != nil {
		t.Fatal(err)
	}
	b.m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: "-1009001", WorkspaceID: "personal",
		MigrateFromID: "-1001", Group: true,
	})
	if _, err := b.db.GetPlatformIdentity("personal", "telegram", "-1009001"); err != nil {
		t.Fatalf("link did not follow the migration: %v", err)
	}
	personalSent, _ := b.personal.snapshot()
	if len(personalSent) != 0 {
		t.Fatalf("a migration notice produced replies: %q", personalSent)
	}
}

func TestStripOwnMention(t *testing.T) {
	cases := []struct{ in, user, want string }{
		{"/help@PersonalBot", "PersonalBot", "/help"},
		{"/remind@personalbot in 5 minutes tea", "PersonalBot", "/remind in 5 minutes tea"},
		{"/help@WorkBot", "PersonalBot", "/help@WorkBot"}, // another bot's: left alone
		{"/help", "PersonalBot", "/help"},
		{"mail me@PersonalBot later", "PersonalBot", "mail me@PersonalBot later"}, // not a command
		{"/help@PersonalBot", "", "/help@PersonalBot"},                            // identity unknown
	}
	for _, c := range cases {
		if got := stripOwnMention(c.in, c.user); got != c.want {
			t.Errorf("stripOwnMention(%q, %q) = %q, want %q", c.in, c.user, got, c.want)
		}
	}
}
