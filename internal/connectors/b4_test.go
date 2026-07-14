package connectors

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func b4Reg(t *testing.T) *Registry {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// renderB4Form renders an action expected to produce a form body; returns method, url, and the
// parsed form values.
func renderB4Form(t *testing.T, r *Registry, provider, action string, args map[string]any, connExtra map[string]string) (string, url.Values) {
	t.Helper()
	a, ok := r.Action(provider, action)
	if !ok {
		t.Fatalf("%s.%s missing", provider, action)
	}
	_, u, body, ct, err := renderRequest(a, args, connExtra)
	if err != nil {
		t.Fatalf("render %s.%s: %v", provider, action, err)
	}
	if ct != "application/x-www-form-urlencoded" && len(body) > 0 {
		t.Fatalf("%s.%s content-type = %q, want form-urlencoded", provider, action, ct)
	}
	v, _ := url.ParseQuery(string(body))
	return u, v
}

func TestB4_RenderFormBasicsAndBracketAndArray(t *testing.T) {
	// Direct renderForm test: flat key, literal bracket key, array→repeated, empty omitted.
	form := map[string]string{
		"email":            "{{email}}",
		"metadata[source]": "{{source}}",
		"expand":           "{{expand}}",
		"name":             "{{name}}",
	}
	body, ct := renderForm(form, map[string]any{
		"email":  "a@b.com",
		"source": "web",
		"expand": []any{"customer", "charges"},
		// name omitted → empty → dropped
	})
	if ct != "application/x-www-form-urlencoded" {
		t.Fatalf("ct=%s", ct)
	}
	v, _ := url.ParseQuery(string(body))
	if v.Get("email") != "a@b.com" {
		t.Fatalf("email: %v", v)
	}
	if v.Get("metadata[source]") != "web" {
		t.Fatalf("bracket key not preserved: %v", v)
	}
	if got := v["expand"]; len(got) != 2 || got[0] != "customer" {
		t.Fatalf("array not expanded to repeated keys: %v", got)
	}
	if _, present := v["name"]; present {
		t.Fatalf("empty value should be omitted: %v", v)
	}
}

func TestB4_StripeCreateCustomer(t *testing.T) {
	r := b4Reg(t)
	p, ok := r.ProviderByName("stripe")
	if !ok || !p.IsAPIKey() || p.Auth.Placement != "basic" {
		t.Fatalf("stripe must be api_key basic, got %+v", p.Auth)
	}
	if len(r.Actions("stripe")) < 8 {
		t.Fatalf("want >=8 stripe actions, got %d", len(r.Actions("stripe")))
	}
	_, v := renderB4Form(t, r, "stripe", "stripe_create_customer", map[string]any{"email": "x@y.com", "name": "Acme"}, nil)
	if v.Get("email") != "x@y.com" || v.Get("name") != "Acme" {
		t.Fatalf("stripe create_customer form: %v", v)
	}
}

func TestB4_TwilioBasicUserAndSMS(t *testing.T) {
	r := b4Reg(t)
	p, ok := r.ProviderByName("twilio")
	if !ok || !p.IsAPIKey() || p.Auth.Placement != "basic" {
		t.Fatalf("twilio must be api_key basic, got %+v", p.Auth)
	}
	// Basic username = account_sid from connExtra; credential = auth token (password)
	req, _ := http.NewRequest("POST", "https://api.twilio.com/x", nil)
	applyAuth(req, p, "AUTHTOKEN", map[string]string{"account_sid": "AC123"})
	u, pw, ok := req.BasicAuth()
	if !ok || u != "AC123" || pw != "AUTHTOKEN" {
		t.Fatalf("twilio basic auth: u=%q pw=%q", u, pw)
	}
	// send_sms form body + templated base URL
	url, v := renderB4Form(t, r, "twilio", "twilio_send_sms",
		map[string]any{"To": "+1555", "From": "+1444", "Body": "hi"}, map[string]string{"account_sid": "AC123"})
	if v.Get("Body") != "hi" || v.Get("To") != "+1555" {
		t.Fatalf("twilio send_sms form: %v", v)
	}
	if url != "https://api.twilio.com/2010-04-01/Accounts/AC123/Messages.json" {
		t.Fatalf("twilio url not templated: %s", url)
	}
}

func TestB4_TrelloKeyAndTokenInQuery(t *testing.T) {
	r := b4Reg(t)
	p, ok := r.ProviderByName("trello")
	if !ok || !p.IsAPIKey() || p.Auth.Placement != "query" || p.Auth.ParamName != "token" {
		t.Fatalf("trello must be api_key query token, got %+v", p.Auth)
	}
	if len(r.Actions("trello")) < 8 {
		t.Fatalf("want >=8 trello actions, got %d", len(r.Actions("trello")))
	}
	// key comes from {{conn.trello_key}} in the action query (rendered by renderRequest)
	a, _ := r.Action("trello", "trello_list_boards")
	_, u, _, _, err := renderRequest(a, nil, map[string]string{"trello_key": "KEY123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "key=KEY123") {
		t.Fatalf("trello key not in query: %s", u)
	}
	// token is added by applyAuth (query placement)
	req, _ := http.NewRequest("GET", u, nil)
	applyAuth(req, p, "TOKEN456", nil)
	if req.URL.Query().Get("token") != "TOKEN456" {
		t.Fatalf("trello token not added by applyAuth: %s", req.URL.String())
	}
}

func TestB4_AllProvidersLoad(t *testing.T) {
	r := b4Reg(t)
	for _, name := range []string{"stripe", "twilio", "trello"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Fatalf("%s not loaded", name)
		}
		if !p.IsAPIKey() {
			t.Fatalf("%s should be api_key", name)
		}
		if len(r.Actions(name)) < 7 {
			t.Fatalf("%s has <7 actions: %d", name, len(r.Actions(name)))
		}
	}
}
