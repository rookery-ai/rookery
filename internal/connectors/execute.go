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
	token, err := store.AccessToken(ctx, conn)
	if err != nil {
		return Result{}, err // TokenStore returns a typed ConnectorError
	}
	method, u, body, contentType, err := renderRequest(a, args, conn.Extra)
	if err != nil {
		return Result{}, &ConnectorError{KindOther, err.Error()}
	}
	prov, _ := reg.OAuthProvider(conn.Provider) // static headers + auth config; resolves auth_parent for aliased providers
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
		applyAuth(req, prov, token, conn.Extra)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for hk, hv := range prov.StaticHeaders {
			req.Header.Set(hk, hv)
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
	return Result{Data: extract(a.ResponseExtract, raw)}, nil
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
	if strings.HasPrefix(path, "$.") {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			if v, ok := m[strings.TrimPrefix(path, "$.")]; ok {
				return v
			}
		}
	}
	return raw
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
