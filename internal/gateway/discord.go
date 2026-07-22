package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ilijad1/simple-agents/internal/gateway/render"
)

// discordAPIBase is the Discord REST base; overridable in tests.
var discordAPIBase = "https://discord.com/api/v10"

// validateDiscordToken confirms a bot token by fetching the bot user, returning its username.
func validateDiscordToken(token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, discordAPIBase+"/users/@me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("discord api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord rejected token (status %d)", resp.StatusCode)
	}
	var out struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Username == "" {
		return "", fmt.Errorf("invalid response from discord")
	}
	return out.Username, nil
}

func init() {
	RegisterCredSpec(CredSpec{
		Platform: "discord",
		Label:    "Discord",
		Blurb:    "Chat with your agents via a personal Discord bot (DMs)",
		Fields:   []CredField{{Key: "token", Label: "Bot Token", Placeholder: "your bot token", Secret: true}},
		SetupURL: "https://discord.com/developers/applications",
		SetupSteps: []string{
			"Open the Discord Developer Portal and create a New Application",
			"Open the Bot tab, click Reset Token, and copy the token",
			"On the Bot tab, enable the MESSAGE CONTENT INTENT (Privileged Gateway Intents)",
			"Invite the bot to a server OR just DM it after connecting; send /start to link",
		},
		Validate: func(v map[string]string) (string, error) { return validateDiscordToken(v["token"]) },
	})

	RegisterAdapter("discord", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return NewDiscord(token, ws, d)
	})
}

// DiscordGateway is one workspace's Discord bot instance (DM-only).
type DiscordGateway struct {
	session          *discordgo.Session
	ownerWorkspaceID string
	dispatch         DispatchFunc

	mu         sync.Mutex
	dmChannels map[string]string // userID → DM channel ID (cache)
}

// mapDiscordDM converts an inbound Discord message to a Message, returning ok=false
// for the bot's own messages, other bots, and non-DM (guild) messages.
func mapDiscordDM(authorID, guildID, content, msgID, botUserID string, isBot bool) (Message, bool) {
	if isBot || authorID == botUserID || guildID != "" {
		return Message{}, false
	}
	return Message{
		Platform:       "discord",
		PlatformUserID: authorID,
		Text:           content,
		MessageID:      msgID,
	}, true
}

// downloadDiscordAttachment fetches an attachment's bytes from Discord's CDN.
// Unlike Slack's url_private, a Discord attachment URL is a pre-signed link
// (ex/is/hm query params) that needs no Authorization header — a plain GET.
func downloadDiscordAttachment(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord attachment unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord attachment fetch failed (status %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("attachment exceeds the size limit")
	}
	return data, nil
}

// NewDiscord creates (does not start) a Discord adapter.
func NewDiscord(token, ownerWorkspaceID string, dispatch DispatchFunc) (*DiscordGateway, error) {
	sess, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("discord session init: %w", err)
	}
	sess.Identify.Intents = discordgo.IntentDirectMessages | discordgo.IntentMessageContent
	g := &DiscordGateway{session: sess, ownerWorkspaceID: ownerWorkspaceID, dispatch: dispatch, dmChannels: map[string]string{}}
	sess.AddHandler(g.onMessageCreate)
	return g, nil
}

func (g *DiscordGateway) Platform() string    { return "discord" }
func (g *DiscordGateway) OwnerUserID() string { return g.ownerWorkspaceID }

func (g *DiscordGateway) Start(ctx context.Context) error {
	if err := g.session.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}
	<-ctx.Done()
	return g.session.Close()
}

func (g *DiscordGateway) Stop() error { return g.session.Close() }

func (g *DiscordGateway) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	msg, ok := mapDiscordDM(m.Author.ID, m.GuildID, m.Content, m.ID, botID, m.Author.Bot)
	if !ok {
		return
	}
	msg.WorkspaceID = g.ownerWorkspaceID

	// Discord delivers a file as a URL on the message rather than a file id;
	// only the first attachment is imported (chat attachments are single-file,
	// same as Telegram). A download failure is logged and otherwise
	// swallowed — the message still dispatches on its text alone.
	if len(m.Attachments) > 0 {
		att := m.Attachments[0]
		if data, err := downloadDiscordAttachment(att.URL); err == nil {
			name := att.Filename
			if name == "" {
				name = "attachment"
			}
			msg.Attachment = &Attachment{Filename: name, Data: data}
		} else {
			fmt.Printf("gateway: discord attachment download failed: %v\n", err)
		}
	}

	g.dispatch(context.Background(), msg)
}

// resolveDM returns the DM channel id for a user id, opening (idempotently) + caching it.
func (g *DiscordGateway) resolveDM(userID string) (string, error) {
	g.mu.Lock()
	if ch, ok := g.dmChannels[userID]; ok {
		g.mu.Unlock()
		return ch, nil
	}
	g.mu.Unlock()
	ch, err := g.session.UserChannelCreate(userID)
	if err != nil {
		return "", fmt.Errorf("discord open DM: %w", err)
	}
	g.mu.Lock()
	g.dmChannels[userID] = ch.ID
	g.mu.Unlock()
	return ch.ID, nil
}

// Send delivers a message to a Discord user (via their DM channel).
func (g *DiscordGateway) Send(platformUserID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, err = g.session.ChannelMessageSend(ch, render.For("discord").Render(text))
	return err
}

func (g *DiscordGateway) SendTyping(platformUserID string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	return g.session.ChannelTyping(ch)
}

func (g *DiscordGateway) SendMessageGetID(platformUserID, text string) (string, error) {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return "", err
	}
	sent, err := g.session.ChannelMessageSend(ch, render.For("discord").Render(text))
	if err != nil {
		return "", err
	}
	return sent.ID, nil
}

func (g *DiscordGateway) EditMessage(platformUserID, msgID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, err = g.session.ChannelMessageEdit(ch, msgID, render.For("discord").Render(text))
	return err
}

func (g *DiscordGateway) DeleteMessage(platformUserID, msgID string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	return g.session.ChannelMessageDelete(ch, msgID)
}
