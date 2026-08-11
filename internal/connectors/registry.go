// Package connectors owns the self-managed-OAuth connector layer: per-provider
// OAuth configs + curated action manifests (embedded data files), and the typed
// Execute path agents call. Adding a service = adding a providers/<p>.yaml and a
// connectors/<p>.yaml; no Go changes.
package connectors

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"

	"github.com/ilijad1/rookery/internal/publicurl"
	"gopkg.in/yaml.v3"
)

//go:embed providers/*.yaml connectors/*.yaml
var files embed.FS

// AuthConfig declares how a provider authenticates. Absent or kind=="oauth2" → the
// legacy OAuth Bearer path. kind=="api_key" → a static user-supplied key injected per
// placement; drives the connect UI (no OAuth app, a paste-key form) too.
type AuthConfig struct {
	Kind        string `yaml:"kind"`         // "oauth2" (default) | "api_key"
	Placement   string `yaml:"placement"`    // "header" | "query" | "basic"
	HeaderName  string `yaml:"header_name"`  // for placement=header
	ValuePrefix string `yaml:"value_prefix"` // e.g. "Bearer "
	ParamName   string `yaml:"param_name"`   // for placement=query
	KeyLabel    string `yaml:"key_label"`    // UI: "OpenAI API key"
	KeyHint     string `yaml:"key_hint"`     // UI placeholder: "sk-..."
	SetupURL    string `yaml:"setup_url"`    // UI: where to get the key

	// The three below are for kind=="sigv4" (AWS Signature Version 4), and each
	// names the connect_input holding that value. AWS is the only scheme here
	// that signs the request rather than carrying a credential in it, so unlike
	// every other kind it needs the region and the service — one connection
	// reaches many services, and every region is a separate signing scope.
	//
	// The SECRET access key is the credential and travels encrypted like all the
	// others. The access key id is NOT secret (it is in the Authorization header
	// of every signed request) and lives in the connection's plaintext `extra`,
	// alongside the region. Defaults: access_key_id, region, service.
	AccessKeyArg string `yaml:"access_key_arg"`
	RegionArg    string `yaml:"region_arg"`
	ServiceArg   string `yaml:"service_arg"`

	// SessionURL, for kind=="session_exchange", is the endpoint that swaps stored
	// credentials for a short-lived bearer token (Bluesky's createSession).
	SessionURL string `yaml:"session_url"`
	// SessionIdentityKey names the connect_input holding the account identifier sent
	// alongside the credential (Bluesky's handle).
	SessionIdentityKey string `yaml:"session_identity_key"`

	// BasicPassLiteral, for placement=="basic", makes the CREDENTIAL the username and
	// this fixed string the password — the inverse of BasicUserTemplate. Toggl Track is
	// why it exists: it wants the API token as the user and the literal "api_token" as
	// the password. BasicUserTemplate wins when both are set.
	BasicPassLiteral string `yaml:"basic_pass_literal"`

	// BasicUserTemplate, for placement=="basic", is a {{conn.<key>}} template resolved
	// against the connection's Extra to produce the HTTP Basic username (the credential
	// is always the password). Empty means the legacy behavior: credential as username,
	// empty password (e.g. ClickUp-style bare tokens use placement=header instead).
	BasicUserTemplate string `yaml:"basic_user_template"`
}

// OAuthCreds names the two fields a provider's own developer console shows for its OAuth
// app, so the connect form asks for what the user is actually looking at. Meta says
// "App ID"/"App Secret", Salesforce "Consumer Key"/"Consumer Secret", Outlook
// "Application (client) ID" — the form hardcoded "Client ID"/"Client secret" for all of
// them, leaving the user to guess the two were the same thing.
//
// Every field is optional and defaults INDEPENDENTLY. X declares only hints, because its
// portal shows TWO credential pairs (OAuth 1.0a "API Key" and OAuth 2.0 "Client ID") and
// the labels are already right — the disambiguation is the whole value. An empty field
// falls back to "Client ID"/"Client secret", which is correct for the eight providers
// whose console genuinely says that.
//
// This lives at the provider top level rather than inside AuthConfig because most OAuth
// providers (google, github, notion) declare no auth: block at all, and nesting would
// force one into files that do not otherwise need it.
//
// A provider with AuthParent set must leave this empty: OAuthProvider() resolves the child
// to its parent before the labels are read, so a block on the child would never be shown,
// and its presence would state something false about where the credentials go.
// TestOAuthCredLabelsAreNonBlank rejects it.
type OAuthCreds struct {
	IDLabel     string `yaml:"id_label"`
	IDHint      string `yaml:"id_hint"`
	SecretLabel string `yaml:"secret_label"`
	SecretHint  string `yaml:"secret_hint"`
}

