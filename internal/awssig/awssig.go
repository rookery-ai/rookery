// Package awssig signs HTTP requests with AWS Signature Version 4.
//
// Hand-rolled against stdlib HMAC/SHA-256 rather than pulling in aws-sdk-go-v2:
// this project deliberately keeps its dependency tree small, and the whole of
// SigV4 that we need is a few dozen lines of hashing.
//
// It lives in its own package because it has two unrelated consumers — the S3
// backup destination and the AWS connector — and neither should import the
// other. Extracting it also gave the two defects below somewhere to be tested,
// which is the real reason the move was worth doing: both were invisible to
// the backup code that carried the original.
package awssig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// EmptyPayloadSHA256 is SHA-256 of the empty string — the payload hash for any
// request with no body.
const EmptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Credentials is an AWS access key pair.
type Credentials struct {
	AccessKey string
	SecretKey string
}

// Sign signs req in place.
//
// payloadSHA256 must be the hex SHA-256 of the request body; use
// EmptyPayloadSHA256 when there is none. AWS requires the hash up front, before
// the body is sent, which is why callers with a body on disk hash the file
// first rather than streaming it.
func Sign(req *http.Request, cred Credentials, region, service, payloadSHA256 string, now time.Time) error {
	if cred.AccessKey == "" || cred.SecretKey == "" {
		return fmt.Errorf("awssig: access key and secret key are both required")
	}
	if region == "" || service == "" {
		return fmt.Errorf("awssig: region and service are both required")
	}
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	req.Header.Set("X-Amz-Date", amzDate)
	// X-Amz-Content-Sha256 is REQUIRED by S3 and unnecessary elsewhere. Setting
	// it unconditionally (as the backup-local original did) is harmless against
	// live AWS, but it changes SignedHeaders — which is why the published SigV4
	// test vectors, none of which use it, could not be checked against that
	// version. Gating it on the service keeps S3 correct and makes the vectors
	// matchable, which is the only independent check available offline.
	// A caller that sets the header itself keeps it either way.
	if service == "s3" {
		req.Header.Set("X-Amz-Content-Sha256", payloadSHA256)
	}

	canonicalHeaders, signedHeaders := canonicalizeHeaders(req, host)

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery(req.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadSHA256,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+cred.SecretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		cred.AccessKey, scope, signedHeaders, signature))
	return nil
}

// canonicalizeHeaders builds the canonical-headers block and the SignedHeaders
// list.
//
// The original signed a FIXED set — host, x-amz-content-sha256, x-amz-date —
// which is enough for the four S3 verbs the backup destination issues and wrong
// for anything else: AWS requires that any x-amz-* header a service demands
// (X-Amz-Target on the JSON-RPC style APIs, for one) also appear in
// SignedHeaders, and Content-Type must be signed when present.
func canonicalizeHeaders(req *http.Request, host string) (canonical, signed string) {
	headers := map[string]string{"host": host}
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if lower == "content-type" || strings.HasPrefix(lower, "x-amz-") {
			// Multiple values join with commas, per the canonical form.
			headers[lower] = strings.Join(values, ",")
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, k := range names {
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(strings.TrimSpace(headers[k]))
		b.WriteString("\n")
	}
	return b.String(), strings.Join(names, ";")
}

// canonicalQuery encodes the query string the way SigV4 requires.
//
// NOT url.Values.Encode(): that escapes a space as "+", while SigV4 demands
// RFC 3986 percent-encoding ("%20"). Any request whose query carries a space
// signs to something AWS rejects with SignatureDoesNotMatch. The backup code
// this was extracted from never hit it because its only query values are
// snapshot names and a bucket prefix, but an arbitrary connector filter will.
func canonicalQuery(q url.Values) string {
	type kv struct{ k, v string }
	var pairs []kv
	for key, values := range q {
		for _, v := range values {
			pairs = append(pairs, kv{rfc3986Escape(key), rfc3986Escape(v)})
		}
	}
	// Sort by encoded key, then encoded value — AWS specifies the ordering on
	// the ENCODED forms, not the raw ones.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p.k+"="+p.v)
	}
	return strings.Join(parts, "&")
}

// rfc3986Escape percent-encodes everything outside the unreserved set
// A-Z a-z 0-9 - _ . ~ — which is what SigV4 means by "URI-encode".
func rfc3986Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
