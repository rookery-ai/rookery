package awssig

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The credentials, region, service and timestamp AWS uses throughout its
// published SigV4 test suite (aws-sig-v4-test-suite).
var (
	vectorCred = Credentials{
		AccessKey: "AKIDEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	vectorTime = time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
)

// TestPublishedVectors checks the signer against AWS's own published test
// suite. This is the only INDEPENDENT check available offline — every other
// test here would pass equally well against a subtly wrong implementation,
// because it would be comparing this code to itself.
func TestPublishedVectors(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		url     string
		headers map[string]string
		want    string
	}{
		{
			name:   "get-vanilla",
			method: "GET",
			url:    "https://example.amazonaws.com/",
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
				"SignedHeaders=host;x-amz-date, " +
				"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31",
		},
		{
			name:   "get-vanilla-query-order-key-case",
			method: "GET",
			url:    "https://example.amazonaws.com/?Param2=value2&Param1=value1",
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
				"SignedHeaders=host;x-amz-date, " +
				"Signature=b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500",
		},
		{
			// Unreserved characters must survive the path UNESCAPED. Go's
			// url.EscapedPath already does the right thing here; the value of
			// the case is that it would catch a "helpful" re-escape.
			name:   "get-unreserved",
			method: "GET",
			url: "https://example.amazonaws.com/-._~0123456789" +
				"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
				"SignedHeaders=host;x-amz-date, " +
				"Signature=07ef7494c76fa4850883e2b006601f940f8a34d404d0cfa977f52a65bbf5f24f",
		},
		{
			// A multibyte query key — the case that proves rfc3986Escape encodes
			// per BYTE, which is what SigV4 requires. A rune-wise implementation
			// passes every ASCII case above and fails only here.
			name:   "get-vanilla-utf8-query",
			method: "GET",
			url:    "https://example.amazonaws.com/?ሴ=bar",
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
				"SignedHeaders=host;x-amz-date, " +
				"Signature=2cdec8eed098649ff3a119c94853b13c643bcf08f8b0a1d91e12c9027818dd04",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, tc.url, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if err := Sign(req, vectorCred, "us-east-1", "service", EmptyPayloadSHA256, vectorTime); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if got := req.Header.Get("Authorization"); got != tc.want {
				t.Errorf("Authorization mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// DEFECT 1. url.Values.Encode escapes a space as "+", SigV4 requires "%20".
// The backup-local original used Encode() and never hit it, because its only
// query values are snapshot names and a bucket prefix. A connector filter will.
func TestCanonicalQueryUsesPercent20NotPlus(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/?q=two+words", nil)
	// Round-trips through url.Values as the signer does; the decoded value is
	// "two words" either way, so this is purely about the ENCODING chosen.
	got := canonicalQuery(req.URL.Query())
	if strings.Contains(got, "+") {
		t.Fatalf("canonical query %q encodes a space as '+', want %%20", got)
	}
	if got != "q=two%20words" {
		t.Fatalf("canonical query = %q, want q=two%%20words", got)
	}
}

func TestRFC3986EscapeLeavesUnreservedAlone(t *testing.T) {
	const unreserved = "AZaz09-_.~"
	if got := rfc3986Escape(unreserved); got != unreserved {
		t.Fatalf("escaped unreserved characters: %q", got)
	}
	if got := rfc3986Escape("a/b c"); got != "a%2Fb%20c" {
		t.Fatalf("rfc3986Escape(%q) = %q, want a%%2Fb%%20c", "a/b c", got)
	}
}

// DEFECT 2. The original signed a fixed three-header set, so a service that
// requires an x-amz-* header (X-Amz-Target on the JSON-RPC APIs) or a body
// content type could not be signed correctly at all.
func TestSignedHeadersIncludeContentTypeAndAmzHeaders(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://example.amazonaws.com/", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
	req.Header.Set("Accept", "application/json") // NOT signed: not host/content-type/x-amz-*

	if err := Sign(req, vectorCred, "us-east-1", "dynamodb", EmptyPayloadSHA256, vectorTime); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	auth := req.Header.Get("Authorization")
	for _, want := range []string{"content-type", "x-amz-target", "x-amz-date", "host"} {
		if !strings.Contains(auth, want) {
			t.Errorf("SignedHeaders is missing %q: %s", want, auth)
		}
	}
	if strings.Contains(auth, "accept") {
		t.Errorf("SignedHeaders should not carry unrelated headers: %s", auth)
	}
}

// S3 needs X-Amz-Content-Sha256; other services must not have it forced on,
// or the published vectors above could not be matched.
func TestContentSHA256HeaderIsS3Only(t *testing.T) {
	s3req, _ := http.NewRequest("GET", "https://bucket.s3.amazonaws.com/key", nil)
	if err := Sign(s3req, vectorCred, "us-east-1", "s3", EmptyPayloadSHA256, vectorTime); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if s3req.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Error("s3 request is missing X-Amz-Content-Sha256")
	}
	if !strings.Contains(s3req.Header.Get("Authorization"), "x-amz-content-sha256") {
		t.Error("s3 SignedHeaders must cover x-amz-content-sha256")
	}

	other, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	if err := Sign(other, vectorCred, "us-east-1", "service", EmptyPayloadSHA256, vectorTime); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if other.Header.Get("X-Amz-Content-Sha256") != "" {
		t.Error("non-s3 request should not have X-Amz-Content-Sha256 forced on")
	}
}

func TestMissingCredentialsAndScopeAreRejected(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	if err := Sign(req, Credentials{}, "us-east-1", "s3", EmptyPayloadSHA256, vectorTime); err == nil {
		t.Error("want an error with no credentials")
	}
	if err := Sign(req, vectorCred, "", "s3", EmptyPayloadSHA256, vectorTime); err == nil {
		t.Error("want an error with no region")
	}
	if err := Sign(req, vectorCred, "us-east-1", "", EmptyPayloadSHA256, vectorTime); err == nil {
		t.Error("want an error with no service")
	}
}
