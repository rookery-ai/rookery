package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionKeyPath is where the pinned 32-byte session key lives. It sits beside
// system.key, and for the same reason: a key that is regenerated on every start
// is not a key, it is a logout.
func SessionKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "session.key")
}

// SessionKey resolves the key that signs session cookies. Resolution order
// mirrors SystemKey exactly:
//
//  1. The configured value (ROOKERY_SESSION_KEY or config.yaml), if set — it
//     wins and is never written to disk.
//  2. <dataDir>/session.key, if present.
//  3. Generate 32 random bytes and persist them at 0600.
//
// This function exists because the fallback it replaces was the literal string
// "change-me-in-production-32bytes!!", compiled into a binary whose source is
// published. Any install that never set ROOKERY_SESSION_KEY signed its cookies
// with a key an attacker could read out of the repository and use to mint a
// session for the owner. Generating a random key per start would close that hole
// but sign everyone out on every restart, which is why the key is pinned to disk
// rather than merely randomised.
//
// A configured value is accepted in two forms. 64 hex characters decode to the
// documented 32-byte key; anything else is taken as raw bytes, which is what the
// server did with this value historically. Rejecting the raw form would log out
// every operator who had already set the variable and read the old behaviour off
// the code rather than the docs.
func SessionKey(dataDir, configured string) ([]byte, error) {
	if key := parseSessionKey(configured); len(key) > 0 {
		return key, nil
	}

	// With nowhere to pin it, generate an ephemeral key rather than falling back
	// to a shared constant. Sessions do not survive a restart, which is the right
	// trade for a configuration that named no data directory (tests, mostly).
	if strings.TrimSpace(dataDir) == "" {
		return randomKey()
	}

	path := SessionKeyPath(dataDir)
	if raw, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s is corrupt: expected 64 hex chars (32 bytes)", path)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read session key: %w", err)
	}

	key, err := randomKey()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("persist session key: %w", err)
	}
	return key, nil
}

func randomKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	return key, nil
}

// parseSessionKey returns the bytes a configured session key stands for, or nil
// when nothing was configured.
func parseSessionKey(configured string) []byte {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return nil
	}
	if len(configured) == 64 {
		if key, err := hex.DecodeString(configured); err == nil {
			return key
		}
	}
	return []byte(configured)
}
