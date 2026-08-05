package gateway

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"gopkg.in/yaml.v3"
)

// fakeCommandData builds the interaction payload Discord delivers for a
// registered command, so the translation back to router text can be tested
// without a live gateway connection.
func fakeCommandData(name, args string) discordgo.ApplicationCommandInteractionData {
	d := discordgo.ApplicationCommandInteractionData{Name: name}
	if args != "" {
		d.Options = []*discordgo.ApplicationCommandInteractionDataOption{{
			Name:  discordArgsOption,
			Type:  discordgo.ApplicationCommandOptionString,
			Value: args,
		}}
	}
	return d
}

// telegramNameRe is Telegram's documented constraint on a command name:
// lowercase English letters, digits and underscores, 1-32 characters.
var telegramNameRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// TestTelegramCommandNamesAreValid guards the whole setMyCommands call, which
// Telegram rejects wholesale if ANY single entry is malformed — so one bad name
// silently empties the menu for every command rather than just its own.
//
// This is also why the Slack overrides are Slack-only: "rookery-help" carries a
// hyphen, which is invalid here.
func TestTelegramCommandNamesAreValid(t *testing.T) {
	for _, c := range telegramCommands() {
		if !telegramNameRe.MatchString(c.Text) {
			t.Errorf("command %q is not a valid Telegram command name", c.Text)
		}
		if n := len(c.Description); n < 3 || n > 256 {
			t.Errorf("command %q description is %d chars, Telegram requires 3-256", c.Text, n)
		}
	}
}

// TestDiscordCommandDescriptionsFitTheLimit pins Discord's 100-character cap.
// As with Telegram, registration is a single bulk call: one over-long
// description fails the whole payload, so every command disappears at once.
func TestDiscordCommandDescriptionsFitTheLimit(t *testing.T) {
	cmds := discordCommands()
	if len(cmds) != len(Commands) {
		t.Fatalf("built %d Discord commands from %d table entries", len(cmds), len(Commands))
	}
	for _, c := range cmds {
		if len(c.Description) > discordMaxDescription {
			t.Errorf("command %q description is %d chars, Discord allows %d",
				c.Name, len(c.Description), discordMaxDescription)
		}
		for _, o := range c.Options {
			if len(o.Description) > discordMaxDescription {
				t.Errorf("command %q option %q description is %d chars, Discord allows %d",
					c.Name, o.Name, len(o.Description), discordMaxDescription)
			}
		}
	}
}

// TestDiscordCommandsCarryASingleStringOption pins the flat shape. A subcommand
// tree would be a second argument grammar to keep in sync with the router's own
// parsing, for no behavioural gain.
func TestDiscordCommandsCarryASingleStringOption(t *testing.T) {
	byName := map[string]int{}
	for _, c := range discordCommands() {
		byName[c.Name] = len(c.Options)
		for _, o := range c.Options {
			if o.Name != discordArgsOption {
				t.Errorf("command %q has option %q, want only %q", c.Name, o.Name, discordArgsOption)
			}
			if o.Required {
				t.Errorf("command %q option must be optional — /agent alone is valid", c.Name)
			}
		}
	}
	// A command taking arguments has the option; one that takes none does not.
	if byName["agent"] != 1 {
		t.Errorf("/agent should carry one args option, got %d", byName["agent"])
	}
	if byName["pending"] != 0 {
		t.Errorf("/pending takes no arguments, got %d options", byName["pending"])
	}
}

