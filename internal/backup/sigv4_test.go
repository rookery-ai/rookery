package backup

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The canonical shape from AWS's SigV4 test suite (get-vanilla): a GET to
// example.amazonaws.com with a fixed date and credentials. Pinning the
// credential scope and signed-header set means a refactor cannot silently
// break signing in a way only a live bucket would reveal.
func TestSignV4CredentialScopeAndHeaders(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"
	when := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	err = signV4(req, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		"us-east-1", "service", emptyPayloadSHA256, when)
	if err != nil {
		t.Fatalf("signV4: %v", err)
	}

	got := req.Header.Get("Authorization")
	const want = "AWS4-HMAC-SHA256 " +
		"Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date, " +
		"Signature="
	if !strings.HasPrefix(got, want) {
		t.Fatalf("got %q,\nwant prefix %q", got, want)
	}
	if req.Header.Get("X-Amz-Date") != "20150830T123600Z" {
		t.Fatalf("X-Amz-Date = %q", req.Header.Get("X-Amz-Date"))
	}
	if req.Header.Get("X-Amz-Content-Sha256") != emptyPayloadSHA256 {
		t.Fatal("payload hash header not set")
	}
}

func TestSignV4IsDeterministic(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	sig := func() string {
		req, _ := http.NewRequest("PUT", "https://b.s3.amazonaws.com/sa/x.rkb", nil)
		signV4(req, "AK", "SK", "us-east-1", "s3", emptyPayloadSHA256, when)
		return req.Header.Get("Authorization")
	}
	if sig() != sig() {
		t.Fatal("signing must be deterministic for identical inputs")
	}
}

func TestSignV4DiffersByPayload(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	reqA, _ := http.NewRequest("PUT", "https://b.s3.amazonaws.com/x", nil)
	reqB, _ := http.NewRequest("PUT", "https://b.s3.amazonaws.com/x", nil)
	signV4(reqA, "AK", "SK", "us-east-1", "s3", emptyPayloadSHA256, when)
	signV4(reqB, "AK", "SK", "us-east-1", "s3", strings.Repeat("a", 64), when)
	if reqA.Header.Get("Authorization") == reqB.Header.Get("Authorization") {
		t.Fatal("a different payload hash must produce a different signature")
	}
}

func TestSignV4DiffersBySecret(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	reqA, _ := http.NewRequest("GET", "https://b.s3.amazonaws.com/x", nil)
	reqB, _ := http.NewRequest("GET", "https://b.s3.amazonaws.com/x", nil)
	signV4(reqA, "AK", "SK1", "us-east-1", "s3", emptyPayloadSHA256, when)
	signV4(reqB, "AK", "SK2", "us-east-1", "s3", emptyPayloadSHA256, when)
	if reqA.Header.Get("Authorization") == reqB.Header.Get("Authorization") {
		t.Fatal("a different secret key must produce a different signature")
	}
}

func TestSignV4EncodesQuery(t *testing.T) {
	when := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	req, _ := http.NewRequest("GET", "https://b.s3.amazonaws.com/?list-type=2&prefix=sa%2F", nil)
	if err := signV4(req, "AK", "SK", "us-east-1", "s3", emptyPayloadSHA256, when); err != nil {
		t.Fatalf("signV4: %v", err)
	}
	if req.Header.Get("Authorization") == "" {
		t.Fatal("query requests must still be signed")
	}
}

func TestSignV4RequiresCredentials(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://b.s3.amazonaws.com/x", nil)
	if err := signV4(req, "", "", "us-east-1", "s3", emptyPayloadSHA256, time.Now()); err == nil {
		t.Fatal("missing credentials must fail loudly rather than send an unsigned request")
	}
}
