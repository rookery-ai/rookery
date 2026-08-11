package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Kind classifies a ConnectorError so callers can react (and the model gets an
// actionable message).
type Kind int

const (
	KindOther Kind = iota
	KindAuth
	KindRateLimit
	KindServer
	KindNetwork
	KindBuildBlocked
	KindBadArgs
	KindNeedsReauth
)

// ConnectorError is the single error type surfaced from the connector layer. Its
// message is already actionable and is surfaced verbatim to the model.
type ConnectorError struct {
	Kind Kind
	Msg  string
}

func (e *ConnectorError) Error() string { return e.Msg }

// ConnRef identifies the connection an action runs against, without importing db.
type ConnRef struct {
	ID, Provider, AccountIdentity string
	// Extra holds per-connection resolved values (e.g. Jira cloudid) exposed to request
	// URL templates as {{conn.<key>}}. Nil for providers with no post-connect resolution.
	Extra map[string]string
}

// TokenStore returns a currently-valid bearer token for a connection, refreshing +
// persisting as needed. Implemented by DBTokenStore.
type TokenStore interface {
	AccessToken(ctx context.Context, conn ConnRef) (string, error)
}

// Result is the normalized payload of a successful action (response_extract applied).
type Result struct {
	Data json.RawMessage
}

// Policy carries the per-call execution rules Execute enforces before touching the
// network. It replaced a bare `buildPhase bool` parameter so new rules (the approval
// gate) could be added without changing the signature at every call site again.
//
// The zero value is the permissive default — no build guard, no approval gate — which
// is what a run, a chat turn, and the livecheck harness all want.
type Policy struct {
	// BuildPhase blocks mutating actions during agent/skill generation: a build must
	// exercise real read paths without sending anything on the user's behalf.
	BuildPhase bool

	// Parker, when non-nil, gates public_write actions: instead of calling the
	// provider, Execute hands the call to Parker and returns its queue ticket as a
	// SUCCESSFUL result. Nil (the default) means no gate — today's behaviour.
	//
	// The caller decides whether this call is gated; Execute only asks the action
	// whether it is eligible. That split keeps the per-binding approval_mode lookup
	// (a DB read) out of this package, which knows nothing about agents.
	Parker Parker
}

// Parker parks a public_write call for the owner to approve later, returning the
// ticket id. Implemented by the approval service; nil in every non-gated path.
//
// Returning ("", nil) means "this call is NOT gated — send it normally". That is how
// the per-binding approval_mode is honoured: one agent run can hold several
// connections with different modes, so the decision cannot be made once up front when
// the Policy is built. The Parker owns the lookup because it has the agent context;
// this package knows nothing about agents.
type Parker interface {
	Park(ctx context.Context, conn ConnRef, action string, args map[string]any) (ticketID string, err error)
}

// ParkedResult is the payload Execute returns for a gated call. It is a SUCCESS, not
// an error: the coder's tool loop treats any `error:` string as a failing call worth
// retrying or blocking on, and a parked post is neither — it is the system working as
// configured.
//
// Note is written for the model, not the user. An agent that records a queued post as
// published would never retry it and would report a lie to its owner, so the wording
// has to be impossible to read as success.
type ParkedResult struct {
	Status string `json:"status"`
	ID     string `json:"pending_action_id"`
	Action string `json:"action"`
	Note   string `json:"note"`
}

// Execute is the typed choke point every connector call goes through: validate args,
// enforce the policy guards, fetch a fresh token, render + send the provider request
// (one transient retry), and normalize the response/errors.
func Execute(ctx context.Context, reg *Registry, store TokenStore, client *http.Client,
	conn ConnRef, actionName string, args map[string]any, pol Policy) (Result, error) {

	a, ok := reg.Action(conn.Provider, actionName)
	if !ok {
		return Result{}, &ConnectorError{KindOther, fmt.Sprintf("unknown action %q for %s", actionName, conn.Provider)}
	}
	if err := validateArgs(a.Params, args); err != nil {
		return Result{}, &ConnectorError{KindBadArgs, err.Error()}
	}
	if a.Mutating && pol.BuildPhase {
		return Result{}, &ConnectorError{KindBuildBlocked,
			fmt.Sprintf("build-time guard: %q sends/modifies for real and is blocked during generation — it will run when the agent executes for real", actionName)}
	}
	// The approval gate sits AFTER arg validation (so a malformed call is rejected
	// rather than parked for a human to discover is broken) and BEFORE the token
	// fetch (parking must not depend on a live token — approval can arrive hours
	// later, and the token is refreshed when the call is actually sent).
	if a.PublicWrite && pol.Parker != nil {
		id, err := pol.Parker.Park(ctx, conn, actionName, args)
		if err != nil {
			return Result{}, &ConnectorError{KindOther, "could not queue for approval: " + err.Error()}
		}
		// An empty id means this binding is on 'auto' — not gated. Fall through and
		// send now, rather than treating "no ticket" as a failure.
		if id != "" {
			payload, err := json.Marshal(ParkedResult{
				Status: "queued_for_approval",
				ID:     id,
				Action: actionName,
				Note: "NOT yet published — this is awaiting the owner's approval and may never " +
					"be sent. Do NOT record it as posted, and do not retry it. The owner will " +
					"be notified of the outcome separately.",
			})
			if err != nil {
				return Result{}, &ConnectorError{KindOther, err.Error()}
			}
			return Result{Data: payload}, nil
		}
	}
	token, err := store.AccessToken(ctx, conn)
	if err != nil {
		return Result{}, err // TokenStore returns a typed ConnectorError
	}
	method, u, body, contentType, err := renderRequest(a, args, conn.Extra)
	if err != nil {
		return Result{}, &ConnectorError{KindOther, err.Error()}
	}
	prov, _ := reg.OAuthProvider(conn.Provider) // auth config; resolves auth_parent for aliased providers
	// Static headers merge parent-then-child: an aliased child inherits the parent's
	// (Notion-Version, GitHub Accept) AND may add its own. Reading only the parent's
	// would silently drop google_ads's developer-token header, since its parent
	// `google` declares none — and the call would 401 with nothing naming the cause.
	child, _ := reg.ProviderByName(conn.Provider)
	staticHeaders := map[string]string{}
	for k, v := range prov.StaticHeaders {
		staticHeaders[k] = v
	}
	for k, v := range child.StaticHeaders {
		staticHeaders[k] = v
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	var raw []byte
	var status int
	for attempt := 0; attempt < 2; attempt++ {
		req, e := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
		if e != nil {
			return Result{}, &ConnectorError{KindOther, e.Error()}
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for hk, hv := range staticHeaders {
			// Header values are TEMPLATED, not literal: Google Ads carries its
			// developer token and manager id as headers sourced from the connection.
			// Copying verbatim would send the literal "{{conn.developer_token}}" and
			// 401 on every call.
			v := subst(hv, nil, conn.Extra)
			// Drop empties, mirroring the query renderer: an optional header sent as
			// "" is not the same as absent — Google Ads rejects a blank
			// login-customer-id, which most accounts do not have.
			if v == "" {
				continue
			}
			req.Header.Set(hk, v)
		}
		// Per-action headers, applied after the provider-wide ones so an action
		// can override its provider. Same templating and same drop-if-empty rule.
		for hk, hv := range a.Request.Headers {
			v := subst(hv, args, conn.Extra)
			if v == "" {
				continue
			}
			req.Header.Set(hk, v)
		}
		// Auth goes on LAST, after Content-Type and every static header. It used
		// to run first, which is fine for a scheme that only adds a header or a
		// query parameter — but SigV4 signs the request it is given, and a
		// Content-Type or x-amz-* header added afterwards would be either
		// unsigned or signed-but-absent. No provider sets Authorization through
		// static_headers, so nothing is clobbered by the reorder.
		if err := applyAuth(req, prov, token, conn.Extra, body); err != nil {
			return Result{}, err
		}
		resp, e := client.Do(req)
		if e != nil {
			if attempt == 0 {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			return Result{}, &ConnectorError{KindNetwork, e.Error()}
		}
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		status = resp.StatusCode
		if (status == 429 || status >= 500) && attempt == 0 {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		break
	}

	if status >= 400 {
		return Result{}, mapHTTPError(status, raw)
	}
	data := extract(a.ResponseExtract, raw)
	if a.ResponseFilter.Field != "" {
		data = applyResponseFilter(data, a.ResponseFilter, asString(args[a.ResponseFilter.PrefixArg]))
	}
	return Result{Data: data}, nil
}

func mapHTTPError(status int, raw []byte) *ConnectorError {
	msg := fmt.Sprintf("provider returned %d: %s", status, truncate(string(raw), 500))
	switch {
	case status == 401 || status == 403:
		return &ConnectorError{KindAuth, msg}
	case status == 429:
		return &ConnectorError{KindRateLimit, msg}
	case status >= 500:
		return &ConnectorError{KindServer, msg}
	default:
		return &ConnectorError{KindOther, msg}
	}
}

// extract applies a tiny subset of JSONPath: "$" (whole body) or "$.field" (top-level key).
func extract(path string, raw []byte) json.RawMessage {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return raw
	}
	if !strings.HasPrefix(path, "$.") {
		return raw
	}
	cur := json.RawMessage(raw)
	for _, seg := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		if seg == "" {
			return raw
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(cur, &m); err != nil {
			return raw
		}
		v, ok := m[seg]
		if !ok {
			return raw
		}
		cur = v
	}
	return cur
}

// applyResponseFilter keeps only the elements of a JSON array whose f.Field value
// starts with prefix. A non-array body, an empty prefix, a non-object element, or an
// element missing the field are all pass-through/no-match rather than errors — this
// runs on real third-party payloads and must never panic or invent an empty result.
func applyResponseFilter(raw json.RawMessage, f ResponseFilter, prefix string) json.RawMessage {
	if f.Field == "" || prefix == "" {
		return raw
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return raw
	}
	kept := make([]json.RawMessage, 0, len(arr))
	for _, el := range arr {
		var obj map[string]any
		if json.Unmarshal(el, &obj) != nil {
			continue
		}
		s, ok := obj[f.Field].(string)
		if !ok || !strings.HasPrefix(s, prefix) {
			continue
		}
		kept = append(kept, el)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return raw
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
