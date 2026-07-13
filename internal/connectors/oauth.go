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
}

// ConsentURL builds the provider consent (authorization) URL. access_type=offline +
// prompt=consent ensure a refresh token is issued. Named ConsentURL (not AuthorizeURL)
// to avoid colliding with the Provider.AuthorizeURL config field.
func (p Provider) ConsentURL(clientID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	if len(p.DefaultScopes) > 0 { // Notion sends no scope param
		q.Set("scope", strings.Join(p.DefaultScopes, " "))
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
		form.Del("client_id")
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
		return TokenSet{}, &ConnectorError{KindAuth, fmt.Sprintf("token endpoint %d: %s", resp.StatusCode, string(b))}
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return TokenSet{}, err
	}
	return TokenSet{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresIn: out.ExpiresIn}, nil
}

// ExchangeCode swaps an authorization code for tokens.
func (c OAuthClient) ExchangeCode(ctx context.Context, p Provider, clientID, clientSecret, code, redirectURI string) (TokenSet, error) {
	f := url.Values{}
	f.Set("grant_type", "authorization_code")
	f.Set("code", code)
	f.Set("client_id", clientID)
	f.Set("client_secret", clientSecret)
	f.Set("redirect_uri", redirectURI)
	return c.tokenRequest(ctx, p, f, clientID, clientSecret)
}

// Refresh renews an access token. Google omits a new refresh token on refresh, so the
// existing one is preserved.
func (c OAuthClient) Refresh(ctx context.Context, p Provider, clientID, clientSecret, refreshToken string) (TokenSet, error) {
	f := url.Values{}
	f.Set("grant_type", "refresh_token")
	f.Set("refresh_token", refreshToken)
	f.Set("client_id", clientID)
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