// TestDiscordInteractionTextRebuildsTheRouterForm is the crux of the Discord
// cutover: once a command is REGISTERED, the client sends an interaction instead
// of message text, so the router only keeps working if the interaction is
// translated back into the exact string a typed message would have produced.
func TestDiscordInteractionTextRebuildsTheRouterForm(t *testing.T) {
	for _, tc := range []struct{ name, args, want string }{
		{"agent", "create hackernews", "/agent create hackernews"},
		{"agent", "", "/agent"},
		{"agent", "   ", "/agent"},
		{"pending", "", "/pending"},
		{"remind", "in 10 minutes to check oven", "/remind in 10 minutes to check oven"},
	} {
		got := discordInteractionText(fakeCommandData(tc.name, tc.args))
		if got != tc.want {
			t.Errorf("discordInteractionText(%q, %q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

// TestIsRegisteredCommandIgnoresForeignInteractions: a Discord app receives
// interactions only for its own commands, but the guard is cheap and the
// alternative — dispatching an arbitrary name as router text — is not.
func TestIsRegisteredCommandIgnoresForeignInteractions(t *testing.T) {
	if !isRegisteredCommand("agent") {
		t.Error("agent must be recognised")
	}
	if isRegisteredCommand("giphy") {
		t.Error("a command this bot never registered must be ignored")
	}
}

// TestSlackNamesAvoidBuiltinCollisions pins the two overrides. Slack's
// slash-command namespace is WORKSPACE-GLOBAL and shared with Slack's own
// built-ins: /remind is Slack's reminder command and /help opens Slack's help.
// Everything else stays unprefixed, which is a deliberate, accepted risk —
// another installed app can claim a name first.
func TestSlackNamesAvoidBuiltinCollisions(t *testing.T) {
	want := map[string]string{
		"remind": "rookery-remind",
		"help":   "rookery-help",
		"agent":  "agent",
		"run":    "run",
	}
	for _, c := range Commands {
		if w, ok := want[c.Name]; ok {
			if got := c.PlatformCommandName("slack"); got != w {
				t.Errorf("slack name for %q = %q, want %q", c.Name, got, w)
			}
		}
		// The override must never leak onto the other platforms — a hyphen is
		// invalid as a Telegram command name.
		if got := c.PlatformCommandName("telegram"); got != c.Name {
			t.Errorf("telegram name for %q = %q, want the canonical name", c.Name, got)
		}
		if got := c.PlatformCommandName("discord"); got != c.Name {
			t.Errorf("discord name for %q = %q, want the canonical name", c.Name, got)
		}
	}
}

// TestCanonicalCommandNameRoundTrips: the Slack handler receives the Slack-side
// name and must dispatch the canonical one, or /rookery-remind would reach the
// router as an unknown command.
func TestCanonicalCommandNameRoundTrips(t *testing.T) {
	for _, c := range Commands {
		slackName := c.PlatformCommandName("slack")
		if got := CanonicalCommandName("slack", slackName); got != c.Name {
			t.Errorf("CanonicalCommandName(slack, %q) = %q, want %q", slackName, got, c.Name)
		}
		// Accepted with or without the leading slash, since Slack sends "/foo".
		if got := CanonicalCommandName("slack", "/"+slackName); got != c.Name {
			t.Errorf("CanonicalCommandName(slack, /%s) = %q, want %q", slackName, got, c.Name)
		}
	}
	// An unrecognised name passes through so the router's own unknown-command
	// branch reports it, rather than this silently rewriting it.
	if got := CanonicalCommandName("slack", "/giphy"); got != "giphy" {
		t.Errorf("unknown command = %q, want it passed through", got)
	}
}

// TestSlashCommandTextRebuildsTheRouterForm covers the Slack analogue of the
// Discord translation, including the name mapping.
func TestSlashCommandTextRebuildsTheRouterForm(t *testing.T) {
	for _, tc := range []struct{ command, text, want string }{
		{"/rookery-remind", "in 10 minutes to check oven", "/remind in 10 minutes to check oven"},
		{"/rookery-help", "", "/help"},
		{"/agent", "create hackernews", "/agent create hackernews"},
		{"/pending", "  ", "/pending"},
	} {
		if got := slashCommandText(tc.command, tc.text); got != tc.want {
			t.Errorf("slashCommandText(%q, %q) = %q, want %q", tc.command, tc.text, got, tc.want)
		}
	}
}

// TestSlackManifestDeclaresEveryCommand is what makes the Slack menu exist at
// all: commands cannot be registered with a bot token, so anything missing from
// the manifest is a command the user can never see or invoke.
func TestSlackManifestDeclaresEveryCommand(t *testing.T) {
	raw, err := SlackAppManifest()
	if err != nil {
		t.Fatalf("SlackAppManifest: %v", err)
	}

	var m struct {
		Features struct {
			SlashCommands []struct {
				Command     string `yaml:"command"`
				Description string `yaml:"description"`
			} `yaml:"slash_commands"`
			AppHome struct {
				MessagesTabEnabled bool `yaml:"messages_tab_enabled"`
			} `yaml:"app_home"`
		} `yaml:"features"`
		OAuthConfig struct {
			Scopes struct {
				Bot []string `yaml:"bot"`
			} `yaml:"scopes"`
		} `yaml:"oauth_config"`
		Settings struct {
			SocketModeEnabled  bool `yaml:"socket_mode_enabled"`
			EventSubscriptions struct {
				BotEvents []string `yaml:"bot_events"`
			} `yaml:"event_subscriptions"`
		} `yaml:"settings"`
	}
	if err := yaml.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("manifest is not valid YAML: %v\n%s", err, raw)
	}

	declared := map[string]bool{}
	for _, sc := range m.Features.SlashCommands {
		declared[sc.Command] = true
		if sc.Description == "" {
			t.Errorf("slash command %q has no description", sc.Command)
		}
	}
	for _, c := range Commands {
		want := "/" + c.PlatformCommandName("slack")
		if !declared[want] {
			t.Errorf("manifest does not declare %q", want)
		}
	}

	// The adapter cannot function without these, and they are the steps the
	// manifest replaced — so their absence would silently reintroduce the manual
	// setup this change exists to remove.
	if !m.Settings.SocketModeEnabled {
		t.Error("manifest must enable Socket Mode")
	}
	if !m.Features.AppHome.MessagesTabEnabled {
		t.Error("manifest must enable the App Home messages tab, or DMs cannot be replied to")
	}
	if len(m.Settings.EventSubscriptions.BotEvents) == 0 ||
		m.Settings.EventSubscriptions.BotEvents[0] != "message.im" {
		t.Errorf("manifest must subscribe to message.im, got %v", m.Settings.EventSubscriptions.BotEvents)
	}
	// "commands" is what makes slash commands deliverable at all.
	if !containsStr(m.OAuthConfig.Scopes.Bot, "commands") {
		t.Errorf("manifest bot scopes must include commands, got %v", m.OAuthConfig.Scopes.Bot)
	}
}

func containsStr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// TestHelpTextCoversEveryCommand keeps /help derived from the table. It used to
// be a hand-maintained string beside a separate dispatch switch, which is
// precisely how a command ends up implemented but undocumented.
func TestHelpTextCoversEveryCommand(t *testing.T) {
	for _, platform := range []string{"telegram", "discord", "slack"} {
		text := helpText(platform)
		for _, c := range Commands {
			want := "/" + c.PlatformCommandName(platform)
			if !strings.Contains(text, want) {
				t.Errorf("%s help text omits %q", platform, want)
			}
		}
	}
	// Slack's help must show the prefixed forms, since the bare ones are not
	// what the user can type there.
	slackHelp := helpText("slack")
	if !strings.Contains(slackHelp, "/rookery-remind") {
		t.Error("slack help must show /rookery-remind")
	}
}

// TestEveryTableCommandIsDispatched is the parity gate. The router's switch is
// deliberately not refactored into a map — its handlers have divergent
// signatures — so parity is asserted behaviourally instead: a table command must
// never reach the unknown-command branch, and a bogus one must.
func TestEveryTableCommandIsDispatched(t *testing.T) {
	for _, c := range Commands {
		t.Run(c.Name, func(t *testing.T) {
			r := NewRouter(nil, nil, nil, nil, nil)
			var replies []string
			// Handle reaches the DB for most commands; a nil *db.DB panics rather
			// than returning an error, so recover and judge only on whether the
			// unknown-command branch was taken. Reaching a handler at all is the
			// property under test.
			func() {
				defer func() { _ = recover() }()
				_ = r.Handle(context.Background(),
					Message{Platform: "telegram", WorkspaceID: "w1", Text: "/" + c.Name},
					func(s string) { replies = append(replies, s) },
					func() {}, func(string) {}, func(string) {})
			}()
			for _, reply := range replies {
				if strings.Contains(reply, "Unknown command") {
					t.Errorf("/%s is in the table but the router does not dispatch it: %q", c.Name, reply)
				}
			}
		})
	}
}

// The other direction: a name nothing dispatches must still be reported, or the
// test above would pass trivially against a router that answers everything.
func TestUnknownCommandIsStillReported(t *testing.T) {
	r := NewRouter(nil, nil, nil, nil, nil)
	var replies []string
	func() {
		defer func() { _ = recover() }()
		_ = r.Handle(context.Background(),
			Message{Platform: "telegram", WorkspaceID: "w1", Text: "/definitelynotacommand"},
			func(s string) { replies = append(replies, s) },
			func() {}, func(string) {}, func(string) {})
	}()
	found := false
	for _, reply := range replies {
		if strings.Contains(reply, "Unknown command") {
			found = true
		}
	}
	if !found {
		t.Errorf("an unregistered command must be reported, got %v", replies)
	}
}
