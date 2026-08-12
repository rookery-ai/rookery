package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/awssig"
)

// emptyPayloadSHA256 is re-exported from awssig for the read-only verbs below;
// the signer owns the constant.
const emptyPayloadSHA256 = awssig.EmptyPayloadSHA256

// S3Destination stores snapshots in any S3-compatible bucket: AWS S3,
// Backblaze B2, Cloudflare R2, MinIO, Wasabi. One implementation covers them
// all, which is why this was chosen over the OAuth providers as the first
// remote destination — static credentials, no app registration, no browser.
type S3Destination struct {
	cfg       S3Config
	secretKey string
	client    *http.Client
}

func NewS3Destination(cfg S3Config, secretKey string) *S3Destination {
	return &S3Destination{
		cfg:       cfg,
		secretKey: secretKey,
		client:    &http.Client{Timeout: 10 * time.Minute},
	}
}

func (d *S3Destination) Name() string {
	return "s3:" + d.cfg.Bucket + "/" + d.cfg.Prefix
}

// key renders the full object key for a snapshot name.
func (d *S3Destination) key(name string) string {
	return strings.TrimPrefix(d.cfg.Prefix, "/") + name
}

// endpointURL builds the request URL, honouring path-style vs virtual-host.
// MinIO and some R2 setups require path-style; AWS defaults to virtual-host.
func (d *S3Destination) endpointURL(objectKey string, query url.Values) (*url.URL, error) {
	base := d.cfg.Endpoint
	if base == "" {
		base = "https://s3." + d.cfg.Region + ".amazonaws.com"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("backup: bad S3 endpoint %q: %w", base, err)
	}
	if d.cfg.PathStyle {
		u.Path = "/" + d.cfg.Bucket
		if objectKey != "" {
			u.Path += "/" + objectKey
		}
	} else {
		u.Host = d.cfg.Bucket + "." + u.Host
		u.Path = "/"
		if objectKey != "" {
			u.Path = "/" + objectKey
		}
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u, nil
}

// do signs and performs a request, returning the response for 2xx and a
// descriptive error otherwise.
func (d *S3Destination) do(ctx context.Context, method string, u *url.URL, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("backup: build request: %w", err)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if err := awssig.Sign(req,
		awssig.Credentials{AccessKey: d.cfg.AccessKey, SecretKey: d.secretKey},
		d.cfg.Region, "s3", payloadHash, time.Now()); err != nil {
		return nil, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backup: %s %s: %w", method, u.Host, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Read a bounded slice of the error document: S3 error bodies name the
		// exact cause (AccessDenied, NoSuchBucket, SignatureDoesNotMatch) and
		// dropping them turns every misconfiguration into "it failed".
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("backup: S3 %s returned %d: %s", method, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp, nil
}

// Put uploads a snapshot. It requires an io.ReadSeeker so the payload can be
// hashed and then rewound — S3 demands the content hash in the signature,
// before the body is sent.
func (d *S3Destination) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	seeker, ok := r.(io.ReadSeeker)
	if !ok {
		return fmt.Errorf("backup: S3 uploads need a seekable source; stage the snapshot to a file first")
	}
	h := sha256.New()
	if _, err := io.Copy(h, seeker); err != nil {
		return fmt.Errorf("backup: hash payload: %w", err)
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup: rewind payload: %w", err)
	}
	payloadHash := hex.EncodeToString(h.Sum(nil))

	u, err := d.endpointURL(d.key(name), nil)
	if err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodPut, u, seeker, size, payloadHash)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (d *S3Destination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	u, err := d.endpointURL(d.key(name), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.do(ctx, http.MethodGet, u, nil, -1, emptyPayloadSHA256)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// listBucketResult mirrors the ListObjectsV2 response shape.
type listBucketResult struct {
	XMLName  xml.Name `xml:"ListBucketResult"`
	Contents []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
}

func (d *S3Destination) List(ctx context.Context) ([]Entry, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if p := strings.TrimPrefix(d.cfg.Prefix, "/"); p != "" {
		q.Set("prefix", p)
	}
	u, err := d.endpointURL("", q)
	if err != nil {
		return nil, err
	}
	resp, err := d.do(ctx, http.MethodGet, u, nil, -1, emptyPayloadSHA256)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed listBucketResult
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("backup: parse S3 listing: %w", err)
	}

	var out []Entry
	for _, c := range parsed.Contents {
		name := c.Key
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		// A bucket is frequently shared; only ever surface our own snapshots.
		if !IsSnapshotName(name) {
			continue
		}
		out = append(out, Entry{Name: name, Size: c.Size, ModTime: c.LastModified})
	}
	return out, nil
}

func (d *S3Destination) Delete(ctx context.Context, name string) error {
	if !IsSnapshotName(name) {
		return fmt.Errorf("backup: refusing to delete %q: not a snapshot name", name)
	}
	u, err := d.endpointURL(d.key(name), nil)
	if err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodDelete, u, nil, -1, emptyPayloadSHA256)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