// Provider is one service's OAuth configuration.
type Provider struct {
	Name string `yaml:"name"`
	// Unverified marks a provider whose action manifest has NOT been confirmed against
	// the live API with cmd/livecheck. It is data rather than a comment so the UI can
	// surface it, and so "which providers are guesses" is answerable by a test.
	Unverified    bool     `yaml:"unverified"`
	AuthorizeURL  string   `yaml:"authorize_url"`
	TokenURL      string   `yaml:"token_url"`
	UserinfoURL   string   `yaml:"userinfo_url"`
	IdentityPath  string   `yaml:"identity_path"`
	DefaultScopes []string `yaml:"default_scopes"`

	// TokenExpiry is "expiring" (default), "never", or "exchange".
	//   never    — GitHub, Notion: the access token does not expire and must never be
	//              refreshed; connect/refresh store an empty expires_at and AccessToken
	//              treats empty as valid.
	//   exchange — Meta: there is NO refresh token. A short-lived token is swapped for a
	//              ~60-day one via the fb_exchange_token grant, and renewed by exchanging
	//              the CURRENT access token again before it expires. OAuthClient.Refresh
	//              routes here so callers need no provider-specific branch.
	TokenExpiry string `yaml:"token_expiry"`
	// TokenExchangeGrant overrides the grant_type used when TokenExpiry=="exchange".
	// Meta uses fb_exchange_token (the default); Threads uses th_exchange_token.
	TokenExchangeGrant string `yaml:"token_exchange_grant"`
	// ClientParam is the parameter name carrying the OAuth client id in BOTH the consent
	// URL and the token request. Defaults to "client_id"; TikTok calls it "client_key".
	ClientParam string `yaml:"client_param"`
	// TokenExtra names fields to capture from the token endpoint's JSON response into
	// TokenSet.Extra / service_connections.extra (e.g. Salesforce's instance_url).
	TokenExtra []string `yaml:"token_extra"`
	// TokenAuth is "body" (default) or "basic": how client_id/secret reach the token
	// endpoint. Notion requires HTTP Basic auth; most providers accept them in the body.
	TokenAuth string `yaml:"token_auth"`
	// TokenContentType is "form" (default, application/x-www-form-urlencoded) or "json"
	// (application/json). Notion's token endpoint requires a JSON body.
	TokenContentType string `yaml:"token_content_type"`
	// StaticHeaders are merged into every action request (e.g. Notion-Version, GitHub Accept).
	StaticHeaders map[string]string `yaml:"static_headers"`
	// AuthorizeExtra are extra query params on the consent URL (e.g. Atlassian audience/prompt).
	AuthorizeExtra map[string]string `yaml:"authorize_extra"`
	// PostConnect names a one-time resolution hook run at callback whose result is stored in
	// service_connections.extra and exposed to URL templates as {{conn.<key>}}. "" = none.
	// Supported: "atlassian_cloudid".
	PostConnect string `yaml:"post_connect"`

	// AuthParent names another provider whose OAuth app + endpoints this provider reuses.
	// A child (e.g. google_drive → google) declares only its own scopes/actions/label; its
	// authorize_url/token_url/token settings/app-credentials all resolve from the parent.
	AuthParent string `yaml:"auth_parent"`

	// UI guidance for obtaining OAuth app credentials (shown on the Services page).
	// Category groups this provider on the connections page. One of: Google,
	// Publishing & Media, Advertising, Productivity, Communication, Commerce,
	// Developer, Support, Other. Empty renders under Other rather than vanishing.
	Category   string   `yaml:"category"`
	Label      string   `yaml:"label"`       // human-friendly name, e.g. "Google (Gmail)"
	SetupURL   string   `yaml:"setup_url"`   // link to the provider's developer console
	SetupSteps []string `yaml:"setup_steps"` // numbered instructions to create the OAuth client

	// OAuthCreds renames the two credential fields on the connect form to match this
	// provider's console. Empty means "Client ID"/"Client secret".
	OAuthCreds OAuthCreds `yaml:"oauth_creds"`

	Auth AuthConfig `yaml:"auth"`

	// ConnectInputs are extra per-connection fields collected on the api-key connect form
	// (e.g. Shopify's store domain). Stored in service_connections.extra and exposed to
	// request templates + auth as {{conn.<key>}}.
	ConnectInputs []ConnectInput `yaml:"connect_inputs"`

	// KeyExtra maps an extra per-connection key to a derive rule applied to the pasted API
	// key at connect time (e.g. Mailchimp's datacenter from the key suffix). Only "suffix"
	// is supported. Derived values are merged into service_connections.extra alongside
	// ConnectInputs and exposed to request templates as {{conn.<key>}}.
	KeyExtra map[string]string `yaml:"key_extra"`

	// RedirectPolicy declares what this provider accepts as a redirect URI, so
	// the connect UI can tell the user their instance URL will not work BEFORE
	// they create an OAuth app and click through consent. An absent block is the
	// zero Policy: fully permissive and unverified, which can only ever warn.
	RedirectPolicy publicurl.Policy `yaml:"redirect_policy"`
}

