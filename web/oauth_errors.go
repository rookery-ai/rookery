package web

import "strings"

// explainOAuthError turns a provider's token-exchange failure into a sentence
// that names the remedy.
//
// The raw error was previously concatenated straight into a query string, which
// told a non-technical user nothing actionable. Matching is on substrings of the
// provider's JSON body because the OAuth error codes are standardised even
// though the surrounding envelope is not.
func explainOAuthError(providerLabel, redirectURI string, err error) string {
	if err == nil {
		return ""
	}
	raw := err.Error()
	switch {
	case strings.Contains(raw, "redirect_uri_mismatch"):
		return "The redirect URI registered with " + providerLabel +
			" does not match this instance. Register exactly this URI in the " +
			providerLabel + " console, then try again: " + redirectURI
	case strings.Contains(raw, "invalid_client"), strings.Contains(raw, "unauthorized_client"):
		return "The Client ID or Client Secret for " + providerLabel +
			" is wrong, or belongs to a different app. Re-enter both from the " +
			providerLabel + " console."
	case strings.Contains(raw, "invalid_scope"), strings.Contains(raw, "insufficient_scope"):
		return providerLabel + " rejected the requested permissions. The app may not" +
			" have those APIs enabled — check them in the " + providerLabel + " console."
	case strings.Contains(raw, "invalid_grant"):
		return "The authorization expired before it could be used. Start the connection again."
	default:
		return providerLabel + " refused the connection: " + raw
	}
}
