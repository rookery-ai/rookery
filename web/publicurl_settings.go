package web

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ilijad1/simple-agents/internal/publicurl"
	"github.com/labstack/echo/v4"
)

// echoNonce is one outstanding self-test challenge. Bounded by the 30s TTL and
// by the fact that only the owner can mint one.
type echoNonce struct{ expires time.Time }

// handleEchoNonce answers the "Test this URL" probe.
//
// Unauthenticated by necessity: the probe is a server-to-server fetch that
// carries no session cookie, so an authenticated endpoint would fail identically
// whether the URL was right or wrong — inverting the signal the test exists to
// give. Safe because it is not an oracle: it echoes only a nonce this process
// issued, once, within 30 seconds, and 404s otherwise. It reveals no
// configuration.
func (s *Server) handleEchoNonce(c echo.Context) error {
	tok := c.QueryParam("token")
	s.echoMu.Lock()
	n, ok := s.echoNonces[tok]
	delete(s.echoNonces, tok) // single use
	s.echoMu.Unlock()
	if !ok || time.Now().After(n.expires) {
		return c.NoContent(http.StatusNotFound)
	}
	return c.JSON(http.StatusOK, map[string]string{"token": tok})
}

// apiSavePublicURL persists the instance's public base URL.
//
// This is a narrow endpoint rather than a revived general admin-settings PUT:
// that PUT was deliberately deleted because its fields persisted into
// system_settings and nothing ever read them back. This value IS read back, on
// every consent URL and every preflight, via publicurl.Resolve.
func (s *Server) apiSavePublicURL(c echo.Context) error {
	var req struct {
		URL string `json:"url"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	// An empty value clears the setting and returns the instance to detection.
	if req.URL == "" {
		if err := s.db.SetSystemSetting(publicurl.SettingKey, ""); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
		return c.JSON(http.StatusOK, s.publicURLState(c))
	}
	n, err := publicurl.Normalize(req.URL)
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_public_url",
			"Enter a full URL including the scheme and no path, for example https://agents.example.com")
	}
	if err := s.db.SetSystemSetting(publicurl.SettingKey, n); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	return c.JSON(http.StatusOK, s.publicURLState(c))
}

// apiPublicURLState reports the configured value, what is actually in use, and
// where that came from.
func (s *Server) apiPublicURLState(c echo.Context) error {
	return c.JSON(http.StatusOK, s.publicURLState(c))
}

func (s *Server) publicURLState(c echo.Context) map[string]string {
	stored, _ := s.db.GetSystemSetting(publicurl.SettingKey)
	actual, src := s.resolvePublicURL(c)
	label := map[publicurl.Source]string{
		publicurl.SourceConfigured: "configured",
		publicurl.SourceEnv:        "env",
		publicurl.SourceDetected:   "detected",
	}[src]
	return map[string]string{
		"public_url":        stored,
		"public_url_actual": actual,
		"public_url_source": label,
	}
}

// apiTestPublicURL fetches the candidate URL's echo endpoint and asserts the
// nonce comes back, proving the URL reaches THIS process — not merely that
// something answered. A typo, a wrong port, DNS pointing elsewhere and a proxy
// aimed at another instance all fail this check; a plain reachability probe
// would catch only the first two.
func (s *Server) apiTestPublicURL(c echo.Context) error {
	var req struct {
		URL string `json:"url"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	base, err := publicurl.Normalize(req.URL)
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_public_url",
			"Enter a full URL including the scheme, for example https://agents.example.com")
	}

	// crypto/rand, NOT math/rand — this nonce is the endpoint's only access
	// control.
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	tok := hex.EncodeToString(raw)
	now := time.Now()
	s.echoMu.Lock()
	if s.echoNonces == nil {
		s.echoNonces = map[string]echoNonce{}
	}
	// Sweep on mint. Only handleEchoNonce deletes, and it is only reached when
	// the probe SUCCEEDS — so without this every failed self-test would leave an
	// entry behind forever, growing the map without bound.
	for k, v := range s.echoNonces {
		if now.After(v.expires) {
			delete(s.echoNonces, k)
		}
	}
	s.echoNonces[tok] = echoNonce{expires: now.Add(30 * time.Second)}
	s.echoMu.Unlock()

	// DELIBERATE EXCEPTION: internal/nethttp.GuardedClient blocks loopback and
	// RFC1918 by design, and dialling ourselves is exactly the point of this
	// check. Do not "fix" this to use the guarded client — it would make the
	// self-test fail on every self-hosted install, which is all of them.
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 6*time.Second)
	defer cancel()
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz/echo?token="+tok, nil)
	resp, err := client.Do(hreq)
	if err != nil {
		// A certificate the SERVER does not trust is a third outcome, not a
		// failure. Verified empirically: against a Caddy internal-CA host this
		// succeeds when `caddy trust` has put the root in the system pool and
		// fails when it has not — so it is install-dependent, while the BROWSER
		// (the only party in an OAuth redirect that actually loads this URL) may
		// trust it either way. Reporting "unreachable" for a working setup is
		// worse than having no button at all.
		var uaErr x509.UnknownAuthorityError
		var hostErr x509.HostnameError
		if errors.As(err, &uaErr) || errors.As(err, &hostErr) {
			return c.JSON(http.StatusOK, map[string]any{"ok": true, "warning": true,
				"error": "Reached " + base + ", but this server does not trust its certificate. " +
					"That is fine for OAuth as long as your browser trusts it — the provider " +
					"never connects to this server, it only redirects your browser."})
		}
		return c.JSON(http.StatusOK, map[string]any{"ok": false,
			"error": "Could not reach " + base + " from the server: " + err.Error() +
				". If your network has no NAT hairpin, the server may be unable to reach its own " +
				"public name even though browsers can — check from a browser before changing anything."})
	}
	defer resp.Body.Close()
	var got struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&got)
	if resp.StatusCode != http.StatusOK || got.Token != tok {
		return c.JSON(http.StatusOK, map[string]any{"ok": false,
			"error": base + " answered, but it is not this instance. Check the address, the port, and any reverse proxy."})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}
