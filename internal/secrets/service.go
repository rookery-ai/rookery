// Package secrets provides per-user AES-256-GCM encrypted secret storage.
// Key derivation uses Argon2id with a per-user salt stored in the users table.
//
// SECURITY INVARIANT: Proxy() resolves ${NAME} placeholders in-memory only.
// Resolved values are NEVER written to disk, logs, or the DB.
// Coder.Generate() must NEVER call Proxy() — only AgentRunner.Run() may do so.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"golang.org/x/crypto/argon2"
)

var ErrNotFound = db.ErrNotFound
var ErrWrongPassword = errors.New("wrong master password")

// argon2id parameters — tuned for interactive login latency (~100ms on modern hardware).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32 // 256-bit AES key
)

// placeholderRe matches ${SECRET_NAME} in agent code.
var placeholderRe = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// Service manages per-user encrypted secrets.
type Service struct {
	db          *db.DB
	workspaceID string
	masterPw    string // plaintext master password (held in-memory only during operation)
	salt        string // hex-encoded per-user Argon2id salt from users table
	systemKey   []byte // 32-byte system key for encrypting master password at rest
}

// New creates a Service for a user with their plaintext master password.
// The master password is used to derive the AES key for secret encryption.
func New(database *db.DB, workspaceID, masterPw, salt string) *Service {
	return &Service{
		db:          database,
		workspaceID: workspaceID,
		masterPw:    masterPw,
		salt:        salt,
	}
}

// WithSystemKey sets the system-wide key used to encrypt master passwords.
// Call this before EncryptMasterPassword / DecryptMasterPassword.
func (s *Service) WithSystemKey(key []byte) *Service {
	s.systemKey = key
	return s
}

// Set encrypts and persists a named secret for the user.
func (s *Service) Set(ctx context.Context, name, value string) error {
	key, err := s.deriveKey()
	if err != nil {
		return err
	}

	ciphertext, nonce, err := aesGCMEncrypt(key, []byte(value))
	if err != nil {
		return err
	}

	return s.db.UpsertSecret(&db.Secret{
		ID:          uuid.New().String(),
		WorkspaceID: s.workspaceID,
		Name:        name,
		Ciphertext:  base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:       base64.StdEncoding.EncodeToString(nonce),
	})
}

// Get decrypts and returns a named secret. Returns ErrNotFound if absent,
// ErrWrongPassword if the master password is incorrect.
func (s *Service) Get(ctx context.Context, name string) (string, error) {
	row, err := s.db.GetSecret(s.workspaceID, name)
	if err != nil {
		return "", err
	}

	key, err := s.deriveKey()
	if err != nil {
		return "", err
	}

	ciphertext, err := base64.StdEncoding.DecodeString(row.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(row.Nonce)
	if err != nil {
		return "", fmt.Errorf("decode nonce: %w", err)
	}

	plaintext, err := aesGCMDecrypt(key, ciphertext, nonce)
	if err != nil {
		return "", ErrWrongPassword
	}
	return string(plaintext), nil
}

// List returns all secret names for the user. Values are never returned.
func (s *Service) List(ctx context.Context) ([]string, error) {
	return s.db.ListSecretNames(s.workspaceID)
}

// Delete removes a named secret. Returns ErrNotFound if absent.
func (s *Service) Delete(ctx context.Context, name string) error {
	return s.db.DeleteSecret(s.workspaceID, name)
}

// Proxy resolves ${NAME} placeholders in text using in-memory decrypted secret values.
// The returned string MUST NOT be logged, stored, or sent to any external service.
// Only AgentRunner.Run() should call this function.
func (s *Service) Proxy(ctx context.Context, text string) (string, error) {
	// Collect all unique placeholder names first to avoid redundant DB calls.
	names := make(map[string]struct{})
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		names[m[1]] = struct{}{}
	}
	if len(names) == 0 {
		return text, nil
	}

	// Resolve each name exactly once.
	resolved := make(map[string]string, len(names))
	for name := range names {
		val, err := s.Get(ctx, name)
		if errors.Is(err, ErrNotFound) {
			// Leave unresolvable placeholders as-is rather than failing the whole run.
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolve secret %s: %w", name, err)
		}
		resolved[name] = val
	}

	// Replace placeholders — strings.ReplaceAll in a loop to avoid regex overhead.
	result := text
	for name, val := range resolved {
		result = strings.ReplaceAll(result, "${"+name+"}", val)
	}
	return result, nil
}

