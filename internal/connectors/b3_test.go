package connectors

import (
	"encoding/json"
	"testing"
)

func b3Reg(t *testing.T) *Registry {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// renderB3 renders an action with the given args + connExtra, returning method, url, and parsed body.
func renderB3(t *testing.T, r *Registry, provider, action string, args map[string]any, connExtra map[string]string) (string, map[string]any) {
	t.Helper()
	a, ok := r.Action(provider, action)
	if !ok {
		t.Fatalf("%s.%s missing", provider, action)
	}
	_, u, body, _, err := renderRequest(a, args, connExtra)
	if err != nil {
		t.Fatalf("render %s.%s: %v", provider, action, err)
	}
	var m map[string]any
	if len(body) > 0 {
		json.Unmarshal(body, &m)
	}
	return u, m
}

func TestB3_ShopifyConnectInputsAndURL(t *testing.T) {
	r := b3Reg(t)
	p, ok := r.ProviderByName("shopify")
	if !ok || !p.IsAPIKey() {
		t.Fatal("shopify must load as api_key")
	}
	// connect_inputs declares the shop field
	if len(p.ConnectInputs) == 0 || p.ConnectInputs[0].Key != "shop" {
		t.Fatalf("shopify must declare connect_input shop, got %+v", p.ConnectInputs)
	}
	// {{conn.shop}} resolves in the action URL
	u, _ := renderB3(t, r, "shopify", "shopify_list_products", nil, map[string]string{"shop": "acme.myshopify.com"})
	if u != "https://acme.myshopify.com/admin/api/2024-10/products.json" {
		t.Fatalf("shop not substituted into URL: %s", u)
	}
	// api-key header uses X-Shopify-Access-Token with no prefix
	if p.Auth.HeaderName != "X-Shopify-Access-Token" || p.Auth.ValuePrefix != "" {
		t.Fatalf("bad shopify auth: %+v", p.Auth)
	}
}

func TestB3_TokenExtraCapture(t *testing.T) {
	// A token response carrying instance_url is captured into TokenSet.Extra for a provider
	// declaring token_extra: [instance_url].
	p := Provider{TokenExtra: []string{"instance_url"}}
	ts, err := parseTokenResponse([]byte(`{"access_token":"AT","expires_in":3600,"instance_url":"https://na1.salesforce.com"}`), p)
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "AT" || ts.Extra["instance_url"] != "https://na1.salesforce.com" {
		t.Fatalf("token_extra not captured: %+v", ts)
	}
}

func TestB3_SalesforceBodyArgAndURL(t *testing.T) {
	r := b3Reg(t)
	if _, ok := r.ProviderByName("salesforce"); !ok {
		t.Fatal("salesforce not loaded")
	}
	// body_arg: whole body is the fields object
	_, m := renderB3(t, r, "salesforce", "salesforce_create_sobject",
		map[string]any{"type": "Account", "fields": map[string]any{"Name": "Acme"}}, map[string]string{"instance_url": "https://na1.salesforce.com"})
	if m["Name"] != "Acme" {
		t.Fatalf("body_arg should marshal fields as the whole body, got %v", m)
	}
	// {{conn.instance_url}} resolves in the URL
	u, _ := renderB3(t, r, "salesforce", "salesforce_get_sobject",
		map[string]any{"type": "Account", "id": "001"}, map[string]string{"instance_url": "https://na1.salesforce.com"})
	if u != "https://na1.salesforce.com/services/data/v60.0/sobjects/Account/001" {
		t.Fatalf("instance_url not substituted: %s", u)
	}
}
