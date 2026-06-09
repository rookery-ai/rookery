package gateway

import (
	"context"
	"fmt"
	"strconv"
	"time"

	telebot "gopkg.in/telebot.v4"
)

// TelegramGateway is one user's Telegram bot instance.
type TelegramGateway struct {
	bot         *telebot.Bot
	ownerUserID string
	manager     *GatewayManager
}

// NewTelegram creates but does not start a Telegram bot adapter.
func NewTelegram(token, ownerUserID string, manager *GatewayManager) (*TelegramGateway, error) {
	settings := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	}
	bot, err := telebot.NewBot(settings)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	return &TelegramGateway{
		bot:         bot,
		ownerUserID: ownerUserID,
		manager:     manager,
	}, nil
}

func (g *TelegramGateway) Platform() string    { return "telegram" }
func (g *TelegramGateway) OwnerUserID() string { return g.ownerUserID }

// Start registers handlers and runs the bot's long-poll loop.
// Blocks until ctx is cancelled or the bot stops.
func (g *TelegramGateway) Start(ctx context.Context) error {
	g.bot.Handle(telebot.OnText, g.handle)
	g.bot.Handle("/start", g.handle)
	g.bot.Handle("/help", g.handle)
	g.bot.Handle("/agent", g.handle)
	g.bot.Handle("/secret", g.handle)
	g.bot.Handle("/remind", g.handle)
	g.bot.Handle("/run", g.handle)

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
	_, err = g.bot.Send(chat, text, telebot.ModeMarkdownV2)
	if err != nil {
		// Fall back to plain text if MarkdownV2 parsing fails.
		_, err = g.bot.Send(chat, text)
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
		UserID:         g.ownerUserID, // always route to bot owner
		Text:           tc.Text(),
	}

	// Commands arrive as "/cmd" from telebot — ensure leading slash is present.
	if cmd := tc.Message().Entities; len(cmd) > 0 && tc.Message().Entities[0].Type == telebot.EntityCommand {
		if msg.Text == "" {
			msg.Text = "/" + tc.Message().Text
		}
	}

	ctx := context.Background()
	g.manager.dispatch(ctx, msg)
	return nil
}
