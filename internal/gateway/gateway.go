// Package gateway provides the platform-agnostic message routing layer.
// Each user has one bot per platform (their own bot token). The GatewayManager
// starts/stops adapters and dispatches all incoming messages through the Router.
package gateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

// Message is a normalised inbound message from any platform.
type Message struct {
	Platform       string // "telegram"
	PlatformUserID string // platform-specific user/chat ID
	WorkspaceID    string // resolved internal user ID (empty if not yet linked)
	Text           string
	MessageID      string // platform message ID (used to delete incoming messages)
	Attachment     *Attachment
}

// Attachment is a file a user sent through a chat platform. Adapters download
// the bytes and hand them to the router; conversion and storage happen once,
// in the shared vault.ImportFile path, so a file sent in Telegram lands exactly
// as one uploaded in the web UI does.
//
// Err represents a failed download as an EXPLICIT outcome rather than as
// absence. An adapter that hit a download error still knows a file was sent —
// dropping that down to a nil Attachment would make the message indistinguishable
// from ordinary empty-text traffic, which downstream handlers (a pending
// master-password challenge, in particular) can silently misread as an answer.
// When Err is set, Data is not meaningful and Router.Handle replies with a
// clear failure message instead of attempting to import anything.
type Attachment struct {
	Filename string
	Data     []byte
	Err      error
}

// ParseCommand splits "/cmd arg1 arg2 ..." into (name, remainder).
// Returns ("", text) for non-command messages.
func ParseCommand(text string) (name, arg string) {
	if !strings.HasPrefix(text, "/") {
		return "", text
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "/"), " ", 2)
	name = strings.ToLower(parts[0])
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	return
}

// Gateway is the interface all platform adapters must implement.
type Gateway interface {
	Platform() string
	// OwnerUserID is the internal user ID that owns this bot token.
	OwnerUserID() string
	Start(ctx context.Context) error
	Stop() error
	Send(platformUserID, text string) error
}

// TypingGateway is an optional interface for gateways that support typing
// indicators and message editing (e.g. Telegram). GatewayManager checks for
// this interface at dispatch time to provide the "⏳ Thinking… → edit" UX.
type TypingGateway interface {
	SendTyping(platformUserID string) error
	SendMessageGetID(platformUserID, text string) (string, error)
	EditMessage(platformUserID, msgID, text string) error
}

// DeletableGateway is an optional interface for gateways that can delete
// incoming messages (e.g. to redact a typed master password from chat history).
type DeletableGateway interface {
	DeleteMessage(platformUserID, msgID string) error
}

// DispatchFunc is the callback an adapter invokes for each inbound message.
type DispatchFunc func(ctx context.Context, msg Message)

// AdapterFactory builds a Gateway from decrypted credentials. config is the
// decrypted encrypted_config JSON ("" for single-token platforms).
type AdapterFactory func(token, config, ownerWorkspaceID string, dispatch DispatchFunc) (Gateway, error)

var (
	adapterMu       sync.RWMutex
	adapterRegistry = map[string]AdapterFactory{}
)

// RegisterAdapter registers a platform's factory. Call from an adapter's init()
// or from main() during wiring.
func RegisterAdapter(platform string, f AdapterFactory) {
	adapterMu.Lock()
	defer adapterMu.Unlock()
	adapterRegistry[platform] = f
}

func adapterFactory(platform string) (AdapterFactory, bool) {
	adapterMu.RLock()
	defer adapterMu.RUnlock()
	f, ok := adapterRegistry[platform]
	return f, ok
}

// messageHandler is the subset of *Router's API that GatewayManager depends
// on. It exists so tests can inject a stub that panics — exercising the
// dispatch-time recover() below — without having to coerce a real Router
// (which needs a live DB, coder, etc.) into panicking.
type messageHandler interface {
	Handle(ctx context.Context, msg Message, send func(string), deleteIncoming func(), sendAutoDelete func(string), sendProgress func(string)) error
}

// GatewayManager manages one Gateway per active platform_connection.
// It is safe for concurrent use.
type GatewayManager struct {
	db        *db.DB
	systemKey []byte
	router    messageHandler

	mu       sync.RWMutex
	gateways map[string]Gateway // key: "platform:workspaceID"
	cancels  map[string]context.CancelFunc
}

