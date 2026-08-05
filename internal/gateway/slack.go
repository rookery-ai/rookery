package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/ilijad1/rookery/internal/gateway/render"
	"github.com/ilijad1/rookery/internal/iolimit"
)

// slackAPINew builds a *slack.Client; indirected for tests.
var slackAPINew = func(botToken string, opts ...slack.Option) *slack.Client {
	return slack.New(botToken, opts...)
}

// validateSlackToken confirms a bot token via auth.test, returning the bot identity.
func validateSlackToken(botToken string) (BotIdentity, error) {
	resp, err := slackAPINew(botToken).AuthTest()
	if err != nil {
		return BotIdentity{}, fmt.Errorf("slack rejected bot token: %w", err)
	}
	return BotIdentity{Username: resp.User, UserID: resp.UserID, TeamID: resp.TeamID}, nil
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
		// Manifest-based creation, replacing seven manual steps. It is not merely
		// shorter: slash commands cannot be registered with a bot token, so the
		// manifest is the ONLY way to declare them without a rotating
		// app-configuration token. Scopes, the message.im subscription, Socket
		// Mode and App Home all arrive declared in the same paste.
		//
		// An app created the old way keeps working but has no slash commands, and
		// Rookery cannot add them — hence the final step.
		SetupSteps: []string{
			"Go to api.slack.com/apps and click Create New App → From an app manifest",
			"Pick your workspace, then paste the manifest shown below and create the app",
			"Open Basic Information → App-Level Tokens, generate a token with the connections:write scope, and copy the xapp- token",
			"Click Install to Workspace, then copy the xoxb- Bot Token from OAuth & Permissions",
			"Already have a Rookery Slack app? Open App Manifest, replace it with the manifest below, and reinstall — that is what adds the slash commands",
		},
		SetupManifest: SlackAppManifest,
		Validate: func(v map[string]string) (BotIdentity, error) {
			if v["app_token"] == "" {
				return BotIdentity{}, fmt.Errorf("app-level token (xapp-) is required for Socket Mode")
			}
			return validateSlackToken(v["token"])
		},
		LinkURLs: func(b BotIdentity) LinkTargets {
			if b.UserID == "" || b.TeamID == "" {
				return LinkTargets{}
			}
			// A single https://app.slack.com/client/<team>/<user> deep link
			// resolves the same DM whether it's opened by the desktop app or a
			// browser — no separate slack:// scheme is used.
			return LinkTargets{DMURL: "https://app.slack.com/client/" + b.TeamID + "/" + b.UserID}
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
// ok=false for the bot's own messages, bot messages, non-DM channels, and
// every subtyped message EXCEPT file_share — a file upload arrives as a
// message with subtype "file_share" (its text is often empty; the file IS
// the message), so it must pass through to be dispatched with an
// Attachment. Every other subtype (message_changed, channel_join, message
// edits, …) is still dropped.
func mapSlackDM(user, channelType, text, ts, botID, subType, botUserID string) (Message, bool) {
	if channelType != "im" || botID != "" || user == "" || user == botUserID {
		return Message{}, false
	}
	if subType != "" && subType != "file_share" {
		return Message{}, false
	}
	return Message{Platform: "slack", PlatformUserID: user, Text: text, MessageID: ts}, true
}

// slackFileDownloader is the seam for downloading a Slack file's bytes,
// satisfied by *slack.Client.GetFile. Storing it as an interface field on
// SlackGateway (rather than calling g.api.GetFile directly) lets a test
// inject a fake without a live Slack workspace or bot token.
type slackFileDownloader interface {
	GetFile(downloadURL string, w io.Writer) error
}

// slackFileHosts allowlists the only host a Slack file download URL should
// ever point at. Belt-and-braces, mirroring the Discord CDN allowlist: this
// URL comes from Slack's own event payload rather than attacker-controlled
// message text, but pinning to Slack's known file host means even a
// malformed/tampered payload can't be used to make this code path reach an
// arbitrary internal or external address. Var (not const) so a test can
// point it at a hermetic fake host.
var slackFileHosts = map[string]bool{
	"files.slack.com": true,
}

// looksLikeHTMLSignIn reports whether b begins (after optional leading
// whitespace/BOM) with an HTML doctype or <html> tag, case-insensitively.
// This is the shape of Slack's web sign-in page — the page a
// url_private_download request returns with HTTP 200 (not an error status)
// when the bot token lacks the files:read scope or has expired. Deliberately
// a cheap prefix check, not a parser: distinguishing "this is HTML" from
// "this is the specific file format the caller expected" needs nothing more,
// and pulling in an HTML parser here would be a dependency this repo doesn't
// otherwise need for the job.
func looksLikeHTMLSignIn(b []byte) bool {
	b = bytes.TrimLeft(b, "\xef\xbb\xbf \t\r\n")
	// Only the first few bytes matter for a prefix check — capping here
	// avoids lowercasing (and thus allocating a copy of) the whole file,
	// including a multi-MB non-HTML upload that could never match anyway.
	// 16 comfortably covers the longest prefix checked ("<!doctype html").
	if len(b) > 16 {
		b = b[:16]
	}
	lower := bytes.ToLower(b)
	return bytes.HasPrefix(lower, []byte("<!doctype html")) || bytes.HasPrefix(lower, []byte("<html"))
}

// isDeclaredHTML reports whether Slack's own file metadata says the file
// genuinely is HTML — in which case looksLikeHTMLSignIn matching is expected
// and must NOT be treated as a failed download.
func isDeclaredHTML(mimetype string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimetype)), "text/html")
}

