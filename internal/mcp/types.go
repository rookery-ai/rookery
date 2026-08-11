// Package mcp is Rookery's Model Context Protocol client layer.
//
// It is a deliberate peer of internal/connectors, mirroring it shape-for-shape:
// ToolDefs/ResolveTool define tool naming exactly once for both coder paths, and
// Execute is the single typed choke point carrying the build-phase guard and the
// approval parker. Everything downstream — the API engine's native tools, the CLI
// coder's loopback bridge, the per-agent binding, the approval queue — therefore
// treats an MCP tool and a connector action identically.
//
// The one structural difference: a connector's actions are vendored YAML compiled
// into the binary, whereas an MCP server's tools are DISCOVERED from that server at
// runtime (tools/list) and cached in the mcp_tools table. Nothing about an MCP server
// ships with Rookery — the owner supplies the URL, the server supplies the catalog.
//
// Two deliberate omissions, both security properties rather than gaps:
//
//   - This package does NOT use the internal/nethttp private-address dial guard,
//     mirroring connectors and for the same recorded reason: the URL is typed by the
//     single owner and self-hosted servers live at RFC1918 or Tailscale addresses.
//     See TestExecuteReachesPrivateAddresses.
//   - Rookery advertises neither sampling nor elicitation. A server can otherwise ask
//     the client to run an LLM completion (spending the owner's tokens) or to prompt
//     the human — and a scheduled run at 03:00 has no human to prompt.
package mcp

import (
	"context"
	"encoding/json"
)

// Kind classifies an Error so callers can react and the model gets an actionable
// message. It mirrors connectors.Kind and adds the two cases only MCP has.
type Kind int

const (
	KindOther Kind = iota
	KindAuth
	KindRateLimit
	KindServer
	KindNetwork
	KindBuildBlocked
	KindBadArgs
	// KindUnreachable is a server that did not answer — distinct from KindAuth
	// because only a definitive rejection may flip a server to NEEDS_AUTH. A DNS
	// blip must not cost the owner a working server until they reconnect it by hand.
	KindUnreachable
	// KindUnsupported is a server asking for something this wave declines: OAuth
	// (deferred), or an input_required/elicitation round-trip that a headless run
	// cannot satisfy.
	KindUnsupported
)

// Error is the single error type surfaced from this package. Its message is already
// actionable and is handed to the model verbatim.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(k Kind, msg string) *Error { return &Error{Kind: k, Msg: msg} }

// Tool is one tool from a server's catalog, as this package sees it.
type Tool struct {
	// Name is the server's own tool name, verbatim — what tools/call is invoked
	// with. MCP permits dots and up to 128 characters here.
	Name string
	// ToolName is the slugged, truncated name exposed to the model. It is stored in
	// the DB rather than recomputed at call time, so an upstream rename cannot
	// silently re-point a name the model already learned mid-run.
	ToolName    string
	Title       string
	Description string
	InputSchema json.RawMessage
	// ReadOnly is the OWNER's judgement, seeded from the server's readOnlyHint. The
	// MCP spec requires clients to treat annotations as untrusted, so Execute's
	// build-phase guard reads this and never the annotation.
	ReadOnly     bool
	ApprovalMode string // auto|approve
}

// BoundServer is one MCP server exposed to a coder for a run, chat turn or build,
// carrying its enabled tools and a DECRYPTED credential.
//
// The credential is decrypted here because this struct never leaves the host
// process: the API engine holds it in memory and the CLI bridge resolves it
// server-side from a per-run token. It is never written to disk or handed to a
// subprocess — which is precisely why native --mcp-config passthrough was rejected.
type BoundServer struct {
	ID          string
	WorkspaceID string
	Name        string
	Slug        string
	URL         string
	AuthKind    string // none|bearer|header
	HeaderName  string
	Token       string
	Tools       []Tool
}

// Result is the normalized payload of a successful tool call.
type Result struct {
	Data json.RawMessage
	// IsError marks a TOOL EXECUTION error (the server's isError:true) as opposed to
	// a protocol or transport failure, which is returned as an error instead.
	//
	// The distinction is load-bearing. The MCP spec says clients should hand
	// execution errors to the model so it can self-correct ("date must be in the
	// future"), so the caller renders this WITHOUT the `error:` prefix that the API
	// engine's oscillation guard counts. Reversing the two either kills legitimate
	// retry-with-fixed-args or lets a dead server spin out the turn budget.
	IsError bool
}

// Policy carries the per-call rules Execute enforces before touching the network.
// The zero value is permissive — no build guard, no approval gate — which is what a
// run and a chat turn both want.
type Policy struct {
	// BuildPhase blocks non-read-only tools during agent generation: a build must
	// exercise real read paths without acting for real on the owner's behalf.
	BuildPhase bool

	// Parker, when non-nil, gates tools marked for approval: Execute hands the call
	// to Parker and returns its queue ticket as a SUCCESSFUL result. Nil means no
	// gate. The caller decides whether a call is gated; Execute only asks the tool
	// whether it is eligible, which keeps the per-binding DB lookup out of a package
	// that knows nothing about agents.
	Parker Parker
}

// Parker records a gated call for the owner to approve later.
type Parker interface {
	Park(ctx context.Context, srv BoundServer, tool string, args map[string]any) (ticketID string, err error)
}

// ParkedResult is the payload Execute returns for a gated call. It is a SUCCESS, not
// an error: the coder's tool loop treats any `error:` string as a failing call worth
// retrying, and a parked call is neither.
//
// Note is written for the model. An agent that records a queued action as done would
// report a lie to its owner, so the wording must be impossible to read as success.
type ParkedResult struct {
	Status string `json:"status"`
	ID     string `json:"pending_action_id"`
	Note   string `json:"note"`
}

// Caller performs one tools/call against a server. Client implements it; tests
// substitute a fake so Execute's gating can be tested without a live server.
type Caller interface {
	CallTool(ctx context.Context, srv BoundServer, tool string, args map[string]any) (Result, error)
}
