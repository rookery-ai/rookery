package connectors

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var placeholderRE = regexp.MustCompile(`\{\{([\w.]+)\}\}`)

// asString renders an arg value for substitution (integers without a trailing .0).
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// subst replaces {{name}} from args and {{conn.key}} from connVars. connVars may be nil.
func subst(tmpl string, args map[string]any, connVars map[string]string) string {
	return placeholderRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := placeholderRE.FindStringSubmatch(m)[1]
		if strings.HasPrefix(name, "conn.") {
			return connVars[strings.TrimPrefix(name, "conn.")]
		}
		return asString(args[name])
	})
}

type bodyBuilder func(args map[string]any) (body []byte, contentType string, err error)

var bodyBuilders = map[string]bodyBuilder{
	"gmail_rfc822": gmailRFC822,
	"gmail_draft":  gmailDraft,
}

// renderRequest turns an action + typed args into a concrete HTTP request. It
// substitutes {{name}} placeholders in the URL + query (dropping query params whose
// value resolved empty) and dispatches the configured body_builder / body_json.
func renderRequest(a Action, args map[string]any, connVars map[string]string) (method, u string, body []byte, contentType string, err error) {
	method = a.Request.Method
	if method == "" {
		method = "GET"
	}
	u = subst(a.Request.URL, args, connVars)
	if len(a.Request.Query) > 0 {
		q := url.Values{}
		for k, tmpl := range a.Request.Query {
			val := subst(tmpl, args, connVars)
			if val == "" {
				continue // drop unresolved/empty params
			}
			q.Set(k, val)
		}
		if enc := q.Encode(); enc != "" {
			u += "?" + enc
		}
	}
	switch {
	case a.Request.BodyBuilder != "":
		bb, ok := bodyBuilders[a.Request.BodyBuilder]
		if !ok {
			return "", "", nil, "", fmt.Errorf("unknown body_builder %q", a.Request.BodyBuilder)
		}
		body, contentType, err = bb(args)
	case len(a.Request.BodyJSON) > 0:
		m := map[string]any{}
		for k, tmpl := range a.Request.BodyJSON {
			m[k] = subst(tmpl, args, connVars)
		}
		body, err = json.Marshal(m)
		contentType = "application/json"
	}
	return method, u, body, contentType, err
}

func rfc822(args map[string]any) string {
	var b strings.Builder
	b.WriteString("To: " + asString(args["to"]) + "\r\n")
	if s := asString(args["subject"]); s != "" {
		b.WriteString("Subject: " + s + "\r\n")
	}
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(asString(args["body"]))
	return b.String()
}

func gmailRFC822(args map[string]any) ([]byte, string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(rfc822(args)))
	body, err := json.Marshal(map[string]string{"raw": raw})
	return body, "application/json", err
}

func gmailDraft(args map[string]any) ([]byte, string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(rfc822(args)))
	body, err := json.Marshal(map[string]any{"message": map[string]string{"raw": raw}})
	return body, "application/json", err
}
