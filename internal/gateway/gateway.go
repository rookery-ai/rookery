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
	"strings"
	"sync"

	"github.com/ilijad1/simple-agents/internal/db"
)

// Message is a normalised inbound message from any platform.
type Message struct {
	Platform       string // "telegram"
	PlatformUserID string // platform-specific user/chat ID
	UserID         string // resolved internal user ID (empty if not yet linked)
	Text           string
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

// GatewayManager manages one Gateway per active platform_connection.
// It is safe for concurrent use.
type GatewayManager struct {
	db        *db.DB
	systemKey []byte
	router    *Router

	mu       sync.RWMutex
	gateways map[string]Gateway        // key: "platform:userID"
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
			fmt.Printf("gateway: failed to start %s for user %s: %v\n", conn.Platform, conn.UserID, err)
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
func (m *GatewayManager) Reload(ctx context.Context, userID, platform string) error {
	// Stop existing if running.
	m.stop(userID, platform)

	conn, err := m.db.GetPlatformConnection(userID, platform)
	if err != nil {
		return nil // connection was deleted — nothing to start
	}
	if !conn.Active {
		return nil
	}
	return m.start(ctx, conn)
}

// Send delivers a message to a platform user on behalf of a user's bot.
func (m *GatewayManager) Send(platform, userID, platformUserID, text string) error {
	m.mu.RLock()
	gw, ok := m.gateways[key(platform, userID)]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no active %s gateway for user %s", platform, userID)
	}
	return gw.Send(platformUserID, text)
}

// SendToUser looks up the user's linked platform identity and sends a message.
// It tries all known platforms in order; the first successful send wins.
// Satisfies the reminder.Sender interface.
func (m *GatewayManager) SendToUser(userID, text string) error {
	// Find all platform identities for this user.
	identities, err := m.db.ListPlatformIdentities(userID, "")
	if err != nil || len(identities) == 0 {
		return fmt.Errorf("no platform identity for user %s", userID)
	}
	for _, identity := range identities {
		if err := m.Send(identity.Platform, userID, identity.PlatformUserID, text); err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to deliver message to user %s on any platform", userID)
}

// ─── Internal ─────────────────────────────────────────────────────────────────

func (m *GatewayManager) start(ctx context.Context, conn *db.PlatformConnection) error {
	token, err := DecryptToken(conn.EncryptedToken, m.systemKey)
	if err != nil {
		return fmt.Errorf("decrypt token: %w", err)
	}

	var gw Gateway
	switch conn.Platform {
	case "telegram":
		gw, err = NewTelegram(token, conn.UserID, m)
		if err != nil {
			return fmt.Errorf("new telegram: %w", err)
		}
	default:
		return fmt.Errorf("unsupported platform: %s", conn.Platform)
	}

	gwCtx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	k := key(conn.Platform, conn.UserID)
	m.gateways[k] = gw
	m.cancels[k] = cancel
	m.mu.Unlock()

	go func() {
		if err := gw.Start(gwCtx); err != nil && gwCtx.Err() == nil {
			fmt.Printf("gateway: %s for user %s stopped with error: %v\n", conn.Platform, conn.UserID, err)
		}
	}()
	return nil
}

func (m *GatewayManager) stop(userID, platform string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(platform, userID)
	if cancel, ok := m.cancels[k]; ok {
		cancel()
		delete(m.cancels, k)
	}
	if gw, ok := m.gateways[k]; ok {
		_ = gw.Stop()
		delete(m.gateways, k)
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
			_ = m.Send(msg.Platform, msg.UserID, msg.PlatformUserID,
				"This is a private bot. Send /start to link your account.")
			return
		}
		// For /start, keep msg.UserID = ownerUserID so the router can create the identity.
	} else {
		msg.UserID = identity.UserID
	}

	send := func(text string) {
		if err := m.Send(msg.Platform, msg.UserID, msg.PlatformUserID, text); err != nil {
			fmt.Printf("gateway: send error: %v\n", err)
		}
	}
	if err := m.router.Handle(ctx, msg, send); err != nil {
		send("An error occurred: " + err.Error())
	}
}

func key(platform, userID string) string {
	return platform + ":" + userID
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
