package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OAuthClient performs the self-managed OAuth 2.0 flows. HTTP is injectable for tests.
type OAuthClient struct{ HTTP *http.Client }

func (c OAuthClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// TokenSet is the result of an authorization-code exchange or refresh.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	// Extra captures provider.TokenExtra fields from the token response (e.g. Salesforce
	// instance_url) for merging into service_connections.extra.
	Extra map[string]string
}

// ConsentURL builds the provider consent (authorization) URL. access_type=offline +
// prompt=consent ensure a refresh token is issued. Named ConsentURL (not AuthorizeURL)
// to avoid colliding with the Provider.AuthorizeURL config field. scopes is passed in
// explicitly (rather than read from p.DefaultScopes) so a child provider aliased via
// auth_parent can request ITS OWN scopes against the PARENT's authorize endpoint.
func (p Provider) ConsentURL(clientID, redirectURI, state string, scopes []string) string {
	q := url.Values{}
	q.Set(p.ClientIDParam(), clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	if len(scopes) > 0 { // Notion sends no scope param
		q.Set("scope", strings.Join(scopes, " "))
	}
	q.Set("state", state)
	// access_type=offline / prompt=consent are Google-isms — providers that need them (or
	// audience, owner, etc.) declare them in authorize_extra so nothing leaks to others.
	for k, v := range p.AuthorizeExtra {
		q.Set(k, v)
	}
	sep := "?"
	if strings.Contains(p.AuthorizeURL, "?") {
		sep = "&"
	}
	return p.AuthorizeURL + sep + q.Encode()
}

func (c OAuthClient) tokenRequest(ctx context.Context, p Provider, form url.Values, clientID, clientSecret string) (TokenSet, error) {
	// token_auth: "basic" sends client creds as HTTP Basic (Notion); default sends them in
	// the form body (Google, GitHub, MS, Atlassian). For basic, strip them from the body.
	if p.TokenAuth == "basic" {
		form.Del(p.ClientIDParam())
		form.Del("client_secret")
	}
	var bodyReader io.Reader
	contentType := "application/x-www-form-urlencoded"
	if p.TokenContentType == "json" {
		// Notion: the token endpoint wants a JSON object, not a form body.
		obj := map[string]string{}
		for k := range form {
			obj[k] = form.Get(k)
		}
		jb, mErr := json.Marshal(obj)
		if mErr != nil {
			return TokenSet{}, mErr
		}
		bodyReader = bytes.NewReader(jb)
		contentType = "application/json"
	} else {
		bodyReader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.TokenURL, bodyReader)
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if p.TokenAuth == "basic" {
		req.SetBasicAuth(clientID, clientSecret)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return TokenSet{}, &ConnectorError{KindNetwork, err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		// Classify rather than collapsing everything onto KindAuth. The caller
		// (DBTokenStore.refresh) uses this to decide whether to mark the
		// connection dead: only a definitive rejection by the provider should,
		// because a row marked NEEDS_REAUTH leaves ConnectionsNearExpiry's
		// status='ACTIVE' filter and is never renewed again. A 500 must not
		// permanently brick a healthy connection.
		kind := KindAuth
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			kind = KindRateLimit
		case resp.StatusCode >= 500:
			kind = KindServer
		}
		return TokenSet{}, &ConnectorError{kind, fmt.Sprintf("token endpoint %d: %s", resp.StatusCode, string(b))}
	}
	return parseTokenResponse(b, p)
}

// parseTokenResponse decodes a token endpoint JSON body into a TokenSet, also capturing any
// prov.TokenExtra fields (e.g. Salesforce instance_url) into TokenSet.Extra.
func parseTokenResponse(b []byte, prov Provider) (TokenSet, error) {
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return TokenSet{}, err
	}
	ts := TokenSet{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresIn: out.ExpiresIn}
	if len(prov.TokenExtra) > 0 {
		var raw map[string]any
		if json.Unmarshal(b, &raw) == nil {
			ts.Extra = map[string]string{}
			for _, k := range prov.TokenExtra {
				if v, ok := raw[k].(string); ok {
					ts.Extra[k] = v
				}
			}
		}
	}
	return ts, nil
}

// ExchangeCode swaps an authorization code for tokens.
func (c OAuthClient) ExchangeCode(ctx context.Context, p Provider, clientID, clientSecret, code, redirectURI string) (TokenSet, error) {
	f := url.Values{}
	f.Set("grant_type", "authorization_code")
	f.Set("code", code)
	f.Set(p.ClientIDParam(), clientID)
	f.Set("client_secret", clientSecret)
	f.Set("redirect_uri", redirectURI)
	return c.tokenRequest(ctx, p, f, clientID, clientSecret)
}

// Refresh renews an access token. Google omits a new refresh token on refresh, so the
// existing one is preserved.
//
// Providers with token_expiry: exchange (Meta) have no refresh token at all — they
// re-exchange the CURRENT access token for a fresh long-lived one. Refresh routes to
// that path so callers (DBTokenStore, RunRefreshLoop) need no provider-specific branch.
func (c OAuthClient) Refresh(ctx context.Context, p Provider, clientID, clientSecret, refreshToken string) (TokenSet, error) {
	if p.UsesTokenExchange() {
		return c.ExchangeLongLived(ctx, p, clientID, clientSecret, refreshToken)
	}
	f := url.Values{}
	f.Set("grant_type", "refresh_token")
	f.Set("refresh_token", refreshToken)
	f.Set(p.ClientIDParam(), clientID)
	f.Set("client_secret", clientSecret)
	ts, err := c.tokenRequest(ctx, p, f, clientID, clientSecret)
	if err != nil {
		return ts, err
	}
	if ts.RefreshToken == "" {
		ts.RefreshToken = refreshToken
	}
	return ts, nil
}

// ExchangeLongLived swaps a token for a longer-lived one via Meta's fb_exchange_token
// grant. It is NOT a refresh grant: there is no refresh token in the Meta model, so the
// value passed in is the current ACCESS token and the result replaces it.
//
// The returned TokenSet carries the new access token as its refresh token as well. That
// looks odd but is deliberate: the store persists RefreshToken and hands it back on the
// next renewal, and for this provider the thing you exchange next time IS the current
// access token. Without it, the second renewal would have nothing to send.
func (c OAuthClient) ExchangeLongLived(ctx context.Context, p Provider, clientID, clientSecret, currentToken string) (TokenSet, error) {
	f := url.Values{}
	grant := p.ExchangeGrant()
	f.Set("grant_type", grant)
	f.Set(grant, currentToken)
	f.Set(p.ClientIDParam(), clientID)
	f.Set("client_secret", clientSecret)
	ts, err := c.tokenRequest(ctx, p, f, clientID, clientSecret)
	if err != nil {
		return ts, err
	}
	if ts.RefreshToken == "" {
		ts.RefreshToken = ts.AccessToken
	}
	return ts, nil
}

// FetchIdentity reads the connected account's identity (e.g. email) from the provider's
// userinfo endpoint using the given access token.
func (c OAuthClient) FetchIdentity(ctx context.Context, p Provider, accessToken string) (string, error) {
	if p.UserinfoURL == "" { // e.g. Notion — no userinfo endpoint; identity stays blank
		return "", nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", p.UserinfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", &ConnectorError{KindNetwork, err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", &ConnectorError{KindAuth, fmt.Sprintf("userinfo %d: %s", resp.StatusCode, string(b))}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	if v, ok := m[p.IdentityPath].(string); ok {
		return v, nil
	}
	return "", nil
}
