package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Execute is the single typed choke point for every MCP tool call, from either coder
// kind. It mirrors connectors.Execute so that changing coder kind, or moving a
// capability from a connector to an MCP server, cannot change the safety posture.
//
// Order is deliberate and matches its connector sibling:
//
//	validate → enabled → build guard → park → call
//
// Validation comes first so a malformed call is rejected before a human is ever
// asked to approve it. Parking comes before the network call, since approval may
// arrive hours later and the ARGS are what gets replayed then.
func Execute(ctx context.Context, c Caller, srv BoundServer, tool Tool, args map[string]any, pol Policy) (Result, error) {
	if err := validateArgs(tool.InputSchema, args); err != nil {
		return Result{}, errf(KindBadArgs, err.Error())
	}

	// Defense in depth. A disabled tool is not in ToolDefs, so a well-behaved caller
	// never reaches here — but the catalog can change between the tool list being
	// built for a run and a call landing minutes later.
	if tool.Name == "" {
		return Result{}, errf(KindBadArgs, "no MCP tool named in the call")
	}

	// The build-phase guard reads the OWNER's read_only column, never the server's
	// readOnlyHint. The MCP spec requires clients to treat annotations as untrusted;
	// the hint only seeded this value at sync time and the owner may have corrected
	// it.
	if pol.BuildPhase && !tool.ReadOnly {
		return Result{}, errf(KindBuildBlocked, fmt.Sprintf(
			"build-time guard: %q on %q is not marked read-only and is blocked during generation — it will run when the agent executes for real",
			tool.Name, srv.Name))
	}

	if pol.Parker != nil && tool.ApprovalMode == "approve" {
		id, err := pol.Parker.Park(ctx, srv, tool.Name, args)
		if err != nil {
			return Result{}, errf(KindOther, "could not queue this call for approval: "+err.Error())
		}
		if id != "" {
			// A SUCCESS, not an error: the coder's tool loop treats any `error:`
			// string as a failing call worth retrying, and a queued action is the
			// system working as configured. The note must be impossible to read as
			// "done", or an agent will report a lie to its owner.
			b, mErr := json.Marshal(ParkedResult{
				Status: "queued_for_approval",
				ID:     id,
				Note: fmt.Sprintf("This call was NOT performed. It is queued for %s's approval and will run only if approved. Do not report it as completed.",
					"the owner"),
			})
			if mErr != nil {
				return Result{}, errf(KindOther, mErr.Error())
			}
			return Result{Data: b}, nil
		}
		// Park returning ("", nil) means "not gated — send now", which is how a
		// mixed set of bindings is honoured.
	}

	return c.CallTool(ctx, srv, tool.Name, args)
}

// validateArgs checks a call against the tool's cached inputSchema.
//
// It is deliberately shallow — required-key presence and top-level type agreement —
// because the SERVER is the authority on its own arguments and will reject what it
// dislikes. The value here is catching the model's common mistakes (a missing
// required field, a string where a number belongs) before a network round-trip, and
// before parking a call a human would be asked to approve.
func validateArgs(schema json.RawMessage, args map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	var s struct {
		Type       string                     `json:"type"`
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		// An unparseable schema is not the caller's fault; let the server decide.
		return nil
	}

	var missing []string
	for _, r := range s.Required {
		if _, ok := args[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required argument(s): %s", strings.Join(missing, ", "))
	}

	for name, raw := range s.Properties {
		v, ok := args[name]
		if !ok || v == nil {
			continue
		}
		var p struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Type == "" {
			continue
		}
		if !typeMatches(p.Type, v) {
			return fmt.Errorf("argument %q should be a %s", name, p.Type)
		}
	}
	return nil
}

func typeMatches(want string, v any) bool {
	switch want {
	case "string":
		_, ok := v.(string)
		return ok
	case "number", "integer":
		switch v.(type) {
		case float64, float32, int, int32, int64, json.Number:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	}
	return true
}
