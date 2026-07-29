package web

import (
	"errors"
	"strings"
	"testing"
)

func TestExplainOAuthError(t *testing.T) {
	uri := "https://agents.example.com/dashboard/connectors/services/callback/google"
	cases := []struct {
		name     string
		err      error
		contains []string
	}{
		{
			"mismatch names the exact URI to register",
			errors.New(`oauth: token exchange failed: {"error":"redirect_uri_mismatch"}`),
			[]string{"Google", "does not match", uri},
		},
		{
			"invalid client points at credentials",
			errors.New(`{"error":"invalid_client","error_description":"bad secret"}`),
			[]string{"Client ID", "Client Secret"},
		},
		{
			"invalid scope points at enabled APIs",
			errors.New(`{"error":"invalid_scope"}`),
			[]string{"permissions"},
		},
		{
			"unknown error is preserved, not swallowed",
			errors.New("connection reset by peer"),
			[]string{"connection reset by peer"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := explainOAuthError("Google", uri, tc.err)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("message %q missing %q", got, want)
				}
			}
		})
	}
}

func TestExplainOAuthErrorNilIsEmpty(t *testing.T) {
	if got := explainOAuthError("Google", "https://x/cb", nil); got != "" {
		t.Fatalf("nil error should explain to empty string, got %q", got)
	}
}