// GetAll decrypts and returns every secret for this user as a name→value map.
// The returned map MUST NOT be logged, stored, or sent to any LLM. Only pass to subprocess env.
func (s *Service) GetAll(ctx context.Context) (map[string]string, error) {
	names, err := s.db.ListSecretNames(s.workspaceID)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return map[string]string{}, nil
	}
	key, err := s.deriveKey()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		row, err := s.db.GetSecret(s.workspaceID, name)
		if err != nil {
			continue
		}
		ct, err := base64.StdEncoding.DecodeString(row.Ciphertext)
		if err != nil {
			continue
		}
		n, err := base64.StdEncoding.DecodeString(row.Nonce)
		if err != nil {
			continue
		}
		pt, err := aesGCMDecrypt(key, ct, n)
		if err != nil {
			continue
		}
		out[name] = strings.TrimSpace(string(pt))
	}
	return out, nil
}

// EncryptMasterPassword encrypts the user's master password using the system key
// so it can be stored at rest and used for cron-triggered agent runs.
// Returns a base64-encoded "nonce||ciphertext" string.
func EncryptMasterPassword(masterPw string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes, got %d", len(systemKey))
	}
	ciphertext, nonce, err := aesGCMEncrypt(systemKey, []byte(masterPw))
	if err != nil {
		return "", err
	}
	combined := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(combined), nil
}

// DecryptMasterPassword decrypts the stored encrypted master password using the system key.
func DecryptMasterPassword(encrypted string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes, got %d", len(systemKey))
	}
	combined, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decode encrypted master pw: %w", err)
	}
	if len(combined) < 12 {
		return "", fmt.Errorf("encrypted master pw too short")
	}
	nonce := combined[:12]
	ciphertext := combined[12:]
	plaintext, err := aesGCMDecrypt(systemKey, ciphertext, nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt master pw: %w", err)
	}
	return string(plaintext), nil
}

// SystemKeyFromEnv reads the system key from ROOKERY_SYSTEM_KEY env var (hex-encoded 32 bytes).
// If not set, derives a fallback key from the hostname (DEV ONLY — not safe for production).
func SystemKeyFromEnv() ([]byte, error) {
	hex64 := os.Getenv("ROOKERY_SYSTEM_KEY")
	if hex64 != "" {
		key, err := hex.DecodeString(hex64)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("ROOKERY_SYSTEM_KEY must be 64 hex chars (32 bytes), got %d chars", len(hex64))
		}
		return key, nil
	}
	// Fallback: derive from hostname — only acceptable in development.
	host, _ := os.Hostname()
	key := argon2.IDKey([]byte(host), []byte("simple-agents-dev-key"), 1, 64*1024, 4, 32)
	return key, nil
}

// SystemKeyPath is where the pinned 32-byte system key lives.
func SystemKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "system.key")
}

// SystemKey resolves the system key, pinning it to disk so it survives a
// hostname change. Resolution order:
//
//  1. ROOKERY_SYSTEM_KEY, if set — still wins, and is never written to disk.
//  2. <dataDir>/system.key, if present.
//  3. Derive and persist. When hasWorkspaces is true the install already holds
//     data encrypted under the legacy hostname-derived key, so that exact key is
//     reproduced and written out — an upgrade must never change it. A fresh
//     install instead gets 32 random bytes, which is strictly stronger than a
//     guessable hostname.
//
// Restore writes the recovered key to this same path, which is how connector
// tokens and stored master passwords survive a move to new hardware.
func SystemKey(dataDir string, hasWorkspaces bool) ([]byte, error) {
	if hex64 := os.Getenv("ROOKERY_SYSTEM_KEY"); hex64 != "" {
		key, err := hex.DecodeString(hex64)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("ROOKERY_SYSTEM_KEY must be 64 hex chars (32 bytes), got %d chars", len(hex64))
		}
		return key, nil
	}

	path := SystemKeyPath(dataDir)
	if raw, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s is corrupt: expected 64 hex chars (32 bytes)", path)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read system key: %w", err)
	}

	var key []byte
	if hasWorkspaces {
		host, _ := os.Hostname()
		key = argon2.IDKey([]byte(host), []byte("simple-agents-dev-key"), 1, 64*1024, 4, 32)
	} else {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate system key: %w", err)
		}
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("persist system key: %w", err)
	}
	return key, nil
}

// NewGenerateSalt creates a new random 16-byte hex-encoded salt.
func NewGenerateSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// deriveKey derives a 32-byte AES key from the master password and salt using Argon2id.
func (s *Service) deriveKey() ([]byte, error) {
	saltBytes, err := hex.DecodeString(s.salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(s.masterPw),
		saltBytes,
		argonTime,
		argonMemory,
		argonThreads,
		argonKeyLen,
	)
	return key, nil
}

// aesGCMEncrypt encrypts plaintext using AES-256-GCM with a random 12-byte nonce.
// Returns (ciphertext, nonce, error).
func aesGCMEncrypt(key, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("gen nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// aesGCMDecrypt decrypts ciphertext using AES-256-GCM with the given nonce.
func aesGCMDecrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}