// New creates a GatewayManager. Call StartAll to bring up all active bots.
func New(database *db.DB, systemKey []byte, router *Router) *GatewayManager {
	return &GatewayManager{
		db:        database,
		systemKey: systemKey,
		router:    router,
		gateways:  make(map[string]Gateway),
		cancels:   make(map[string]context.CancelFunc),
	}
}

// StartAll loads all active platform_connections and starts their adapters.
func (m *GatewayManager) StartAll(ctx context.Context) error {
	connections, err := m.db.ListActivePlatformConnections()
	if err != nil {
		return fmt.Errorf("load connections: %w", err)
	}
	for _, conn := range connections {
		if err := m.start(ctx, conn); err != nil {
			// Log but don't abort — other users' bots should still start.
			fmt.Printf("gateway: failed to start %s for user %s: %v\n", conn.Platform, conn.WorkspaceID, err)
		}
	}
	return nil
}

// StopAll gracefully stops all running adapters.
func (m *GatewayManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, cancel := range m.cancels {
		cancel()
		delete(m.cancels, key)
	}
	for key, gw := range m.gateways {
		_ = gw.Stop()
		delete(m.gateways, key)
	}
}

// Reload starts or stops the gateway for a specific user+platform.
// Call this after a user adds or removes a connector via the web UI.
func (m *GatewayManager) Reload(ctx context.Context, workspaceID, platform string) error {
	// Stop existing if running.
	m.stop(workspaceID, platform)

	conn, err := m.db.GetPlatformConnection(workspaceID, platform)
	if err != nil {
		return nil // connection was deleted — nothing to start
	}
	if !conn.Active {
		return nil
	}
	return m.start(ctx, conn)
}

// Send delivers a message to a platform user on behalf of a user's bot.
func (m *GatewayManager) Send(platform, workspaceID, platformUserID, text string) error {
	m.mu.RLock()
	gw, ok := m.gateways[key(platform, workspaceID)]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no active %s gateway for user %s", platform, workspaceID)
	}
	return gw.Send(platformUserID, text)
}

// SendToUser looks up the user's linked platform identity and sends a message.
// It tries all known platforms in order; the first successful send wins.
// Satisfies the reminder.Sender interface.
func (m *GatewayManager) SendToUser(workspaceID, text string) error {
	// Find all platform identities for this user.
	identities, err := m.db.ListPlatformIdentities(workspaceID, "")
	if err != nil || len(identities) == 0 {
		return fmt.Errorf("no platform identity for user %s", workspaceID)
	}
	for _, identity := range identities {
		if err := m.Send(identity.Platform, workspaceID, identity.PlatformUserID, text); err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to deliver message to user %s on any platform", workspaceID)
}

// ─── Internal ─────────────────────────────────────────────────────────────────

func (m *GatewayManager) start(ctx context.Context, conn *db.PlatformConnection) error {
	token, err := DecryptToken(conn.EncryptedToken, m.systemKey)
	if err != nil {
		return fmt.Errorf("decrypt token: %w", err)
	}
	var config string
	if conn.EncryptedConfig != "" {
		if config, err = DecryptToken(conn.EncryptedConfig, m.systemKey); err != nil {
			return fmt.Errorf("decrypt config: %w", err)
		}
	}
	factory, ok := adapterFactory(conn.Platform)
	if !ok {
		return fmt.Errorf("unsupported platform: %s", conn.Platform)
	}
	gw, err := factory(token, config, conn.WorkspaceID, m.dispatchFunc())
	if err != nil {
		return fmt.Errorf("new %s: %w", conn.Platform, err)
	}

	gwCtx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	k := key(conn.Platform, conn.WorkspaceID)
	m.gateways[k] = gw
	m.cancels[k] = cancel
	m.mu.Unlock()

	go func() {
		if err := gw.Start(gwCtx); err != nil && gwCtx.Err() == nil {
			fmt.Printf("gateway: %s for user %s stopped with error: %v\n", conn.Platform, conn.WorkspaceID, err)
		}
	}()
	return nil
}

func (m *GatewayManager) stop(workspaceID, platform string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(platform, workspaceID)
	if cancel, ok := m.cancels[k]; ok {
		cancel()
		delete(m.cancels, k)
	}
	if gw, ok := m.gateways[k]; ok {
		_ = gw.Stop()
		delete(m.gateways, k)
	}
}

// dispatchFunc returns m.dispatch bound as a DispatchFunc, for injection into
// adapter factories. It is the single funnel every adapter (Telegram, Discord,
// Slack) calls for every inbound message, so it recovers from any panic that
// occurs while handling ONE message: the offending message is dropped, the
// sender gets a best-effort generic error reply, and the adapter's read loop
// (and every other workspace's bot) keeps running. Without this, an
// unrecovered panic anywhere in the dispatch/router path takes down the
// entire process — there is no supervisor restarting it on a home install.
func (m *GatewayManager) dispatchFunc() DispatchFunc {
	return func(ctx context.Context, msg Message) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("gateway: recovered panic handling inbound message",
					"platform", msg.Platform,
					"workspace_id", msg.WorkspaceID,
					"platform_user_id", msg.PlatformUserID,
					"panic", r,
					"stack", string(debug.Stack()),
				)
				m.sendPanicReply(msg)
			}
		}()
		m.dispatch(ctx, msg)
	}
}