// WithConnVars returns a copy of the provider with its OAuth endpoint URLs resolved
// against per-connection values.
//
// Mastodon is why this exists: every instance is its own OAuth server, so
// authorize_url/token_url/userinfo_url are templates over {{conn.instance}} rather than
// constants. The values come from connect_inputs, which are collected BEFORE consent
// and ride the signed state — so both the consent URL and the callback's token exchange
// can resolve them. Providers with literal URLs are unaffected: subst leaves a string
// with no placeholders untouched.
func (p Provider) WithConnVars(vars map[string]string) Provider {
	if len(vars) == 0 {
		return p
	}
	p.AuthorizeURL = subst(p.AuthorizeURL, nil, vars)
	p.TokenURL = subst(p.TokenURL, nil, vars)
	p.UserinfoURL = subst(p.UserinfoURL, nil, vars)
	return p
}

// ConnectInput is a per-connection value collected on the api-key connect form and stored in
// service_connections.extra (exposed to request templates + auth as {{conn.<key>}}).
type ConnectInput struct {
	Key      string `yaml:"key"`
	Label    string `yaml:"label"`
	Hint     string `yaml:"hint"`
	Required bool   `yaml:"required"`
	// Normalize names a canonicalizer applied to the pasted value at connect time.
	// "" (default) stores the value verbatim. "base_url" runs NormalizeBaseURL —
	// see internal/connectors/baseurl.go.
	Normalize string `yaml:"normalize"`
}

// IsAPIKey reports whether this provider authenticates with a static API key.
func (p Provider) IsAPIKey() bool { return p.Auth.Kind == "api_key" }

// IsKeyless reports whether this provider needs no credential at all (Open-Meteo).
// Its connections store no secret, are never refreshed, and connect with a bare
// button rather than a paste-a-key form.
func (p Provider) IsKeyless() bool { return p.Auth.Kind == "none" }

// UsesSessionExchange reports whether the stored credential is swapped for a
// short-lived bearer token on use (Bluesky: handle + app password → accessJwt).
//
// It is a third auth model: the credential never expires like an API key, but the
// value actually sent on a request does — so it is neither IsAPIKey (send the stored
// value verbatim) nor OAuth (no authorization-code flow, no refresh token).
func (p Provider) UsesSessionExchange() bool { return p.Auth.Kind == "session_exchange" }

// UsesSigV4 reports whether requests are SIGNED with AWS Signature Version 4
// rather than carrying a credential in a header. The stored credential is the
// secret access key; the key id and region ride in the connection's `extra`.
func (p Provider) UsesSigV4() bool { return p.Auth.Kind == "sigv4" }

