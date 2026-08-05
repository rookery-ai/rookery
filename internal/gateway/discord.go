package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ilijad1/rookery/internal/gateway/render"
	"github.com/ilijad1/rookery/internal/iolimit"
	"github.com/ilijad1/rookery/internal/nethttp"
)

// discordAPIBase is the Discord REST base; overridable in tests.
var discordAPIBase = "https://discord.com/api/v10"

// validateDiscordToken confirms a bot token by fetching the bot user, returning its identity.
func validateDiscordToken(token string) (BotIdentity, error) {
	req, err := http.NewRequest(http.MethodGet, discordAPIBase+"/users/@me", nil)
	if err != nil {
		return BotIdentity{}, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return BotIdentity{}, fmt.Errorf("discord api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return BotIdentity{}, fmt.Errorf("discord rejected token (status %d)", resp.StatusCode)
	}
	var out struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	// A bot's user id IS its application id, which is what the invite URL needs.
	if err := json.Unmarshal(body, &out); err != nil || out.Username == "" {
		return BotIdentity{}, fmt.Errorf("invalid response from discord")
	}
	return BotIdentity{Username: out.Username, UserID: out.ID}, nil
}

func init() {
	RegisterCredSpec(CredSpec{
		Platform: "discord",
		Label:    "Discord",
		Blurb:    "Chat with your agents via a personal Discord bot (DMs)",
		Fields:   []CredField{{Key: "token", Label: "Bot Token", Placeholder: "your bot token", Secret: true}},
		SetupURL: "https://discord.com/developers/applications",
		SetupSteps: []string{
			"Open the Discord Developer Portal and click New Application",
			"Open the Bot tab, click Reset Token, and copy the token",
			"Still on the Bot tab, enable the MESSAGE CONTENT INTENT under Privileged Gateway Intents",
		},
		Validate: func(v map[string]string) (BotIdentity, error) { return validateDiscordToken(v["token"]) },
		LinkURLs: func(b BotIdentity) LinkTargets {
			if b.UserID == "" {
				return LinkTargets{}
			}
			return LinkTargets{
				DMURL: "https://discord.com/users/" + b.UserID,
				// permissions=0: guild permissions do not govern 1:1 DMs, so a
				// DM-only bot needs none. It also creates no role on join and
				// the consent screen asks for nothing.
				//
				// applications.commands is required for the app's slash
				// commands to appear in the guild — `bot` alone authorizes the
				// bot user but surfaces no commands, so /start would silently
				// not exist there. A guild authorized before this change must
				// be re-invited once for the command to show up.
				InviteURL: "https://discord.com/api/oauth2/authorize?client_id=" +
					url.QueryEscape(b.UserID) + "&scope=bot%20applications.commands&permissions=0",
			}
		},
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

// discordCDNHosts allowlists the only hosts a Discord attachment URL should
// ever point at. An allowlist is appropriate specifically because this URL is
// NOT attacker-controlled message text the way a web_fetch target is — it is
// a pre-signed link Discord's own gateway payload hands us for an object
// Discord itself hosts. Pinning to Discord's known CDN hosts means even a
// malformed/tampered payload (or a bug upstream in discordgo) can't be used
// to make this code path reach an arbitrary internal or external address —
// on top of, not instead of, the private-address dial guard below.
// Var (not const) so a test can point it at a hermetic fake server.
var discordCDNHosts = map[string]bool{
	"cdn.discordapp.com":   true,
	"media.discordapp.net": true,
}

// discordAttachmentClient is the guarded HTTP client used for attachment
// downloads (see internal/nethttp) — it refuses to dial private/loopback
// address space, the same protection internal/coder's web_fetch tool uses.
// Var so a test can substitute an unguarded client: the real guard rejects
// loopback outright, so a hermetic httptest server (which always binds
// 127.0.0.1) could never be reached through the production client.
var discordAttachmentClient = nethttp.GuardedClient(20 * time.Second)

// downloadDiscordAttachment fetches an attachment's bytes from Discord's CDN.
// Unlike Slack's url_private, a Discord attachment URL is a pre-signed link
// (ex/is/hm query params) that needs no Authorization header — a plain GET.
func downloadDiscordAttachment(rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid attachment url: %w", err)
	}
	if !discordCDNHosts[parsed.Hostname()] {
		return nil, fmt.Errorf("attachment host %q is not a recognised discord cdn host", parsed.Hostname())
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := discordAttachmentClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord attachment unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord attachment fetch failed (status %d)", resp.StatusCode)
	}
	data, err := iolimit.ReadCapped(resp.Body, maxAttachmentBytes)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// startCommand is the application command registered when the adapter opens.
//
// Typing "/" in Discord opens the client's slash-command picker, so telling an
// operator to "send /start" as a plain message fights Discord's own UI — and a
// /start typed in a server channel is discarded by mapDiscordDM with NO reply
// at all, while a wrong DM at least answers. Absolute silence for the most
// likely first action was the defect.
//
// An application command is delivered as an INTERACTION, so it works in both a
// DM and a guild and needs neither IntentMessageContent nor
// IntentGuildMessages — the adapter's DM-only intent set is unchanged. That is
// why this is preferred over replying to guild messages, which would have the
// bot receive every message in every server it joins.
var startCommand = &discordgo.ApplicationCommand{
	Name:        "start",
	Description: "Link your account to this workspace",
}

// discordInteractionUserID resolves the invoking user. Discord populates
// Interaction.User in a DM and Interaction.Member.User in a guild; reading only
// one returns nil in the other context, which would reintroduce exactly the
// silence this command exists to remove.
func discordInteractionUserID(i *discordgo.InteractionCreate) string {
	if i == nil || i.Interaction == nil {
		return ""
	}
	if i.User != nil {
		return i.User.ID
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
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
	sess.AddHandler(g.onInteractionCreate)
	return g, nil
}

// onInteractionCreate handles the /start application command. The
// message-based /start path in onMessageCreate is KEPT, not replaced: it is
// the only path on Telegram and Slack, it already works, and a freshly
// registered global command does not propagate instantly. Both converge on the
// same Router.handleStart via dispatch.
func (g *DiscordGateway) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != startCommand.Name {
		return
	}
	userID := discordInteractionUserID(i)
	if userID == "" {
		return
	}

	// Acknowledge FIRST and ephemerally. The dispatch below reaches the DB and
	// the router, and Discord expires an unacknowledged interaction after 3
	// seconds; ephemeral keeps the reply out of a public channel, so linking
	// never posts the operator's business where others can read it.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Linking your account — I'll reply in your direct messages.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Warn("gateway: discord interaction ack failed", "err", err)
	}

	g.dispatch(context.Background(), Message{
		Platform:       "discord",
		PlatformUserID: userID,
		WorkspaceID:    g.ownerWorkspaceID,
		Text:           "/start",
	})
}

func (g *DiscordGateway) Platform() string    { return "discord" }
func (g *DiscordGateway) OwnerUserID() string { return g.ownerWorkspaceID }

func (g *DiscordGateway) Start(ctx context.Context) error {
	if err := g.session.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}

	// Best-effort: a registration failure must never stop the adapter. The
	// message-based /start still works, so a bot that cannot register commands
	// is degraded, not broken. Registered globally (empty guild id) because a
	// guild-scoped command is not available in DMs, which is where this bot
	// actually operates.
	if g.session.State != nil && g.session.State.User != nil {
		if _, err := g.session.ApplicationCommandCreate(g.session.State.User.ID, "", startCommand); err != nil {
			slog.Warn("gateway: discord /start command registration failed", "err", err)
		}
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
	// same as Telegram). A download failure is logged AND surfaced as an
	// explicit Attachment.Err — never silently swallowed into an empty-text
	// dispatch, which the router could misread as an answer to an unrelated
	// pending flow (see Router.Handle).
	if len(m.Attachments) > 0 {
		att := m.Attachments[0]
		name := att.Filename
		if name == "" {
			name = "attachment"
		}
		if data, err := downloadDiscordAttachment(att.URL); err == nil {
			msg.Attachment = &Attachment{Filename: name, Data: data}
		} else {
			slog.Warn("gateway: discord attachment download failed", "err", err)
			msg.Attachment = &Attachment{Filename: name, Err: err}
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
// Send delivers text to the user's DM, splitting it across messages when it
// exceeds Discord's 2000-character limit.
//
// Before chunking, an over-long message was rejected with an HTTP 400 whose
// error dispatch then discarded — so the agent overview a user needs in order to
// approve a build was silently dropped while every short message arrived.
func (g *DiscordGateway) Send(platformUserID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	// Split the RENDERED text: the limit applies to what is transmitted, and each
	// platform's renderer produces different output.
	return sendChunked(render.For("discord").Render(text), discordMessageLimit,
		func(chunk string) error {
			_, err := g.session.ChannelMessageSend(ch, chunk)
			return err
		})
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
	// The FIRST chunk is the placeholder anchor; any remainder follows as
	// ordinary messages, since only one message can be edited later.
	chunks := splitMessage(render.For("discord").Render(text), discordMessageLimit)
	sent, err := g.session.ChannelMessageSend(ch, chunks[0])
	if err != nil {
		return "", err
	}
	for _, chunk := range chunks[1:] {
		if _, err := g.session.ChannelMessageSend(ch, chunk); err != nil {
			return sent.ID, err
		}
	}
	return sent.ID, nil
}

func (g *DiscordGateway) EditMessage(platformUserID, msgID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	// An edit can only carry one message's worth. Put the first chunk in the
	// placeholder and send the rest as follow-ups, so a long result still arrives
	// in full rather than failing the edit and vanishing.
	chunks := splitMessage(render.For("discord").Render(text), discordMessageLimit)
	if _, err = g.session.ChannelMessageEdit(ch, msgID, chunks[0]); err != nil {
		return err
	}
	for _, chunk := range chunks[1:] {
		if _, err := g.session.ChannelMessageSend(ch, chunk); err != nil {
			// Deliberately NOT returned. GatewayManager.dispatch treats an
			// EditMessage error as "the edit did not happen" and re-sends the ENTIRE
			// message as a new one — so reporting a follow-up failure after the edit
			// already succeeded would deliver chunk 1 twice plus the whole text
			// again. Log it and report the edit's own outcome.
			slog.Error("gateway: discord follow-up chunk failed",
				"channel", ch, "chars", msgLen(chunk), "err", err)
			return nil
		}
	}
	return nil
}

func (g *DiscordGateway) DeleteMessage(platformUserID, msgID string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	return g.session.ChannelMessageDelete(ch, msgID)
}
