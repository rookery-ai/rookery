package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// handlerFunc implements an action in Go rather than as a single templated request.
// It runs only after every Execute gate has passed, so it never needs to re-check the
// build guard, the approval parker or scopes — and it must not perform a mutating call
// for an action that is not declared mutating in its YAML.
//
// The returned JSON is what the model reads, so it is shaped for the MODEL: short,
// self-describing, and never ambiguous about whether anything changed. A handler that
// cannot prove its write landed returns an error, never a hopeful success.
type handlerFunc func(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error)

// handlers is the registry an action's `handler:` field names. Entries are added by
// the files implementing them (gdocs_handlers.go, excel_handlers.go).
var handlers = map[string]handlerFunc{}

// ignoredLegacyParams lists parameters a handler action still ACCEPTS but no longer
// reads, keyed "action.param". They stay in the schema so an agent built against the
// old shape keeps validating; TestHandlerParamsAreRead exempts exactly these.
var ignoredLegacyParams = map[string]bool{
	// The end is now found by reading the document; a model-computed end_index was
	// the arithmetic this change exists to remove.
	"docs_append_text.end_index": true,
}

// handlerCall is a handler's only route to the provider. It reuses the exact request
// path a template action takes (auth, static headers, retry), so moving an action from
// a template to a handler cannot change how it authenticates.
type handlerCall struct {
	snd *sender
}

// maxHandlerRead bounds one response a handler reads. Higher than a template action's
// 4 MiB because a handler parses a whole document (a long Google Doc's JSON runs to
// several MiB) and its OUTPUT, not this input, is what reaches the model.
const maxHandlerRead = 32 << 20

// httpError is returned by call for an HTTP error status, so a handler can recognise
// the one status it expects (a revision conflict) and map the rest like Execute does.
type httpError struct {
	Status int
	Body   []byte
}

func (e *httpError) Error() string {
	return fmt.Sprintf("provider returned %d: %s", e.Status, truncate(string(e.Body), 500))
}

// call sends one JSON request and returns the raw body. A non-2xx status comes back as
// *httpError; transport failures as the sender's ConnectorError.
func (hc *handlerCall) call(ctx context.Context, method, u string, body any) ([]byte, error) {
	var b []byte
	ct := ""
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return nil, &ConnectorError{KindOther, err.Error()}
		}
		ct = "application/json"
	}
	raw, status, err := hc.snd.do(ctx, method, u, b, ct, maxHandlerRead)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, &httpError{Status: status, Body: raw}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte(`{}`), nil
	}
	return raw, nil
}

// toConnectorError maps whatever a handler's call returned into the ConnectorError
// taxonomy Execute's callers understand.
func toConnectorError(err error) error {
	if err == nil {
		return nil
	}
	if he, ok := err.(*httpError); ok {
		return mapHTTPError(he.Status, he.Body)
	}
	if _, ok := err.(*ConnectorError); ok {
		return err
	}
	return &ConnectorError{KindOther, err.Error()}
}

// failf is the error a handler returns when the MODEL must change its call: a missing
// anchor, an ambiguous one, a heading that does not exist. KindBadArgs, because the
// call was well-formed but cannot be carried out as asked.
func failf(format string, a ...any) error {
	return &ConnectorError{KindBadArgs, fmt.Sprintf(format, a...)}
}

func marshalResult(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &ConnectorError{KindOther, err.Error()}
	}
	return b, nil
}

// argString, argInt and argBool read optional arguments tolerantly: a missing value is
// the zero value, and an integer the model sent as a string ("3") is still an integer,
// since small models do that often and refusing it costs a turn for nothing.
func argString(args map[string]any, k string) string {
	return strings.TrimSpace(asString(args[k]))
}

func argInt(args map[string]any, k string) (int, bool) {
	switch v := args[k].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

func argBool(args map[string]any, k string) (bool, bool) {
	switch v := args[k].(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1":
			return true, true
		case "false", "no", "0":
			return false, true
		}
	}
	return false, false
}
