package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/gateway/render"
	telebot "gopkg.in/telebot.v4"
)

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
	g.bot.Handle("/start", g.handle)
	g.bot.Handle("/help", g.handle)
	g.bot.Handle("/agent", g.handle)
	g.bot.Handle("/secret", g.handle)
	g.bot.Handle("/remind", g.handle)
	g.bot.Handle("/run", g.handle)
	g.bot.Handle("/chat", g.handle)
	g.bot.Handle("/memory", g.handle)

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
	rendered := render.For("telegram").Render(text)
	_, err = g.bot.Send(chat, rendered, telebot.ModeMarkdownV2)
	if err != nil {
		// Fall back to plain text if MarkdownV2 parsing fails.
		_, err = g.bot.Send(chat, text) // plain-text fallback uses the neutral source
	}
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
	rendered := render.For("telegram").Render(text)
	sent, err := g.bot.Send(chat, rendered, telebot.ModeMarkdownV2)
	if err != nil {
		sent, err = g.bot.Send(chat, text) // plain-text fallback uses the neutral source
	}
	if err != nil {
		return "", err
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
	msg := &telebot.Message{ID: id, Chat: &telebot.Chat{ID: chatID}}
	rendered := render.For("telegram").Render(text)
	_, err = g.bot.Edit(msg, rendered, telebot.ModeMarkdownV2)
	if err != nil {
		// Fall back to plain text if markdown parsing fails on the response.
		_, err = g.bot.Edit(msg, text) // plain-text fallback uses the neutral source
	}
	return err
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
	data, err := io.ReadAll(io.LimitReader(rc, maxAttachmentBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxAttachmentBytes {
		return nil, "", fmt.Errorf("attachment exceeds the size limit")
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
	})
}

func init() {
	RegisterAdapter("telegram", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return NewTelegram(token, ws, d)
	})
}
