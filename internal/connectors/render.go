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

// lonePlaceholderRE matches a string that is EXACTLY one {{name}} placeholder, so its
// substituted value keeps the arg's real type (array/int/bool) instead of stringifying.
var lonePlaceholderRE = regexp.MustCompile(`^\{\{([\w.]+)\}\}$`)

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

// renderBody walks a nested body template (maps/arrays/scalars). A leaf that is exactly
// one {{arg}} adopts the arg's real value/type; if that arg is absent/nil the key is
// OMITTED (present=false). A placeholder embedded in a larger string renders to string.
// Returned values are real Go values (marshaled by the caller) so user data can never
// break the JSON.
func renderBody(node any, args map[string]any, connVars map[string]string) (any, bool) {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			if rv, ok := renderBody(v, args, connVars); ok {
				out[k] = rv
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(n))
		for _, e := range n {
			if rv, ok := renderBody(e, args, connVars); ok {
				out = append(out, rv)
			}
		}
		return out, true
	case string:
		if m := lonePlaceholderRE.FindStringSubmatch(n); m != nil {
			name := m[1]
			if strings.HasPrefix(name, "conn.") {
				return connVars[strings.TrimPrefix(name, "conn.")], true
			}
			v, present := args[name]
			if !present || v == nil {
				return nil, false
			}
			return v, true
		}
		return subst(n, args, connVars), true
	default:
		return n, true // scalar literal (int/bool/float from YAML)
	}
}

type bodyBuilder func(args map[string]any) (body []byte, contentType string, err error)

var bodyBuilders = map[string]bodyBuilder{
	"gmail_rfc822":     gmailRFC822,
	"gmail_draft":      gmailDraft,
	"gmail_reply":      gmailReply,
	"notion_page":      notionPage,
	"msgraph_sendmail": msgraphSendMail,
	"msgraph_draft":    msgraphDraft,
	"jira_issue":       jiraIssue,
	"jira_comment":     jiraComment,
	"drive_folder":     driveFolder,
}

// driveFolder builds a Drive create-folder body, including parents (as a single-element
// array) only when parent_id is provided. Args: name, parent_id (optional).
func driveFolder(args map[string]any) ([]byte, string, error) {
	m := map[string]any{
		"name":     asString(args["name"]),
		"mimeType": "application/vnd.google-apps.folder",
	}
	if p := asString(args["parent_id"]); p != "" {
		m["parents"] = []string{p}
	}
	b, err := json.Marshal(m)
	return b, "application/json", err
}

// adf wraps plain text in a minimal Atlassian Document Format doc (Jira API v3 requires
// ADF for description/comment bodies, not a plain string).
func adf(text string) map[string]any {
	return map[string]any{
		"type": "doc", "version": 1,
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": text}},
		}},
	}
}

// jiraIssue builds a create-issue payload. Args: project_key, summary, issue_type, description.
func jiraIssue(args map[string]any) ([]byte, string, error) {
	issueType := asString(args["issue_type"])
	if issueType == "" {
		issueType = "Task"
	}
	fields := map[string]any{
		"project":   map[string]string{"key": asString(args["project_key"])},
		"summary":   asString(args["summary"]),
		"issuetype": map[string]string{"name": issueType},
	}
	if desc := asString(args["description"]); desc != "" {
		fields["description"] = adf(desc)
	}
	b, err := json.Marshal(map[string]any{"fields": fields})
	return b, "application/json", err
}

// jiraComment builds an add-comment payload (ADF body). Args: body.
func jiraComment(args map[string]any) ([]byte, string, error) {
	b, err := json.Marshal(map[string]any{"body": adf(asString(args["body"]))})
	return b, "application/json", err
}

// msgraphMessage builds the shared Microsoft Graph message object from flat args.
func msgraphMessage(args map[string]any) map[string]any {
	msg := map[string]any{
		"subject": asString(args["subject"]),
		"body":    map[string]string{"contentType": "Text", "content": asString(args["body"])},
	}
	if to := asString(args["to"]); to != "" {
		msg["toRecipients"] = []any{map[string]any{"emailAddress": map[string]string{"address": to}}}
	}
	return msg
}

// msgraphSendMail wraps the message for POST /me/sendMail (sends immediately).
func msgraphSendMail(args map[string]any) ([]byte, string, error) {
	b, err := json.Marshal(map[string]any{"message": msgraphMessage(args), "saveToSentItems": true})
	return b, "application/json", err
}

// msgraphDraft is the bare message for POST /me/messages (creates a draft).
func msgraphDraft(args map[string]any) ([]byte, string, error) {
	b, err := json.Marshal(msgraphMessage(args))
	return b, "application/json", err
}

// notionPage builds a minimal valid Notion "create page" payload under a page parent:
// a title property plus one paragraph block for the body. Args: parent_id, title, content.
func notionPage(args map[string]any) ([]byte, string, error) {
	payload := map[string]any{
		"parent": map[string]string{"page_id": asString(args["parent_id"])},
		"properties": map[string]any{
			"title": map[string]any{
				"title": []any{map[string]any{"text": map[string]string{"content": asString(args["title"])}}},
			},
		},
	}
	if content := asString(args["content"]); content != "" {
		payload["children"] = []any{map[string]any{
			"object": "block", "type": "paragraph",
			"paragraph": map[string]any{
				"rich_text": []any{map[string]any{"text": map[string]string{"content": content}}},
			},
		}}
	}
	b, err := json.Marshal(payload)
	return b, "application/json", err
}

// renderForm builds an application/x-www-form-urlencoded body from a flat form map. Each value is
// a {{arg}} template; empty results are omitted. A lone-placeholder whose arg is an array expands
// to repeated key=value pairs (form array convention). Keys are used literally (Stripe/Twilio
// bracket notation like "metadata[source]" is preserved).
func renderForm(form map[string]string, args map[string]any) ([]byte, string) {
	v := url.Values{}
	for k, tmpl := range form {
		if m := lonePlaceholderRE.FindStringSubmatch(tmpl); m != nil {
			if arr, ok := args[m[1]].([]any); ok {
				for _, e := range arr {
					v.Add(k, asString(e))
				}
				continue
			}
		}
		val := subst(tmpl, args, nil)
		if val == "" {
			continue
		}
		v.Set(k, val)
	}
	return []byte(v.Encode()), "application/x-www-form-urlencoded"
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
	case a.Request.BodyArg != "":
		body, err = json.Marshal(args[a.Request.BodyArg])
		contentType = "application/json"
	case a.Request.BodyBuilder != "":
		bb, ok := bodyBuilders[a.Request.BodyBuilder]
		if !ok {
			return "", "", nil, "", fmt.Errorf("unknown body_builder %q", a.Request.BodyBuilder)
		}
		body, contentType, err = bb(args)
	case len(a.Request.Form) > 0:
		body, contentType = renderForm(a.Request.Form, args)
	case len(a.Request.Body) > 0:
		rendered, _ := renderBody(a.Request.Body, args, connVars)
		body, err = json.Marshal(rendered)
		contentType = "application/json"
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

// gmailReply builds a threaded reply: the RFC822 message plus the threadId so Gmail keeps
// it in-thread. Args: thread_id, to, subject, body.
func gmailReply(args map[string]any) ([]byte, string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(rfc822(args)))
	body, err := json.Marshal(map[string]any{"raw": raw, "threadId": asString(args["thread_id"])})
	return body, "application/json", err
}
