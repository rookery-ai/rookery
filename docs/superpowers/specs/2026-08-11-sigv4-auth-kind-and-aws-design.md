# A SigV4 auth kind, and AWS as a connector

**Date:** 2026-08-11
**Status:** approved, ready for implementation
**Scope:** Spec C of four. See also: `2026-08-11-reconnect-and-workspace-images-design.md` (A),
`2026-08-11-cli-coder-model-and-ai-providers-design.md` (B),
`2026-08-11-connector-expansion-waves-design.md` (D).

Every connector today authenticates by putting a credential somewhere in the request:
a Bearer header, a named header, a query parameter, or HTTP Basic. AWS cannot be reached
that way — it requires the request itself to be signed. This spec adds a fifth auth kind
and ships one provider on it.

AWS is the only hyperscaler in scope. GCP and Azure are excluded; see the end.

## The existing signer is nearly general, and has two defects that only reuse exposes

`internal/backup/sigv4.go` already takes region *and* service as parameters — the
S3-specific behaviour lives in `dest_s3.go`, not in the signer. It is much better prior
art than "S3-shaped". Two things are wrong for general use, and neither can bite the
backup code as written:

**1. The canonical query string is encoded incorrectly for values containing spaces.**

```go
canonicalQuery := req.URL.Query().Encode()
```

`url.Values.Encode` escapes a space as `+`. SigV4 requires RFC 3986 percent-encoding,
which is `%20`. Any request whose query carries a space produces a signature AWS rejects
with `SignatureDoesNotMatch`. Backup never hits it because its only query values are
snapshot names and a bucket prefix, which contain no spaces — but an arbitrary connector
filter certainly will. The shared signer must build the canonical query itself,
percent-encoding each key and value per RFC 3986 and sorting by encoded key.

**2. Only three headers are ever signed.** The signer hardcodes `host`,
`x-amz-content-sha256` and `x-amz-date`. AWS requires that any `X-Amz-*` header a service
demands also appear in `SignedHeaders` — `X-Amz-Target` for the JSON-RPC style APIs, for
instance. `Content-Type` must likewise be signed when present. The shared signer must
collect the header set from the request: `host`, `content-type` when set, and every
`X-Amz-*` header, lowercased and sorted.

A third, cosmetic item: the credential-missing error reads
`"backup: S3 credentials are not configured"` and needs to stop naming backup.

## Design

### A shared `internal/awssig` package

Move the signer to its own package rather than exporting it from `internal/backup`. Two
consumers with no other relationship should not import each other, and the package
boundary is where the two fixes above get their own tests.

```go
package awssig

// Sign signs req in place with AWS Signature Version 4.
func Sign(req *http.Request, cred Credentials, region, service, payloadSHA256 string, now time.Time) error
```

`internal/backup/dest_s3.go` becomes a caller. Its existing SigV4 tests move with the
signer and are joined by cases for the two defects: a query value containing a space, and
a request carrying `Content-Type` plus an `X-Amz-*` header.

Signing against the published AWS test vectors is the strongest available check and does
not need an AWS account. Use them.

### `auth.kind: sigv4` in the connector layer

`applyAuth` gains a branch. It is the first auth kind that needs the request *body*, not
just headers, so the signature changes to carry the payload hash:

```go
func applyAuth(req *http.Request, prov Provider, credential string, connExtra map[string]string, payloadSHA256 string)
```

Every existing branch ignores the new parameter. `Execute` computes the hash after
rendering the body — which it already holds in memory, so nothing needs to become
seekable the way backup's file upload did. An empty body uses the well-known
SHA-256-of-empty-string constant.

Provider YAML:

```yaml
auth:
  kind: sigv4
  service_arg: service     # which connect_input holds the AWS service name
  region_arg: region       # which connect_input holds the region
```

Service and region come from the connection rather than the provider, because one AWS
connection reaches many services and every region is a different signing scope.

### Where the credentials live — the part that is easy to get wrong

`service_connections.extra` is **plaintext JSON**. This is a deliberate, recorded property:
the Meta page-token hook was designed to move a credential *out* of `extra` and into
`encrypted_access_token` precisely so that an "encrypt extra" change was unnecessary.

Therefore:

| Value | Stored in | Why |
|---|---|---|
| Secret access key | `encrypted_access_token` | It is the credential. Encrypted under the system key like every other. |
| Access key ID | `extra` | An identifier, not a secret — it appears in the `Authorization` header of every signed request anyway. |
| Region | `extra` | Configuration. |

The paste-key form collects all three: the secret key through the normal credential field
(so it is never logged or echoed), and the key id plus region through `connect_inputs`.
`key_label` and `key_hint` name them properly — "AWS secret access key", not the generic
"AWS API key", using the fields added for exactly this reason.

A test asserting the secret key never appears in `extra` is worth writing, because the
failure is invisible: everything works, and the secret sits in cleartext in the database.

### `aws.yaml` — one provider, read-mostly actions

Category `Cloud`, a new category. Scope the first release to reads and low-risk operations
across four services, each an action whose `service` connect-input value is fixed by the
action's own template:

- **S3** — list buckets, list objects, get object metadata
- **EC2** — describe instances, describe regions, instance status
- **Lambda** — list functions, get function configuration, invoke
- **CloudWatch** — get metric statistics, describe alarms

`lambda_invoke` is marked `mutating: true`. Nothing is marked `public_write`: none of these
publishes anything publicly, and `public_write` is deliberately narrower than `mutating`.

EC2 and CloudWatch use Query-protocol APIs whose parameters go in the query string; S3
returns XML rather than JSON. `response_extract` walks dotted paths over decoded JSON and
cannot read XML, so **S3 actions must request JSON where the API offers it and are
otherwise limited to what the connector layer can express**. Where an action cannot be
expressed cleanly, leave it out of the first release rather than shipping one that
returns an unusable blob — the bridge's 8 KiB cap will truncate it into something that
looks like data and is not.

## Testing

- The AWS published SigV4 test vectors, verbatim.
- A query value containing a space signs with `%20`, not `+`.
- `Content-Type` and `X-Amz-*` headers appear in `SignedHeaders`.
- `applyAuth` with `kind: sigv4` produces an `Authorization: AWS4-HMAC-SHA256 …` header
  and leaves the four existing kinds byte-identical.
- The secret access key is absent from `extra` after a connect.
- Backup's S3 destination still round-trips against its existing fake — the move must not
  regress a working, shipped code path.

Live verification needs an AWS account. Absent one, `aws.yaml` ships with
`unverified: true`, which `TestWave1ProvidersDeclareVerificationStatus` requires anyway.

## Deliberately excluded

- **GCP** — authenticates by signing a JWT with a service-account private key and
  exchanging it for a short-lived token. That is a token *minting* flow, not a request
  signing scheme; it needs its own token store semantics, not an `applyAuth` branch.
- **Azure** — `api-key` header plus a mandatory `api-version` query parameter, which is
  closer to the existing `api_key` kind than to this one, and is a separate, smaller
  piece of work.

Both are recorded here so that "AWS shipped, where are the other two" has an answer that
is a reason rather than an omission.