// PastesCredential reports whether the connect UI should show the paste-a-credential
// form rather than an OAuth app setup. api_key, session_exchange and sigv4 all do —
// none of them runs a consent flow, so none has a redirect URI to register.
func (p Provider) PastesCredential() bool {
	return p.IsAPIKey() || p.UsesSessionExchange() || p.UsesSigV4()
}

// NonExpiring reports whether this provider's access tokens never expire.
func (p Provider) NonExpiring() bool { return p.TokenExpiry == "never" }

// UsesTokenExchange reports whether renewal goes through Meta's fb_exchange_token grant
// rather than a standard refresh_token grant. Such providers issue no refresh token at
// all; the current access token is what gets exchanged.
func (p Provider) UsesTokenExchange() bool { return p.TokenExpiry == "exchange" }

// ExchangeGrant is the grant_type for the token-exchange renewal path.
func (p Provider) ExchangeGrant() string {
	if p.TokenExchangeGrant != "" {
		return p.TokenExchangeGrant
	}
	return "fb_exchange_token"
}

// ClientIDParam is the parameter name carrying the client id. Providers overwhelmingly
// use "client_id"; TikTok is the exception, and getting it wrong yields an opaque
// "invalid request" rather than anything naming the parameter.
func (p Provider) ClientIDParam() string {
	if p.ClientParam != "" {
		return p.ClientParam
	}
	return "client_id"
}

// RequestTemplate describes how to turn typed args into a real provider HTTP request.
type RequestTemplate struct {
	Method      string            `yaml:"method"`
	URL         string            `yaml:"url"`
	Query       map[string]string `yaml:"query"`
	BodyBuilder string            `yaml:"body_builder"`
	BodyJSON    map[string]string `yaml:"body_json"`
	// Body is a nested template (maps/arrays) rendered to JSON by renderBody. Preferred over
	// BodyJSON for anything non-flat. BodyBuilder still wins when set (non-JSON encodings).
	Body map[string]any `yaml:"body"`
	// BodyArg names a single object-typed arg whose value becomes the entire request body
	// (e.g. Salesforce sObject create/update takes the raw fields object, no wrapper key).
	BodyArg string `yaml:"body_arg"`
	// Form is a flat map of application/x-www-form-urlencoded field -> {{arg}} template
	// (e.g. Stripe/Twilio writes). Keys are used literally, so bracket notation like
	// "metadata[source]" is preserved. Rendered by renderForm.
	Form map[string]string `yaml:"form"`
	// Headers are per-ACTION headers, as opposed to the provider-wide
	// static_headers. AWS's JSON-RPC services are why this exists: they dispatch
	// entirely on X-Amz-Target, whose value names the operation and so differs
	// per action rather than per provider. Values are {{arg}}/{{conn.key}}
	// templates like everywhere else, and an empty result is dropped rather than
	// sent blank. Set BEFORE auth is applied, so a signing scheme covers them.
	Headers map[string]string `yaml:"headers"`
}

// ResponseFilter narrows a JSON ARRAY response client-side, for APIs that offer no
// server-side filter of their own. Home Assistant is the motivating case: GET
// /api/states returns every entity in the house, so an entity_prefix parameter that
// the request ignored would be a lie — the filter is what makes it true.
type ResponseFilter struct {
	// Field is the object key each array element is matched on (e.g. "entity_id").
	Field string `yaml:"field"`
	// PrefixArg names the action argument holding the prefix to match. An absent or
	// empty argument means no filtering, so the action still works unfiltered.
	PrefixArg string `yaml:"prefix_arg"`
}

