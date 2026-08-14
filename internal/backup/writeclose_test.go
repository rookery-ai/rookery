package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/iotest"
)

// failOnCloseWriter accepts every write and fails only on Close, which is how a
// buffered or network-backed filesystem reports a write that never actually
// reached stable storage.
type failOnCloseWriter struct {
	bytes.Buffer
	err error
}

func (w *failOnCloseWriter) Close() error { return w.err }

// A restored file whose Close fails was not fully written — but the SHA-256 is
// computed from the STREAM, never read back off disk, so without this check a
// truncated extract still matches its manifest entry and the restore reports
// success.
func TestWriteHashCloseReportsACloseFailure(t *testing.T) {
	w := &failOnCloseWriter{err: os.ErrClosed}
	_, _, err := writeHashClose("snap/db/rookery.db", w, bytes.NewReader([]byte("payload")))
	if err == nil {
		t.Fatal("expected a close failure to be reported, got nil")
	}
	if !strings.Contains(err.Error(), "snap/db/rookery.db") {
		t.Fatalf("error should name the file, got %q", err)
	}
}

func TestWriteHashCloseHashesAndSizesTheStream(t *testing.T) {
	w := &failOnCloseWriter{}
	sum, n, err := writeHashClose("x", w, bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len("payload")) {
		t.Fatalf("size = %d, want %d", n, len("payload"))
	}
	want := sha256.Sum256([]byte("payload"))
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sum = %s, want %s", sum, hex.EncodeToString(want[:]))
	}
	if w.Buffer.String() != "payload" {
		t.Fatalf("payload not written through: %q", w.Buffer.String())
	}
}

// A write failure must still win over a close failure — reporting "close" for a
// disk that filled mid-copy would point triage at the wrong thing.
func TestWriteHashClosePrefersTheWriteError(t *testing.T) {
	w := &failOnCloseWriter{err: os.ErrClosed}
	_, _, err := writeHashClose("x", w, iotest.ErrReader(errors.New("disk full")))
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("want the write error, got %v", err)
	}
}
