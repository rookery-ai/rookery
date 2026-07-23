package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/websearch"
	"github.com/labstack/echo/v4"
)

// registerSearchKeysAPI registers the JSON search-API-key endpoints on the
// given group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI). These are a thin, dedicated wrapper over the
// secrets service: a workspace-wide Brave/Tavily key upgrades web search
// (chat, agents, skills) from keyless scraping to a real API — see
// internal/websearch.KeySecretNames/KeyedProvider. Kept separate from the
// generic /secrets CRUD so the SEARCH_KEY_<PROVIDER> naming convention stays
// out of the frontend and GET can report configured-state per provider
// without the caller needing to know the secret name.
func (s *Server) registerSearchKeysAPI(g *echo.Group) {
	g.GET("/search-keys", s.apiGetSearchKeys)
	g.PUT("/search-keys", s.apiPutSearchKey)
	g.DELETE("/search-keys/:provider", s.apiDeleteSearchKey)
}

// searchKeyProviders maps the user-facing provider name to its secret name.
// The only two supported today; unknown names are rejected.
var searchKeyProviders = map[string]string{
	"brave":  "SEARCH_KEY_BRAVE",
	"tavily": "SEARCH_KEY_TAVILY",
}

func init() {
	// Guard against searchKeyProviders drifting from websearch.KeySecretNames
	// (the actual source of truth) — a mismatch here would silently store a
	// secret name searchProviders() never reads.
	want := map[string]bool{}
	for _, n := range websearch.KeySecretNames() {
		want[n] = true
	}
	if len(want) != len(searchKeyProviders) {
		panic("web: searchKeyProviders out of sync with websearch.KeySecretNames")
	}
	for _, n := range searchKeyProviders {
		if !want[n] {
			panic("web: searchKeyProviders out of sync with websearch.KeySecretNames")
		}
	}
}

type apiSearchKeysResponse struct {
	Brave  bool `json:"brave"`
	Tavily bool `json:"tavily"`
}

type apiPutSearchKeyRequest struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
}

// apiGetSearchKeys reports which search API keys are configured for the
// workspace. Never returns a key value — configured state only.
// GET /api/v1/search-keys → 200 {"brave":bool,"tavily":bool}
func (s *Server) apiGetSearchKeys(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	names, err := s.db.ListSecretNames(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to list secrets")
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	return c.JSON(http.StatusOK, apiSearchKeysResponse{
		Brave:  have[searchKeyProviders["brave"]],
		Tavily: have[searchKeyProviders["tavily"]],
	})
}

// apiPutSearchKey stores (or replaces) a search API key for one provider.
// PUT /api/v1/search-keys {provider,key} → 200 {"ok":true}
func (s *Server) apiPutSearchKey(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req apiPutSearchKeyRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	secretName, ok := searchKeyProviders[req.Provider]
	if !ok {
		return jsonErr(c, http.StatusBadRequest, "invalid_provider", "provider must be brave or tavily")
	}
	// Trim before storing: an untrimmed value (a pasted "key\n" or " key ") would
	// be sent verbatim in the X-Subscription-Token / Bearer header and rejected by
	// the provider. Matches apiCreateSecret, which trims too.
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "key is required")
	}
	if u.SecretsSalt == "" || u.EncryptedMasterPassword == "" {
		return jsonErr(c, http.StatusBadRequest, "setup_incomplete", "complete account setup before managing secrets")
	}

	// Decrypt the stored master password (exactly as apiCreateSecret does — no
	// master password required in the request; the server already holds it,
	// encrypted under the system key, for headless/cron use).
	masterPw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not decrypt master password")
	}

	svc := secrets.New(s.db, u.ID, masterPw, u.SecretsSalt)
	if err := svc.Set(context.Background(), secretName, key); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save key")
	}

	s.audit.Log(u.ID, "set_search_key", "search_key:"+req.Provider, "", c.RealIP())

	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiDeleteSearchKey clears a provider's search API key, reverting that
// provider to the keyless cascade. Idempotent — deleting an unconfigured
// provider still returns 200.
// DELETE /api/v1/search-keys/:provider → 200 {"ok":true}
func (s *Server) apiDeleteSearchKey(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")

	secretName, ok := searchKeyProviders[provider]
	if !ok {
		return jsonErr(c, http.StatusBadRequest, "invalid_provider", "provider must be brave or tavily")
	}

	if err := s.db.DeleteSecret(u.ID, secretName); err != nil && !errors.Is(err, db.ErrNotFound) {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to delete key")
	}

	s.audit.Log(u.ID, "delete_search_key", "search_key:"+provider, "", c.RealIP())

	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}
