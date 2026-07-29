package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// emptyPayloadSHA256 is SHA-256 of the empty string, the payload hash for any
// request with no body.
const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// signV4 signs req in place with AWS Signature Version 4.
//
// Hand-rolled against stdlib HMAC/SHA-256 rather than pulling in aws-sdk-go-v2:
// four verbs against one bucket do not justify that dependency tree in a
// project that deliberately keeps dependencies few.
//
// payloadSHA256 must be the hex SHA-256 of the request body. Callers with a
// body on disk hash the file first — S3 requires the hash up front, which is
// one more reason the engine stages the snapshot to a temp file.
func signV4(req *http.Request, accessKey, secretKey, region, service, payloadSHA256 string, now time.Time) error {
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("backup: S3 credentials are not configured")
	}
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadSHA256)

	// Canonical headers: host plus the two x-amz headers, lowercase, sorted.
	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadSHA256,
		"x-amz-date":           amzDate,
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headers[k]))
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Query values must be sorted and RFC3986-encoded.
	canonicalQuery := req.URL.Query().Encode()

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
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

	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature))
	return nil
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