// Action is one curated, typed operation on a provider.
type Action struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Mutating    bool   `yaml:"mutating"`
	// PublicWrite marks an action that publishes irreversibly to a public audience —
	// a post, a comment, an upload. `Mutating` is too blunt to gate on: pausing an ad
	// campaign is mutating but private and reversible, while a LinkedIn post is
	// neither. Only PublicWrite actions are ever eligible for the approval gate, so
	// enabling the gate never makes an agent wait on a routine write.
	//
	// A PublicWrite action is always Mutating too; RunGuardrails-style consistency is
	// enforced by TestPublicWriteImpliesMutating rather than by the loader, so a data
	// file with the pair wrong fails the build rather than silently skipping a guard.
	PublicWrite     bool            `yaml:"public_write"`
	ParamsRaw       map[string]any  `yaml:"params"`
	Request         RequestTemplate `yaml:"request"`
	ResponseExtract string          `yaml:"response_extract"`
	// ResponseFilter optionally narrows an array response after ResponseExtract runs.
	ResponseFilter ResponseFilter  `yaml:"response_filter"`
	Params         json.RawMessage `yaml:"-"` // compiled JSON schema from ParamsRaw
}

type manifest struct {
	Provider string   `yaml:"provider"`
	Actions  []Action `yaml:"actions"`
}

// Registry holds the loaded providers + actions.
type Registry struct {
	providers map[string]Provider
	actions   map[string][]Action // provider -> actions
}

// LoadBundled parses every embedded provider config + action manifest.
func LoadBundled() (*Registry, error) {
	r := &Registry{providers: map[string]Provider{}, actions: map[string][]Action{}}

	pents, err := files.ReadDir("providers")
	if err != nil {
		return nil, err
	}
	for _, e := range pents {
		b, err := files.ReadFile(path.Join("providers", e.Name()))
		if err != nil {
			return nil, err
		}
		var p Provider
		if err := yaml.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("provider %s: %w", e.Name(), err)
		}
		r.providers[p.Name] = p
	}

	cents, err := files.ReadDir("connectors")
	if err != nil {
		return nil, err
	}
	for _, e := range cents {
		b, err := files.ReadFile(path.Join("connectors", e.Name()))
		if err != nil {
			return nil, err
		}
		var m manifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", e.Name(), err)
		}
		for i := range m.Actions {
			raw, err := json.Marshal(m.Actions[i].ParamsRaw)
			if err != nil {
				return nil, fmt.Errorf("%s.%s params: %w", m.Provider, m.Actions[i].Name, err)
			}
			m.Actions[i].Params = raw
		}
		r.actions[m.Provider] = m.Actions
	}
	return r, nil
}

// ProviderByName returns the OAuth config for a provider.
func (r *Registry) ProviderByName(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// OAuthProvider returns the provider whose OAuth config governs authentication for name:
// the auth_parent when set (one level), else the provider itself. Used for endpoints, token
// settings, static_headers, authorize_extra, and the app-credentials lookup key. ProviderByName
// still returns the child itself (its scopes, label, actions, post_connect).
func (r *Registry) OAuthProvider(name string) (Provider, bool) {
	p, ok := r.providers[name]
	if !ok {
		return Provider{}, false
	}
	if p.AuthParent != "" {
		if parent, ok := r.providers[p.AuthParent]; ok {
			return parent, true
		}
		return Provider{}, false
	}
	return p, true
}

// ProviderNames returns every loaded provider slug, sorted. The connections page renders
// this set, so it must be deterministic — map iteration order is not.
func (r *Registry) ProviderNames() []string {
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Actions returns all actions declared for a provider.
func (r *Registry) Actions(provider string) []Action { return r.actions[provider] }

// Action returns one named action for a provider.
func (r *Registry) Action(provider, name string) (Action, bool) {
	for _, a := range r.actions[provider] {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// BoundConn is a runner/UI-facing view of a connection an agent is bound to.
type BoundConn struct {
	ID, Provider, AccountLabel, AccountIdentity string
	Extra                                       map[string]string // resolved per-connection values (e.g. cloudid)
}

// RedirectPolicy returns the redirect-URI policy governing a provider.
//
// It resolves through OAuthProvider, so an aliased child (google_drive → google)
// inherits its parent's policy. That is required for correctness rather than
// convenience: the redirect URI is registered against the PARENT's OAuth app, so
// the parent's rules are the ones the provider will enforce.
func (r *Registry) RedirectPolicy(provider string) publicurl.Policy {
	if p, ok := r.OAuthProvider(provider); ok {
		return p.RedirectPolicy
	}
	if p, ok := r.ProviderByName(provider); ok {
		return p.RedirectPolicy
	}
	return publicurl.Policy{}
}
