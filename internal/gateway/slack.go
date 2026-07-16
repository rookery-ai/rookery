package gateway

import (
	"fmt"

	"github.com/slack-go/slack"
)

// slackAPINew builds a *slack.Client; indirected for tests.
var slackAPINew = func(botToken string, opts ...slack.Option) *slack.Client {
	return slack.New(botToken, opts...)
}

// validateSlackToken confirms a bot token via auth.test, returning the bot user name.
func validateSlackToken(botToken string) (string, error) {
	resp, err := slackAPINew(botToken).AuthTest()
	if err != nil {
		return "", fmt.Errorf("slack rejected bot token: %w", err)
	}
	return resp.User, nil
}

func init() {
	RegisterCredSpec(CredSpec{
		Platform: "slack",
		Label:    "Slack",
		Blurb:    "Chat with your agents via a personal Slack bot (Socket Mode DMs)",
		Fields: []CredField{
			{Key: "token", Label: "Bot Token (xoxb-)", Placeholder: "xoxb-...", Secret: true},
			{Key: "app_token", Label: "App-Level Token (xapp-)", Placeholder: "xapp-...", Secret: true},
		},
		SetupURL: "https://api.slack.com/apps",
		SetupSteps: []string{
			"Create a Slack app at api.slack.com/apps (From scratch)",
			"Socket Mode → enable it; generate an App-Level Token with connections:write (xapp-...)",
			"OAuth & Permissions → add bot scopes chat:write, im:history, im:write; Install to Workspace; copy the Bot Token (xoxb-...)",
			"Event Subscriptions → enable; subscribe to bot event message.im; reinstall if prompted",
			"Paste BOTH tokens here, then DM your bot /start",
		},
		Validate: func(v map[string]string) (string, error) {
			if v["app_token"] == "" {
				return "", fmt.Errorf("app-level token (xapp-) is required for Socket Mode")
			}
			return validateSlackToken(v["token"])
		},
	})
}
