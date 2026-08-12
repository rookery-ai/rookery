package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/gateway/render"
	"github.com/rookery-ai/rookery/internal/iolimit"
	telebot "gopkg.in/telebot.v4"
)

// telegramCommands maps the shared table onto telebot's Command type for
// setMyCommands. Telegram rejects the whole call if any single entry is invalid
// — names must be lowercase letters, digits and underscores (1-32) and
// descriptions 3-256 characters — which is why TestTelegramCommandNamesAreValid
// asserts the constraint rather than trusting the table to stay compliant.
func telegramCommands() []telebot.Command {
	out := make([]telebot.Command, 0, len(Commands))
	for _, c := range Commands {
		out = append(out, telebot.Command{Text: c.Name, Description: c.Description})
	}
	return out
}

// TelegramGateway is one user's Telegram bot instance.
type TelegramGateway struct {
	bot              *telebot.Bot
	ownerWorkspaceID string
	dispatch         DispatchFunc
}

// NewTelegram creates but does not start a Telegram bot adapter.
func NewTelegram(token, ownerWorkspaceID string, dispatch DispatchFunc) (*TelegramGateway, error) {
	settings := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	}
	bot, err := telebot.NewBot(settings)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	return &TelegramGateway{
		bot:              bot,
		ownerWorkspaceID: ownerWorkspaceID,
		dispatch:         dispatch,
	}, nil
}

func (g *TelegramGateway) Platform() string    { return "telegram" }
func (g *TelegramGateway) OwnerUserID() string { return g.ownerWorkspaceID }

// Start registers handlers and runs the bot's long-poll loop.
// Blocks until ctx is cancelled or the bot stops.
func (g *TelegramGateway) Start(ctx context.Context) error {
	g.bot.Handle(telebot.OnText, g.handle)
	g.bot.Handle(telebot.OnDocument, g.handle)
	g.bot.Handle(telebot.OnPhoto, g.handle)
	// Registered from the shared table rather than a hand-written list, which had
	// already drifted: /skill, /pending, /approve and /reject were absent. They
	// still reached the router via the OnText fallback, so nothing was broken —
	// but the list read as the set of supported commands and was not.
	for _, c := range Commands {
		g.bot.Handle("/"+c.Name, g.handle)
	}

	// Populate the client's Menu button. Telegram delivers commands as ordinary
	// text messages whether or not they are registered here, so this is purely
	// discoverability and a failure must never stop the adapter — a bot that
	// cannot advertise its commands is degraded, not broken.
	if err := g.bot.SetCommands(telegramCommands()); err != nil {
		slog.Warn("gateway: telegram setMyCommands failed", "err", err)
	}

	// Stop the bot when context is cancelled.
	go func() {
		<-ctx.Done()
		g.bot.Stop()
	}()

	g.bot.Start()
	return nil
}

func (g *TelegramGateway) Stop() error {
	g.bot.Stop()
	return nil
}

// Send delivers a text message to a Telegram chat ID.
func (g *TelegramGateway) Send(platformUserID, text string) error {
	chatID, err := strconv.ParseInt(platformUserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat id %q: %w", platformUserID, err)
	}
	chat := &telebot.Chat{ID: chatID}
	for _, chunk := range telegramChunks(text) {
		if err := g.sendChunk(chat, chunk); err != nil {
			return err
		}
	}
	return nil
}

// telegramChunks splits the NEUTRAL source, not the rendered form, so each chunk
// has one and only one plain-text counterpart. Splitting the rendered text
// instead would leave the MarkdownV2 and plain-text paths with different chunk
// counts, and the fallback would have to guess which plain chunk corresponds to
// a failed rendered one.
//
// MarkdownV2 escaping only ever grows a string (it inserts backslashes), so a
// neutral chunk at the limit can render past it. sendChunk handles that by
// falling back to the neutral chunk, which fits by construction.
func telegramChunks(text string) []string {
	return splitMessage(text, telegramMessageLimit)
}

// sendChunk sends one neutral chunk, preferring MarkdownV2 and degrading to
// plain text when the rendered form either overflows the limit or fails to
// parse. The plain form is the neutral chunk, which is already within the limit.
func (g *TelegramGateway) sendChunk(chat *telebot.Chat, chunk string) error {
	rendered := render.For("telegram").Render(chunk)
	if msgLen(rendered) <= telegramMessageLimit {
		if _, err := g.bot.Send(chat, rendered, telebot.ModeMarkdownV2); err == nil {
			return nil
		}
	}
	_, err := g.bot.Send(chat, chunk)
	return err
}

// SendTyping sends a typing action to the chat.
func (g *TelegramGateway) SendTyping(platformUserID string) error {
	chatID, err := strconv.ParseInt(platformUserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat id %q: %w", platformUserID, err)
	}
	return g.bot.Notify(&telebot.Chat{ID: chatID}, telebot.Typing)
}

// SendMessageGetID sends a message and returns its Telegram message ID.
func (g *TelegramGateway) SendMessageGetID(platformUserID, text string) (string, error) {
	chatID, err := strconv.ParseInt(platformUserID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid telegram chat id %q: %w", platformUserID, err)
	}
	chat := &telebot.Chat{ID: chatID}
	// The FIRST chunk is the placeholder anchor; any remainder follows as
	// ordinary messages, since only one message can be edited later.
	chunks := telegramChunks(text)
	rendered := render.For("telegram").Render(chunks[0])
	sent, err := g.bot.Send(chat, rendered, telebot.ModeMarkdownV2)
	if err != nil {
		sent, err = g.bot.Send(chat, chunks[0]) // plain-text fallback uses the neutral source
	}
	if err != nil {
		return "", err
	}
	for _, chunk := range chunks[1:] {
		if err := g.sendChunk(chat, chunk); err != nil {
			return strconv.Itoa(sent.ID), err
		}
	}
	return strconv.Itoa(sent.ID), nil
}

