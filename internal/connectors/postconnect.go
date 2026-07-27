package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ParseExtra decodes a service_connections.extra JSON string into a key→value map.
// Returns nil for empty/invalid input (the templates just resolve {{conn.*}} to "").
func ParseExtra(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

// PostConnectResult is what a post-connect hook resolves at OAuth callback time.
type PostConnectResult struct {
	// Extra is persisted to service_connections.extra and exposed to request templates
	// as {{conn.<key>}}. Non-secret identifiers only.
	Extra map[string]string
	// AccessToken, when non-empty, REPLACES the token stored for this connection.
	//
	// Facebook needs this: publishing to a Page requires that Page's own access token,
	// not the user token the OAuth flow returns. Storing the Page token as the
	// connection's token keeps the secret in the already-encrypted column and makes a
	// connection mean "this Page" — rather than putting a credential in `extra`, which
	// is plaintext.
	AccessToken string
}

// RunPostConnect executes a provider's named post-connect resolution hook once at OAuth
// callback time. Returns a zero result for an unknown/empty hook.
func RunPostConnect(ctx context.Context, name string, client *http.Client, accessToken string) (PostConnectResult, error) {
	switch name {
	case "":
		return PostConnectResult{}, nil
	case "atlassian_cloudid":
		extra, err := resolveAtlassianCloudID(ctx, client, accessToken)
		return PostConnectResult{Extra: extra}, err
	case "meta_page_token":
		return resolveMetaPageToken(ctx, client, accessToken)
	default:
		return PostConnectResult{}, fmt.Errorf("unknown post_connect hook %q", name)
	}
}

// resolveMetaPageToken swaps a user token for the first managed Page's own token.
//
// One connection maps to ONE Page — the first returned. For a single-owner install that
// is the normal case; managing several Pages means connecting several times. The
// alternative (one connection holding a user token plus a map of per-Page tokens) would
// put credentials in `extra`, which is plaintext.
func resolveMetaPageToken(ctx context.Context, client *http.Client, userToken string) (PostConnectResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, _ := http.NewRequestWithContext(ctx, "GET",
		"https://graph.facebook.com/v21.0/me/accounts?fields=id,name,access_token", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	resp, err := client.Do(req)
	if err != nil {
		return PostConnectResult{}, &ConnectorError{KindNetwork, err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return PostConnectResult{}, &ConnectorError{KindAuth,
			fmt.Sprintf("me/accounts %d: %s", resp.StatusCode, string(b))}
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return PostConnectResult{}, err
	}
	if len(out.Data) == 0 {
		return PostConnectResult{}, &ConnectorError{KindOther,
			"this Facebook account manages no Pages — create or get admin access to a Page first"}
	}
	pg := out.Data[0]
	if pg.AccessToken == "" {
		// Without the Page token every publish would 403 with a permissions error that
		// does not mention tokens, so fail loudly at connect instead.
		return PostConnectResult{}, &ConnectorError{KindAuth,
			"no Page access token returned — the app is missing pages_show_list or pages_manage_posts"}
	}
	return PostConnectResult{
		Extra:       map[string]string{"page_id": pg.ID, "page_name": pg.Name},
		AccessToken: pg.AccessToken,
	}, nil
}

// resolveAtlassianCloudID fetches the first accessible Atlassian site's cloud id, which the
// Jira REST base URL (https://api.atlassian.com/ex/jira/{cloudid}/...) requires.
func resolveAtlassianCloudID(ctx context.Context, client *http.Client, accessToken string) (map[string]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.atlassian.com/oauth/token/accessible-resources", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ConnectorError{KindNetwork, err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, &ConnectorError{KindAuth, fmt.Sprintf("accessible-resources %d: %s", resp.StatusCode, string(b))}
	}
	var sites []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &sites); err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return nil, &ConnectorError{KindOther, "no accessible Atlassian sites for this account"}
	}
	return map[string]string{"cloudid": sites[0].ID}, nil
}
