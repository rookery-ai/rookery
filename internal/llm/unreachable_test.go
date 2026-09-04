package llm

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"
)

// errTransport fails every request with a fixed error and counts the attempts.
// A RoundTripper rather than a real dead port: the point of these tests is which
// errors doJSON classifies as terminal, and only a synthetic transport lets a
// test name the exact error and count the attempts deterministically.
type errTransport struct {
	err   error
	calls int
}

func (t *errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, t.err
}

func clientFailingWith(err error) (*http.Client, *errTransport) {
	rt := &errTransport{err: err}
	return &http.Client{Transport: rt}, rt
}

// The three terminal transport failures. Each must return ErrUnreachable after
// exactly ONE attempt: nothing about a refused dial, a hostname that does not
// exist, or a certificate that does not verify gets better after a 30-second
// backoff, and the retry ladder used to spend ~68 seconds proving that before
// handing back an unclassified error.
func TestTerminalTransportFailuresAreNotRetried(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"connection refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}},
		{"host does not exist", &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}},
		{"certificate does not verify", x509.UnknownAuthorityError{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, rt := clientFailingWith(tc.err)
			start := time.Now()
			_, _, err := doJSON(context.Background(), client, http.MethodPost, "http://127.0.0.1:1/v1", nil, []byte(`{}`))
			if !errors.Is(err, ErrUnreachable) {
				t.Fatalf("err = %v, want ErrUnreachable", err)
			}
			if rt.calls != 1 {
				t.Errorf("attempts = %d, want 1 (a terminal failure must not be retried)", rt.calls)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("took %s, want well under a second (no backoff should have been slept)", elapsed)
			}
		})
	}
}

// The classification fails OPEN toward retrying: an error we do not recognise
// keeps today's ladder. A misclassification therefore costs latency and never a
// lost turn. Uses a short deadline rather than waiting out the full ~68s budget
// — the property under test is "it tried more than once", not the exact count.
func TestAnUnrecognisedNetworkErrorStillRetries(t *testing.T) {
	client, rt := clientFailingWith(errors.New("some transport hiccup nobody classified"))
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	_, _, err := doJSON(ctx, client, http.MethodPost, "http://127.0.0.1:1/v1", nil, []byte(`{}`))
	if errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want an unrecognised error to stay retryable, not ErrUnreachable", err)
	}
	if rt.calls < 2 {
		t.Errorf("attempts = %d, want at least 2 (unrecognised errors must still retry)", rt.calls)
	}
}
