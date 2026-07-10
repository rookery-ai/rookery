package secrets

import (
	"crypto/rand"
	"testing"
)

func TestSystemKeyRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := EncryptWithSystemKey("hello token", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == "hello token" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	got, err := DecryptWithSystemKey(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != "hello token" {
		t.Fatalf("got %q, want %q", got, "hello token")
	}
}

func TestSystemKeyWrongKeyFails(t *testing.T) {
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	rand.Read(k1)
	rand.Read(k2)
	enc, _ := EncryptWithSystemKey("secret", k1)
	if _, err := DecryptWithSystemKey(enc, k2); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestSystemKeyBadLength(t *testing.T) {
	if _, err := EncryptWithSystemKey("x", make([]byte, 16)); err == nil {
		t.Fatal("must reject non-32-byte key")
	}
}
