package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestS3PutSignsAndUploads(t *testing.T) {
	var gotPath, gotAuth, gotLen string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotLen = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Length")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "mybucket",
		Prefix: "sa/", AccessKey: "AK", PathStyle: true,
	}, "SK")

	body := []byte("encrypted snapshot")
	name := "simple-agents-20260729-030000.sab"
	if err := d.Put(context.Background(), name, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if gotPath != "/mybucket/sa/"+name {
		t.Fatalf("path = %q, want path-style /mybucket/sa/<name>", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AK/") {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotLen != fmt.Sprint(len(body)) {
		t.Fatalf("Content-Length = %q, want %d", gotLen, len(body))
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatal("uploaded body does not match")
	}
}

func TestS3PutRejectsNonSeekableSource(t *testing.T) {
	d := NewS3Destination(S3Config{Endpoint: "http://unused", Region: "r", Bucket: "b", AccessKey: "AK"}, "SK")
	// A pipe is not seekable, and S3 needs the payload hash before sending.
	pr, pw := io.Pipe()
	pw.Close()
	if err := d.Put(context.Background(), "simple-agents-20260729-030000.sab", pr, 1); err == nil {
		t.Fatal("a non-seekable source must be refused, not silently mis-signed")
	}
}

func TestS3VirtualHostStyleURL(t *testing.T) {
	var gotPath, gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.Path, r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "mybucket",
		AccessKey: "AK", PathStyle: false,
	}, "SK")
	// Virtual-host style puts the bucket in the hostname, which cannot resolve
	// against an httptest server bound to an IP. Dial the test server whatever
	// the hostname says, so the URL construction itself is what gets asserted.
	d.client.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(srv.URL, "http://"))
		},
	}
	if err := d.Put(context.Background(), "simple-agents-20260729-030000.sab", strings.NewReader("x"), 1); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if gotPath != "/simple-agents-20260729-030000.sab" {
		t.Fatalf("virtual-host style must omit the bucket from the path, got %q", gotPath)
	}
	if !strings.HasPrefix(gotHost, "mybucket.") {
		t.Fatalf("host = %q, want the bucket as a subdomain", gotHost)
	}
}

func TestS3ListParsesAndFilters(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <Contents><Key>sa/simple-agents-20260728-030000.sab</Key><Size>120</Size><LastModified>2026-07-28T03:00:00.000Z</LastModified></Contents>
  <Contents><Key>sa/simple-agents-20260729-030000.sab</Key><Size>130</Size><LastModified>2026-07-29T03:00:00.000Z</LastModified></Contents>
  <Contents><Key>sa/notes.txt</Key><Size>5</Size><LastModified>2026-07-29T03:00:00.000Z</LastModified></Contents>
</ListBucketResult>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") != "2" {
			t.Errorf("expected a ListObjectsV2 request, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, body)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", Prefix: "sa/",
		AccessKey: "AK", PathStyle: true,
	}, "SK")

	entries, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 — foreign keys must be filtered out: %+v", len(entries), entries)
	}
	if entries[0].Name != "simple-agents-20260728-030000.sab" {
		t.Fatalf("name = %q, want the prefix stripped", entries[0].Name)
	}
	if entries[0].Size != 120 {
		t.Fatalf("size = %d, want 120", entries[0].Size)
	}
}

func TestS3GetReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "snapshot-bytes")
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "AK", PathStyle: true}, "SK")
	rc, err := d.Get(context.Background(), "simple-agents-20260729-030000.sab")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "snapshot-bytes" {
		t.Fatalf("got %q", got)
	}
}

func TestS3ErrorsCarryStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
	}))
	defer srv.Close()

	d := NewS3Destination(S3Config{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "AK", PathStyle: true}, "SK")
	err := d.Put(context.Background(), "simple-agents-20260729-030000.sab", strings.NewReader("x"), 1)
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("error must carry the status and body for triage, got %q", err)
	}
}

func TestS3DeleteRefusesForeignNames(t *testing.T) {
	d := NewS3Destination(S3Config{Endpoint: "http://unused", Region: "us-east-1", Bucket: "b", AccessKey: "AK"}, "SK")
	if err := d.Delete(context.Background(), "important-tax-return.pdf"); err == nil {
		t.Fatal("delete must refuse a name that is not a snapshot")
	}
}
