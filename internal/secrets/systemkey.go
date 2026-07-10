package secrets

import (
	"encoding/base64"
	"fmt"
)

// EncryptWithSystemKey encrypts plaintext under the 32-byte system key and returns
// base64("nonce||ciphertext"). Same framing as EncryptMasterPassword, but general
// purpose — used for connector OAuth tokens and client secrets that a headless
// background/refresh loop must be able to decrypt without a master password.
func EncryptWithSystemKey(plaintext string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes, got %d", len(systemKey))
	}
	ct, nonce, err := aesGCMEncrypt(systemKey, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

// DecryptWithSystemKey reverses EncryptWithSystemKey.
func DecryptWithSystemKey(encoded string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes, got %d", len(systemKey))
	}
	combined, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(combined) < 12 {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := aesGCMDecrypt(systemKey, combined[12:], combined[:12])
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}
