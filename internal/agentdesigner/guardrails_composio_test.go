package agentdesigner

import "testing"

// TestComposioGuardrails covers the v1/wrong-host rejection that makes generated
// agents fail at runtime (the bug: code hit api.composio.dev/api/v1, HTTP 410).
func TestComposioGuardrails(t *testing.T) {
	cases := []struct {
		name      string
		code      string
		wantError bool
	}{
		{
			name:      "wrong host api.composio.dev",
			code:      "COMPOSIO_BASE_URL = 'https://api.composio.dev/api'\n# composio helper",
			wantError: true,
		},
		{
			name:      "v1 endpoint with composio reference",
			code:      "url = base + '/v1/connections'  # composio\nrequests.get(url)",
			wantError: true,
		},
		{
			name:      "v2 endpoint with composio reference",
			code:      "# composio\nr = requests.post('https://backend.composio.dev/api/v2/execute')",
			wantError: true,
		},
		{
			name:      "correct v3 usage passes",
			code:      "COMPOSIO_BASE = 'https://backend.composio.dev/api/v3'\nrequests.get(COMPOSIO_BASE + '/connected_accounts')  # composio",
			wantError: false,
		},
		{
			name:      "non-composio v1 API is not flagged",
			code:      "requests.get('https://api.example.com/api/v1/users')",
			wantError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckEthics(tc.code, "")
			if tc.wantError && err == nil {
				t.Errorf("expected rejection, got nil")
			}
			if !tc.wantError && err != nil {
				t.Errorf("expected pass, got error: %v", err)
			}
		})
	}
}