// sendPanicReply best-effort notifies the sender that something went wrong,
// after a panic was recovered from dispatch. It guards itself with its own
// recover so a panic inside the send path (e.g. a misbehaving gateway
// implementation) can never escape and re-crash the process.
func (m *GatewayManager) sendPanicReply(msg Message) {
	defer func() {
		_ = recover()
	}()
	if msg.Platform == "" || msg.WorkspaceID == "" || msg.PlatformUserID == "" {
		return
	}
	if err := m.Send(msg.Platform, msg.WorkspaceID, msg.PlatformUserID,
		"⚠️ Something went wrong handling that message."); err != nil {
		slog.Error("gateway: failed to send panic-recovery reply",
			"platform", msg.Platform, "workspace_id", msg.WorkspaceID, "err", err)
	}
}

// dispatch is called by adapters when a message arrives.
// It resolves the platform identity and enforces that only the linked user
// can interact with the bot. Unlinked senders may only use /start.
func (m *GatewayManager) dispatch(ctx context.Context, msg Message) {
	identity, err := m.db.GetPlatformIdentity(msg.Platform, msg.PlatformUserID)
	if err != nil {
		// Sender is not linked. Only /start is permitted.
		cmd, _ := ParseCommand(msg.Text)
		if cmd != "start" {
			_ = m.Send(msg.Platform, msg.WorkspaceID, msg.PlatformUserID,
				"This is a private bot. Send /start to link your account.")
			return
		}
		// For /start, keep msg.WorkspaceID = ownerWorkspaceID so the router can create the identity.
	} else {
		msg.WorkspaceID = identity.WorkspaceID
	}

	// For plain-text messages (not commands), use typing indicator + placeholder
	// message that gets edited with the final response, if the gateway supports it.
	var placeholderID string
	isPlainText := !strings.HasPrefix(msg.Text, "/")
	if isPlainText {
		m.mu.RLock()
		gw, ok := m.gateways[key(msg.Platform, msg.WorkspaceID)]
		m.mu.RUnlock()
		if ok {
			if tg, ok := gw.(TypingGateway); ok {
				_ = tg.SendTyping(msg.PlatformUserID)
				placeholderID, _ = tg.SendMessageGetID(msg.PlatformUserID, "⏳ _Thinking..._")
			}
		}
	}

	// updatePlaceholder edits the placeholder message WITHOUT consuming it, so the
	// final send() call can still do the definitive edit. Used by the router to
	// push mid-generation progress updates (milestones) to the Telegram chat.
	updatePlaceholder := func(text string) {
		if placeholderID == "" {
			return
		}
		m.mu.RLock()
		gw, ok := m.gateways[key(msg.Platform, msg.WorkspaceID)]
		m.mu.RUnlock()
		if ok {
			if tg, ok := gw.(TypingGateway); ok {
				_ = tg.EditMessage(msg.PlatformUserID, placeholderID, text)
			}
		}
	}

	send := func(text string) {
		if placeholderID != "" {
			m.mu.RLock()
			gw, ok := m.gateways[key(msg.Platform, msg.WorkspaceID)]
			m.mu.RUnlock()
			if ok {
				if tg, ok := gw.(TypingGateway); ok {
					if err := tg.EditMessage(msg.PlatformUserID, placeholderID, text); err == nil {
						placeholderID = "" // mark used so subsequent sends go through normally
						return
					}
				}
			}
			placeholderID = ""
		}
		if err := m.Send(msg.Platform, msg.WorkspaceID, msg.PlatformUserID, text); err != nil {
			fmt.Printf("gateway: send error: %v\n", err)
		}
	}
	// deleteIncoming silently removes the user's incoming message from chat.
	// Used to redact typed master passwords from the visible chat history.
	deleteIncoming := func() {
		if msg.MessageID == "" {
			return
		}
		m.mu.RLock()
		gw, ok := m.gateways[key(msg.Platform, msg.WorkspaceID)]
		m.mu.RUnlock()
		if ok {
			if dg, ok := gw.(DeletableGateway); ok {
				_ = dg.DeleteMessage(msg.PlatformUserID, msg.MessageID)
			}
		}
	}

	// sendAutoDelete sends text, captures the sent message ID, and schedules
	// deletion after 30 s followed by a notification to the user.
	// Used for messages containing sensitive values (e.g. revealed secrets).
	sendAutoDelete := func(text string) {
		var sentMsgID string

		m.mu.RLock()
		gw, ok := m.gateways[key(msg.Platform, msg.WorkspaceID)]
		m.mu.RUnlock()

		if ok {
			if tg, ok2 := gw.(TypingGateway); ok2 {
				if placeholderID != "" {
					if err := tg.EditMessage(msg.PlatformUserID, placeholderID, text); err == nil {
						sentMsgID = placeholderID
					}
					placeholderID = ""
				} else {
					sentMsgID, _ = tg.SendMessageGetID(msg.PlatformUserID, text)
				}
			}
		}
		if sentMsgID == "" {
			_ = m.Send(msg.Platform, msg.WorkspaceID, msg.PlatformUserID, text)
			return
		}

		go func(id string) {
			time.Sleep(30 * time.Second)
			m.mu.RLock()
			gw2, ok2 := m.gateways[key(msg.Platform, msg.WorkspaceID)]
			m.mu.RUnlock()
			if ok2 {
				if dg, ok3 := gw2.(DeletableGateway); ok3 {
					_ = dg.DeleteMessage(msg.PlatformUserID, id)
				}
			}
			_ = m.Send(msg.Platform, msg.WorkspaceID, msg.PlatformUserID, "🔐 Secret message was automatically deleted.")
		}(sentMsgID)
	}

	if err := m.router.Handle(ctx, msg, send, deleteIncoming, sendAutoDelete, updatePlaceholder); err != nil {
		send("An error occurred: " + err.Error())
	}
}

func key(platform, workspaceID string) string {
	return platform + ":" + workspaceID
}

// ─── Token encryption (system key AES-256-GCM) ────────────────────────────────

// EncryptToken encrypts a bot token with the system key.
// Returns base64(nonce || ciphertext).
func EncryptToken(token string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes")
	}
	block, err := aes.NewCipher(systemKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(token), nil)
	combined := append(nonce, ct...)
	return base64.StdEncoding.EncodeToString(combined), nil
}

// DecryptToken decrypts a bot token encrypted by EncryptToken.
func DecryptToken(encrypted string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes")
	}
	// Support plaintext tokens stored during Phase 1 (prefixed with __plain__).
	if strings.HasPrefix(encrypted, "__plain__") {
		return strings.TrimPrefix(encrypted, "__plain__"), nil
	}
	combined, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	block, err := aes.NewCipher(systemKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(combined) < gcm.NonceSize() {
		return "", fmt.Errorf("token ciphertext too short")
	}
	nonce := combined[:gcm.NonceSize()]
	ct := combined[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return string(plain), nil
}
