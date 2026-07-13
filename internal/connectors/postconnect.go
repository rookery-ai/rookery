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

// RunPostConnect executes a provider's named post-connect resolution hook once at OAuth
// callback time and returns key→value pairs to persist in service_connections.extra
// (exposed to request templates as {{conn.<key>}}). Returns nil for an unknown/empty hook.
func RunPostConnect(ctx context.Context, name string, client *http.Client, accessToken string) (map[string]string, error) {
	switch name {
	case "":
		return nil, nil
	case "atlassian_cloudid":
		return resolveAtlassianCloudID(ctx, client, accessToken)
	default:
		return nil, fmt.Errorf("unknown post_connect hook %q", name)
	}
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
