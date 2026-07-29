package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func roundTrip(t *testing.T, plaintext []byte, pass string) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := Encrypt(&enc, bytes.NewReader(plaintext), pass); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return enc.Bytes()
}

func TestCryptoRoundTrip(t *testing.T) {
	// Larger than one 1 MiB frame, so framing itself is exercised.
	plaintext := make([]byte, 3*chunkSize+1234)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	sealed := roundTrip(t, plaintext, "correct horse")

	if bytes.Contains(sealed, plaintext[:64]) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	if string(sealed[:8]) != magic {
		t.Fatalf("magic = %q, want %q", sealed[:8], magic)
	}

	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "correct horse"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(out.Bytes(), plaintext) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", out.Len(), len(plaintext))
	}
}

// An exact multiple of chunkSize is the boundary case: the last full read
// returns no error, so the terminator must come from a following empty frame.
func TestCryptoExactChunkMultiple(t *testing.T) {
	plaintext := make([]byte, 2*chunkSize)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	sealed := roundTrip(t, plaintext, "pw")

	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(out.Bytes(), plaintext) {
		t.Fatalf("got %d bytes, want %d", out.Len(), len(plaintext))
	}
}

func TestCryptoEmptyInput(t *testing.T) {
	sealed := roundTrip(t, nil, "pw")
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("got %d bytes, want 0", out.Len())
	}
}

func TestCryptoWrongPassphrase(t *testing.T) {
	sealed := roundTrip(t, []byte("hello"), "right")
	var out bytes.Buffer
	err := Decrypt(&out, bytes.NewReader(sealed), "wrong")
	if !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("got %v, want ErrBadPassphrase", err)
	}
}

func TestCryptoDetectsFlippedCiphertextByte(t *testing.T) {
	sealed := roundTrip(t, []byte("hello world"), "pw")
	sealed[len(sealed)-1] ^= 0xff
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err == nil {
		t.Fatal("expected failure on flipped ciphertext byte")
	}
}

// The header is authenticated as AAD, so tampering with the KDF parameters
// must be detected rather than silently honoured.
func TestCryptoDetectsFlippedHeaderByte(t *testing.T) {
	sealed := roundTrip(t, []byte("hello world"), "pw")
	sealed[10] ^= 0xff // inside the argon time/memory parameters
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err == nil {
		t.Fatal("expected failure on flipped header byte")
	}
}

// A snapshot cut short by a failed upload must not decrypt cleanly into a
// partial archive: the final-flag in the AAD makes truncation detectable.
func TestCryptoDetectsTruncation(t *testing.T) {
	plaintext := make([]byte, 2*chunkSize)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	sealed := roundTrip(t, plaintext, "pw")

	cut := sealed[:headerLen+4+nonceLen+chunkSize/2]
	var out bytes.Buffer
	err := Decrypt(&out, bytes.NewReader(cut), "pw")
	if err == nil {
		t.Fatal("expected failure on truncated stream")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

// Dropping only the final frame is the subtler truncation: every remaining
// frame authenticates, so only the missing terminator reveals the damage.
func TestCryptoDetectsMissingFinalFrame(t *testing.T) {
	plaintext := make([]byte, chunkSize+10)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	sealed := roundTrip(t, plaintext, "pw")

	// The first frame is a full chunk; keep exactly it and drop the rest.
	firstFrameEnd := headerLen + 4 + nonceLen + chunkSize + 16 // 16 = GCM tag
	var out bytes.Buffer
	err := Decrypt(&out, bytes.NewReader(sealed[:firstFrameEnd]), "pw")
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

func TestCryptoRejectsBadMagic(t *testing.T) {
	sealed := roundTrip(t, []byte("x"), "pw")
	copy(sealed[:8], "NOTABACK")
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}