// downloadSlackFile fetches a Slack file's bytes via the downloader seam,
// bounded by an iolimit.CappingWriter. GetFile streams into a plain
// io.Writer with no size bound of its own — capping the WRITE side (rather
// than reading a response body, which slack.Client.GetFile does internally
// and doesn't expose) is what stops an oversized file from being buffered
// in full before anyone notices it's too big.
//
// declaredMimetype is the file's Mimetype as reported in Slack's own event
// payload (me.Message.Files[0].Mimetype) — the file's ADVERTISED type,
// independent of whatever bytes actually came back. It exists to catch a
// mis-scoped or expired bot token: url_private_download responds HTTP 200
// with an HTML sign-in page rather than an error when the token lacks
// files:read, and that HTML would otherwise sail straight through GetFile's
// non-200 check (which only inspects the status code) and get filed as a
// plausible-looking note that silently REPLACES the user's actual file. A
// download is only rejected when the bytes look like HTML AND the file
// wasn't actually declared as HTML — a genuine text/html upload still
// imports normally.
func downloadSlackFile(dl slackFileDownloader, downloadURL, declaredMimetype string) ([]byte, error) {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("invalid file url: %w", err)
	}
	if !slackFileHosts[parsed.Hostname()] {
		return nil, fmt.Errorf("file host %q is not a recognised slack file host", parsed.Hostname())
	}
	var buf bytes.Buffer
	cw := iolimit.NewCappingWriter(&buf, maxAttachmentBytes)
	if err := dl.GetFile(downloadURL, cw); err != nil {
		return nil, fmt.Errorf("slack file download: %w", err)
	}
	if looksLikeHTMLSignIn(buf.Bytes()) && !isDeclaredHTML(declaredMimetype) {
		return nil, fmt.Errorf("download returned a sign-in page, not the file — the Slack connection is likely missing the files:read scope; reconnect Slack to grant it")
	}
	return buf.Bytes(), nil
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
	downloader       slackFileDownloader

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
		downloader:       api, // *slack.Client satisfies slackFileDownloader
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
	} else {
		// Non-fatal: the bot_id filter in mapSlackDM is the primary self-message
		// guard; botUserID is a secondary check. Log so it isn't silent.
		fmt.Printf("gateway: slack AuthTest failed for workspace %s (self-message filter relies on bot_id only): %v\n", g.ownerWorkspaceID, err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel() // when RunContext returns (ctx-cancel OR reconnect failure), stop readLoop
	go g.readLoop(loopCtx)
	return g.sm.RunContext(ctx)
}

func (g *SlackGateway) Stop() error { return nil } // RunContext exits on ctx cancel

// slackAttachmentFromEvent builds the KB Attachment for a file_share event, or
// nil when the event carries no file. Pure and testable — the readLoop just
// calls it, so the "which field holds the files" decision is covered by a
// unit test rather than only by the live socketmode transport.
//
// A file_share message carries its file(s) on the event itself, not as a URL
// in the text — only the first file is imported (chat attachments are
// single-file, same as Telegram/Discord). slackevents.MessageEvent has no
// Files field of its own; for a non-message_changed event its custom
// UnmarshalJSON populates Message from the SAME top-level payload, and
// slack.Msg IS where "files" unmarshals to — hence me.Message.Files, not
// me.Files. A download failure is logged AND surfaced as an explicit
// Attachment.Err — never silently swallowed into an empty-text dispatch,
// which the router could misread as an answer to an unrelated pending flow.
func slackAttachmentFromEvent(me *slackevents.MessageEvent, dl slackFileDownloader) *Attachment {
	if me == nil || me.Message == nil || len(me.Message.Files) == 0 {
		return nil
	}
	f := me.Message.Files[0]
	name := f.Name
	if name == "" {
		name = "attachment"
	}
	data, err := downloadSlackFile(dl, f.URLPrivateDownload, f.Mimetype)
	if err != nil {
		slog.Warn("gateway: slack file download failed", "err", err)
		return &Attachment{Filename: name, Err: err}
	}
	return &Attachment{Filename: name, Data: data}
}

func (g *SlackGateway) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-g.sm.Events:
			// handleEvent recovers its own panics (see below): readLoop is a
			// long-lived background goroutine with no supervisor, and it now runs
			// panic-capable code per event (the SDK file download), so a single
			// malformed event must drop that event, not take down the loop and the
			// whole server process. This is the same guarantee dispatchFunc gives
			// the synchronous path — extended to the one goroutine that does work
			// before reaching it.
			g.handleEvent(evt)
		}
	}
}

