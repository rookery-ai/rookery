package gateway

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Slack slash commands CANNOT be created with a bot token. They are declared in
// the app's configuration, and automating that needs an app-configuration token
// — rotating every 12 hours, and a third credential beyond the bot and app-level
// tokens Rookery already collects.
//
// A manifest sidesteps it entirely: the user creates the app "from an app
// manifest" and every command, scope, event subscription and App Home setting
// arrives declared in one paste. That also replaces the seven manual setup steps
// this adapter used to require, so the shortest path is now also the correct one.
//
// The manifest is generated rather than checked in as a fixture so it cannot
// drift from the command table.

// slackManifest mirrors the subset of Slack's app-manifest schema this adapter
// needs. Field order here is the field order in the emitted YAML.
type slackManifest struct {
	DisplayInformation slackDisplayInfo `yaml:"display_information"`
	Features           slackFeatures    `yaml:"features"`
	OAuthConfig        slackOAuthConfig `yaml:"oauth_config"`
	Settings           slackSettings    `yaml:"settings"`
}

type slackDisplayInfo struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type slackFeatures struct {
	BotUser       slackBotUser        `yaml:"bot_user"`
	AppHome       slackAppHome        `yaml:"app_home"`
	SlashCommands []slackSlashCommand `yaml:"slash_commands"`
}

type slackBotUser struct {
	DisplayName  string `yaml:"display_name"`
	AlwaysOnline bool   `yaml:"always_online"`
}

// AppHome's messages tab must be enabled AND accept typed messages, or the bot
// can send DMs the user cannot reply to.
type slackAppHome struct {
	MessagesTabEnabled         bool `yaml:"messages_tab_enabled"`
	MessagesTabReadOnlyEnabled bool `yaml:"messages_tab_read_only_enabled"`
}

type slackSlashCommand struct {
	Command      string `yaml:"command"`
	Description  string `yaml:"description"`
	UsageHint    string `yaml:"usage_hint,omitempty"`
	ShouldEscape bool   `yaml:"should_escape"`
}

type slackOAuthConfig struct {
	Scopes slackScopes `yaml:"scopes"`
}

type slackScopes struct {
	Bot []string `yaml:"bot"`
}

type slackSettings struct {
	EventSubscriptions slackEventSubscriptions `yaml:"event_subscriptions"`
	Interactivity      slackInteractivity      `yaml:"interactivity"`
	SocketModeEnabled  bool                    `yaml:"socket_mode_enabled"`
}

type slackEventSubscriptions struct {
	BotEvents []string `yaml:"bot_events"`
}

// Interactivity must be on for slash commands to be delivered over Socket Mode.
type slackInteractivity struct {
	IsEnabled bool `yaml:"is_enabled"`
}

// slackBotScopes are the scopes the adapter actually uses: chat:write to reply,
// im:history to receive DMs, im:write to open the DM channel, files:read to
// import attachments, and commands for the slash commands declared below.
var slackBotScopes = []string{
	"chat:write",
	"im:history",
	"im:write",
	"files:read",
	"commands",
}

// SlackAppManifest renders the manifest a user pastes into Slack's "create an
// app from a manifest" flow. Every command in the shared table becomes a slash
// command under its Slack-side name.
func SlackAppManifest() (string, error) {
	cmds := make([]slackSlashCommand, 0, len(Commands))
	for _, c := range Commands {
		cmds = append(cmds, slackSlashCommand{
			Command:     "/" + c.PlatformCommandName("slack"),
			Description: c.Description,
			UsageHint:   c.UsageHint,
			// Slack would otherwise wrap URLs and @-mentions in <…> markup, which
			// the router's argument parsing would then have to strip back out.
			ShouldEscape: false,
		})
	}

	m := slackManifest{
		DisplayInformation: slackDisplayInfo{
			Name:        "Rookery",
			Description: "Your personal agent assistant",
		},
		Features: slackFeatures{
			BotUser:       slackBotUser{DisplayName: "rookery", AlwaysOnline: false},
			AppHome:       slackAppHome{MessagesTabEnabled: true, MessagesTabReadOnlyEnabled: false},
			SlashCommands: cmds,
		},
		OAuthConfig: slackOAuthConfig{Scopes: slackScopes{Bot: slackBotScopes}},
		Settings: slackSettings{
			EventSubscriptions: slackEventSubscriptions{BotEvents: []string{"message.im"}},
			Interactivity:      slackInteractivity{IsEnabled: true},
			SocketModeEnabled:  true,
		},
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
