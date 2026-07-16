package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/ilijad1/simple-agents/internal/gateway/render"
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
			"App Home → Messages Tab → enable it and check 'Allow users to send Slash commands and messages from the messages tab'",
			"Paste BOTH tokens here, then DM your bot /start",
		},
		Validate: func(v map[string]string) (string, error) {
			if v["app_token"] == "" {
				return "", fmt.Errorf("app-level token (xapp-) is required for Socket Mode")
			}
			return validateSlackToken(v["token"])
		},
	})

	RegisterAdapter("slack", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		appToken, err := parseSlackConfig(config)
		if err != nil {
			return nil, err
		}
		return NewSlack(token, appToken, ws, d)
	})
}

// mapSlackDM converts an inbound Slack message event to a Message, returning
// ok=false for the bot's own messages, bot messages, subtyped messages
// (edits/joins), and non-DM channels.
func mapSlackDM(user, channelType, text, ts, botID, subType, botUserID string) (Message, bool) {
	if channelType != "im" || botID != "" || subType != "" || user == "" || user == botUserID {
		return Message{}, false
	}
	return Message{Platform: "slack", PlatformUserID: user, Text: text, MessageID: ts}, true
}

// parseSlackConfig extracts the app-level token from the encrypted_config JSON.
func parseSlackConfig(config string) (string, error) {
	var c struct {
		AppToken string `json:"app_token"`
	}
	if err := json.Unmarshal([]byte(config), &c); err != nil {
		return "", fmt.Errorf("slack config parse: %w", err)
	}
	if c.AppToken == "" {
		return "", fmt.Errorf("slack config missing app_token")
	}
	return c.AppToken, nil
}

// SlackGateway is one workspace's Slack bot (Socket Mode, DM-only).
type SlackGateway struct {
	api              *slack.Client
	sm               *socketmode.Client
	ownerWorkspaceID string
	dispatch         DispatchFunc

	mu         sync.Mutex
	botUserID  string
	dmChannels map[string]string // userID → DM channel ID
}

// NewSlack creates (does not start) a Slack adapter from bot + app tokens.
func NewSlack(botToken, appToken, ownerWorkspaceID string, dispatch DispatchFunc) (*SlackGateway, error) {
	api := slackAPINew(botToken, slack.OptionAppLevelToken(appToken))
	return &SlackGateway{
		api:              api,
		sm:               socketmode.New(api),
		ownerWorkspaceID: ownerWorkspaceID,
		dispatch:         dispatch,
		dmChannels:       map[string]string{},
	}, nil
}

func (g *SlackGateway) Platform() string    { return "slack" }
func (g *SlackGateway) OwnerUserID() string { return g.ownerWorkspaceID }

func (g *SlackGateway) Start(ctx context.Context) error {
	if resp, err := g.api.AuthTest(); err == nil {
		g.mu.Lock()
		g.botUserID = resp.UserID
		g.mu.Unlock()
	}
	go g.readLoop(ctx)
	return g.sm.RunContext(ctx)
}

func (g *SlackGateway) Stop() error { return nil } // RunContext exits on ctx cancel

func (g *SlackGateway) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-g.sm.Events:
			if evt.Type != socketmode.EventTypeEventsAPI {
				continue
			}
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			if evt.Request != nil {
				g.sm.Ack(*evt.Request)
			}
			if eventsAPIEvent.Type != slackevents.CallbackEvent {
				continue
			}
			if me, ok := eventsAPIEvent.InnerEvent.Data.(*slackevents.MessageEvent); ok {
				g.mu.Lock()
				botID := g.botUserID
				g.mu.Unlock()
				msg, ok := mapSlackDM(me.User, me.ChannelType, me.Text, me.TimeStamp, me.BotID, me.SubType, botID)
				if ok {
					msg.WorkspaceID = g.ownerWorkspaceID
					g.dispatch(context.Background(), msg)
				}
			}
		}
	}
}

func (g *SlackGateway) resolveDM(userID string) (string, error) {
	g.mu.Lock()
	if ch, ok := g.dmChannels[userID]; ok {
		g.mu.Unlock()
		return ch, nil
	}
	g.mu.Unlock()
	ch, _, _, err := g.api.OpenConversation(&slack.OpenConversationParameters{Users: []string{userID}, ReturnIM: true})
	if err != nil {
		return "", fmt.Errorf("slack open DM: %w", err)
	}
	g.mu.Lock()
	g.dmChannels[userID] = ch.ID
	g.mu.Unlock()
	return ch.ID, nil
}

func (g *SlackGateway) Send(platformUserID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, _, err = g.api.PostMessage(ch, slack.MsgOptionText(render.For("slack").Render(text), false))
	return err
}

// SendTyping is a no-op: Slack has no reliable typing indicator over Socket Mode.
func (g *SlackGateway) SendTyping(platformUserID string) error { return nil }

func (g *SlackGateway) SendMessageGetID(platformUserID, text string) (string, error) {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return "", err
	}
	_, ts, err := g.api.PostMessage(ch, slack.MsgOptionText(render.For("slack").Render(text), false))
	if err != nil {
		return "", err
	}
	return ts, nil
}

func (g *SlackGateway) EditMessage(platformUserID, msgID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, _, _, err = g.api.UpdateMessage(ch, msgID, slack.MsgOptionText(render.For("slack").Render(text), false))
	return err
}

func (g *SlackGateway) DeleteMessage(platformUserID, msgID string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, _, err = g.api.DeleteMessage(ch, msgID)
	return err
}