// DeleteMessage removes a message from the chat (e.g. to redact a typed password).
func (g *TelegramGateway) DeleteMessage(platformUserID, msgID string) error {
	chatID, err := strconv.ParseInt(platformUserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat id %q: %w", platformUserID, err)
	}
	id, err := strconv.Atoi(msgID)
	if err != nil {
		return fmt.Errorf("invalid telegram message id %q: %w", msgID, err)
	}
	return g.bot.Delete(&telebot.Message{ID: id, Chat: &telebot.Chat{ID: chatID}})
}

// EditMessage replaces the text of an existing message.
func (g *TelegramGateway) EditMessage(platformUserID, msgID, text string) error {
	chatID, err := strconv.ParseInt(platformUserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat id %q: %w", platformUserID, err)
	}
	id, err := strconv.Atoi(msgID)
	if err != nil {
		return fmt.Errorf("invalid telegram message id %q: %w", msgID, err)
	}
	chat := &telebot.Chat{ID: chatID}
	msg := &telebot.Message{ID: id, Chat: chat}
	// An edit carries one message's worth. The first chunk replaces the
	// placeholder and the rest follow as new messages, so a long result arrives
	// in full instead of failing the edit and disappearing.
	chunks := telegramChunks(text)
	rendered := render.For("telegram").Render(chunks[0])
	_, err = g.bot.Edit(msg, rendered, telebot.ModeMarkdownV2)
	if err != nil {
		// Fall back to plain text if markdown parsing fails on the response.
		_, err = g.bot.Edit(msg, chunks[0]) // plain-text fallback uses the neutral source
	}
	if err != nil {
		return err
	}
	for _, chunk := range chunks[1:] {
		if err := g.sendChunk(chat, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (g *TelegramGateway) handle(tc telebot.Context) error {
	chat := tc.Chat()
	sender := tc.Sender()
	if chat == nil || sender == nil {
		return nil
	}

	msg := Message{
		Platform:       "telegram",
		PlatformUserID: strconv.FormatInt(chat.ID, 10),
		WorkspaceID:    g.ownerWorkspaceID,
		Text:           tc.Text(),
		MessageID:      strconv.Itoa(tc.Message().ID),
	}

	// Commands arrive as "/cmd" from telebot — ensure leading slash is present.
	if cmd := tc.Message().Entities; len(cmd) > 0 && tc.Message().Entities[0].Type == telebot.EntityCommand {
		if msg.Text == "" {
			msg.Text = "/" + tc.Message().Text
		}
	}

	// Telegram delivers a file as a Document (or a Photo, which telebot has
	// already reduced from the size-array Telegram sends down to the single
	// largest entry). Either way the bytes come from a two-step getFile →
	// download, which telebot wraps in File/Download. A download failure is
	// logged AND surfaced as an explicit Attachment.Err — never silently
	// swallowed into an empty-text dispatch, which the router could misread as
	// an answer to an unrelated pending flow (see Router.Handle).
	if doc := tc.Message().Document; doc != nil {
		name := doc.FileName
		if data, resolvedName, err := g.downloadTelegramFile(doc.File, name); err == nil {
			msg.Attachment = &Attachment{Filename: resolvedName, Data: data}
		} else {
			slog.Warn("telegram: attachment download failed", "err", err)
			msg.Attachment = &Attachment{Filename: name, Err: err}
		}
	} else if photo := tc.Message().Photo; photo != nil {
		if data, name, err := g.downloadTelegramFile(photo.File, "photo.jpg"); err == nil {
			msg.Attachment = &Attachment{Filename: name, Data: data}
		} else {
			slog.Warn("telegram: attachment download failed", "err", err)
			msg.Attachment = &Attachment{Filename: "photo.jpg", Err: err}
		}
	}

	ctx := context.Background()
	g.dispatch(ctx, msg)
	return nil
}

// downloadTelegramFile fetches an attachment's bytes via the bot API.
func (g *TelegramGateway) downloadTelegramFile(f telebot.File, name string) ([]byte, string, error) {
	rc, err := g.bot.File(&f)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := iolimit.ReadCapped(rc, maxAttachmentBytes)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}
	return data, name, nil
}

func init() {
	RegisterCredSpec(CredSpec{
		Platform: "telegram",
		Label:    "Telegram",
		Blurb:    "Chat with your agents via a personal Telegram bot",
		Fields:   []CredField{{Key: "token", Label: "Bot Token", Placeholder: "123456:ABC-...", Secret: true}},
		SetupURL: "https://t.me/BotFather",
		SetupSteps: []string{
			"Open @BotFather in Telegram", "Send /newbot and follow the prompts",
			"Copy the token it gives you and paste it here",
		},
		LinkURLs: func(b BotIdentity) LinkTargets {
			if b.Username == "" {
				return LinkTargets{}
			}
			return LinkTargets{DMURL: "https://t.me/" + strings.TrimPrefix(b.Username, "@")}
		},
	})
}

func init() {
	RegisterAdapter("telegram", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return NewTelegram(token, ws, d)
	})
}
