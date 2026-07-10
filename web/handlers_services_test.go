package web

import (
	"testing"
	"time"
)

func TestStateSignVerify(t *testing.T) {
	secret := []byte("system-key-or-any-secret-32bytes")
	payload := "ws1~google~work~nonce"
	tok := signState(secret, payload, time.Now())
	got, ok := verifyState(secret, tok, time.Now())
	if !ok || got != payload {
		t.Fatalf("verify: %q %v", got, ok)
	}
	if _, ok := verifyState(secret, tok, time.Now().Add(11*time.Minute)); ok {
		t.Fatal("expired state must fail")
	}
	if _, ok := verifyState(secret, tok+"x", time.Now()); ok {
		t.Fatal("tampered state must fail")
	}
}