// handleEvent processes one Socket Mode event under a panic guard so a bad event
// cannot crash the read loop or the process.
func (g *SlackGateway) handleEvent(evt socketmode.Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("gateway: recovered panic handling slack event",
				"workspace_id", g.ownerWorkspaceID, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	if evt.Type == socketmode.EventTypeSlashCommand {
		g.handleSlashCommand(evt)
		return
	}
	if evt.Type != socketmode.EventTypeEventsAPI {
		return
	}
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if evt.Request != nil {
		g.sm.Ack(*evt.Request)
	}
	if eventsAPIEvent.Type != slackevents.CallbackEvent {
		return
	}
	me, ok := eventsAPIEvent.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return
	}
	g.mu.Lock()
	botID := g.botUserID
	g.mu.Unlock()
	msg, ok := mapSlackDM(me.User, me.ChannelType, me.Text, me.TimeStamp, me.BotID, me.SubType, botID)
	if !ok {
		return
	}
	msg.WorkspaceID = g.ownerWorkspaceID
	msg.Attachment = slackAttachmentFromEvent(me, g.downloader)
	g.dispatch(context.Background(), msg)
}

// slashCommandText rebuilds the router's text form from a Slack slash command,
// mapping the Slack-side name back to the canonical one: "/rookery-remind" with
// text "in 10 minutes to check oven" becomes "/remind in 10 minutes to check
// oven". Exported shape kept as a pure function so the mapping is testable
// without a Socket Mode connection.
func slashCommandText(command, text string) string {
	name := CanonicalCommandName("slack", command)
	if text = strings.TrimSpace(text); text != "" {
		return "/" + name + " " + text
	}
	return "/" + name
}

// handleSlashCommand routes a Slack slash command into the same router path a
// typed message takes.
//
// The ack is ephemeral and says the reply is coming by DM: a slash command can
// be invoked from a channel, while Rookery always replies in the DM, so without
// this an in-channel invocation looks like it did nothing at all.
func (g *SlackGateway) handleSlashCommand(evt socketmode.Event) {
	cmd, ok := evt.Data.(slack.SlashCommand)
	if !ok {
		return
	}
	if evt.Request != nil {
		g.sm.Ack(*evt.Request, map[string]string{
			"response_type": "ephemeral",
			"text":          "Working on it — I'll reply in your direct messages.",
		})
	}
	if cmd.UserID == "" {
		return
	}
	g.dispatch(context.Background(), Message{
		Platform:       "slack",
		PlatformUserID: cmd.UserID,
		WorkspaceID:    g.ownerWorkspaceID,
		Text:           slashCommandText(cmd.Command, cmd.Text),
	})
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
