package connectors

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// These are catalog-wide invariants rather than per-provider assertions: they hold for
// every provider that exists now and every one added later, which is what makes them
// worth having as the catalog grows past 79 providers and 400 actions.

// A tool name is what the model sees, and both coder kinds slug it into
// ^[a-zA-Z0-9_-]{1,64}$. A name that violates it is rejected by the provider at call
// time — long after the YAML looked fine.
func TestEveryActionNameIsAValidToolName(t *testing.T) {
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	seen := map[string]string{}
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			if !valid.MatchString(a.Name) {
				t.Errorf("%s/%s is not a valid tool name", prov, a.Name)
			}
			// A duplicate across providers collides in the single-account tool set,
			// where names are exposed bare.
			if other, dup := seen[a.Name]; dup {
				t.Errorf("action name %q is used by both %s and %s", a.Name, other, prov)
			}
			seen[a.Name] = prov
		}
	}
}

// The description IS the model's instruction sheet — it is all it has to choose between
// 400+ tools. An empty or terse one makes the tool unusable in practice.
func TestEveryActionHasAUsefulDescription(t *testing.T) {
	r, _ := LoadBundled()
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			if len(strings.TrimSpace(a.Description)) < 20 {
				t.Errorf("%s/%s description is too thin to pick a tool by: %q", prov, a.Name, a.Description)
			}
		}
	}
}

// Every action must declare a params schema, even an empty one. A missing schema means
// validateArgs accepts anything, so a malformed call reaches the provider as a 400 the
// model cannot diagnose.
func TestEveryActionDeclaresParams(t *testing.T) {
	r, _ := LoadBundled()
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			if len(a.Params) == 0 {
				t.Errorf("%s/%s declares no params schema", prov, a.Name)
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal(a.Params, &obj); err != nil {
				t.Errorf("%s/%s params is not a JSON object: %v", prov, a.Name, err)
			}
		}
	}
}

// Every required parameter must appear somewhere the request can consume it — the URL,
// the query, a body, or a response filter. A required arg referenced nowhere is
// collected from the model and silently dropped, which is worse than not asking.
func TestRequiredParamsAreActuallyUsed(t *testing.T) {
	r, _ := LoadBundled()
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			var schema struct {
				Required []string `json:"required"`
			}
			if json.Unmarshal(a.Params, &schema) != nil {
				continue
			}
			// Assemble everything a parameter could legitimately be referenced from.
			blob := a.Request.URL + " " + a.Request.BodyArg + " " + a.ResponseFilter.PrefixArg
			for _, v := range a.Request.Query {
				blob += " " + v
			}
			for _, v := range a.Request.Form {
				blob += " " + v
			}
			for _, v := range a.Request.BodyJSON {
				blob += " " + v
			}
			if b, err := json.Marshal(a.Request.Body); err == nil {
				blob += " " + string(b)
			}
			// A body_builder assembles the body in Go, so its params cannot be seen here.
			if a.Request.BodyBuilder != "" {
				continue
			}
			for _, req := range schema.Required {
				if !strings.Contains(blob, "{{"+req+"}}") &&
					!strings.Contains(blob, "{{"+req+"|escape}}") &&
					a.Request.BodyArg != req && a.ResponseFilter.PrefixArg != req {
					t.Errorf("%s/%s requires %q but never references it", prov, a.Name, req)
				}
			}
		}
	}
}

// A connect_input is collected from the user, so it must be used. One that no action
// and no auth template references is a question asked for nothing.
func TestConnectInputsAreReferenced(t *testing.T) {
	r, _ := LoadBundled()
	for _, prov := range r.ProviderNames() {
		p, _ := r.ProviderByName(prov)
		if len(p.ConnectInputs) == 0 {
			continue
		}
		blob := p.AuthorizeURL + p.TokenURL + p.UserinfoURL + p.Auth.BasicUserTemplate
		for _, v := range p.StaticHeaders {
			blob += " " + v
		}
		for _, a := range r.Actions(prov) {
			blob += " " + a.Request.URL
			for _, v := range a.Request.Query {
				blob += " " + v
			}
			if b, err := json.Marshal(a.Request.Body); err == nil {
				blob += " " + string(b)
			}
		}
		for _, ci := range p.ConnectInputs {
			// post_connect and token_extra populate Extra from the provider side, so a
			// value the hooks resolve is legitimately unreferenced at connect time.
			if p.PostConnect != "" {
				continue
			}
			if !strings.Contains(blob, "{{conn."+ci.Key+"}}") {
				t.Errorf("%s asks for connect input %q but nothing references {{conn.%s}}",
					prov, ci.Key, ci.Key)
			}
		}
	}
}

// A keyless provider must not ask for a credential, and an api_key provider must say
// what to paste — the connect form has no other label to show.
func TestAuthConfigIsCoherent(t *testing.T) {
	r, _ := LoadBundled()
	for _, prov := range r.ProviderNames() {
		p, _ := r.ProviderByName(prov)
		switch {
		case p.IsKeyless():
			if p.Auth.KeyLabel != "" || p.Auth.KeyHint != "" {
				t.Errorf("%s is keyless but declares a key label/hint", prov)
			}
		case p.IsAPIKey():
			if p.Auth.KeyLabel == "" {
				t.Errorf("%s is api_key but has no key_label — the form would show a blank field", prov)
			}
			if p.Auth.Placement == "header" && p.Auth.HeaderName == "" {
				t.Errorf("%s uses header placement with no header_name", prov)
			}
			if p.Auth.Placement == "query" && p.Auth.ParamName == "" {
				t.Errorf("%s uses query placement with no param_name", prov)
			}
		}
	}
}

// The same trap in the optional direction: a parameter the model is offered but the
// request never reads. It is quieter than the required case — the call still succeeds —
// which is exactly why it survives review. The model passes lang=mk, gets English back,
// and nothing anywhere reports a problem.
func TestOptionalParamsAreActuallyUsed(t *testing.T) {
	r, _ := LoadBundled()
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			if a.Request.BodyBuilder != "" {
				continue // assembled in Go; not visible here
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if json.Unmarshal(a.Params, &schema) != nil {
				continue
			}
			blob := a.Request.URL + " " + a.Request.BodyArg + " " + a.ResponseFilter.PrefixArg
			for _, v := range a.Request.Query {
				blob += " " + v
			}
			for _, v := range a.Request.Form {
				blob += " " + v
			}
			for _, v := range a.Request.BodyJSON {
				blob += " " + v
			}
			if b, err := json.Marshal(a.Request.Body); err == nil {
				blob += " " + string(b)
			}
			for name := range schema.Properties {
				if strings.Contains(blob, "{{"+name+"}}") ||
					strings.Contains(blob, "{{"+name+"|escape}}") ||
					a.Request.BodyArg == name || a.ResponseFilter.PrefixArg == name {
					continue
				}
				t.Errorf("%s/%s offers parameter %q but the request never reads it", prov, a.Name, name)
			}
		}
	}
}
