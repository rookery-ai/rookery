package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// registerSecretsAPI registers the JSON secrets endpoints on the given group
// (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI).
func (s *Server) registerSecretsAPI(g *echo.Group) {
	g.GET("/secrets", s.apiListSecrets)
	g.POST("/secrets", s.apiCreateSecret)
	g.DELETE("/secrets/:name", s.apiDeleteSecret)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiSecretListResponse struct {
	Secrets []apiSecretName `json:"secrets"`
}

type apiSecretName struct {
	Name string `json:"name"`
}

type apiCreateSecretRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type apiDeleteSecretRequest struct {
	MasterPassword string `json:"master_password"`
}

type apiOKResponse struct {
	OK bool `json:"ok"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// apiListSecrets lists all secret names for the workspace (never values).
// GET /api/v1/secrets → 200 {"secrets":[{"name":"..."}]}
func (s *Server) apiListSecrets(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	names, _ := s.db.ListSecretNames(u.ID)

	secrets := []apiSecretName{}
	for _, name := range names {
		secrets = append(secrets, apiSecretName{Name: name})
	}

	return c.JSON(http.StatusOK, apiSecretListResponse{Secrets: secrets})
}

// apiCreateSecret creates a new secret.
// POST /api/v1/secrets {name,value} → 201 {"ok":true}
func (s *Server) apiCreateSecret(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req apiCreateSecretRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	name := strings.TrimSpace(req.Name)
	value := strings.TrimSpace(req.Value)

	// Validation
	if name == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "name is required")
	}
	if value == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "value is required")
	}
	if u.SecretsSalt == "" || u.EncryptedMasterPassword == "" {
		return jsonErr(c, http.StatusBadRequest, "setup_incomplete", "complete account setup before managing secrets")
	}

	// Decrypt the stored master password
	masterPw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not decrypt master password")
	}

	// Create and save the secret
	svc := secrets.New(s.db, u.ID, masterPw, u.SecretsSalt)
	if err := svc.Set(context.Background(), name, value); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save secret")
	}

	// Audit log
	s.audit.Log(u.ID, "create_secret", "secret:"+name, "", c.RealIP())

	return c.JSON(http.StatusCreated, apiOKResponse{OK: true})
}

// apiDeleteSecret deletes a secret (master-password gated).
// DELETE /api/v1/secrets/:name {master_password} → 200 {"ok":true}
func (s *Server) apiDeleteSecret(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	name := c.Param("name")

	var req apiDeleteSecretRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	masterPw := strings.TrimSpace(req.MasterPassword)

	// Validation
	if masterPw == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "master_password is required")
	}
	if u.SecretsSalt == "" {
		return jsonErr(c, http.StatusBadRequest, "setup_incomplete", "account setup incomplete")
	}

	// Verify the master password by attempting to decrypt the secret
	svc := secrets.New(s.db, u.ID, masterPw, u.SecretsSalt)
	if _, err := svc.Get(context.Background(), name); err != nil {
		if errors.Is(err, secrets.ErrWrongPassword) {
			return jsonErr(c, http.StatusUnauthorized, "wrong_master_password", "wrong master password")
		}
		if !errors.Is(err, db.ErrNotFound) {
			return jsonErr(c, http.StatusInternalServerError, "internal", "verification failed")
		}
		// Secret not found — let the delete proceed (idempotent)
	}

	// Delete the secret
	if err := s.db.DeleteSecret(u.ID, name); err != nil && !errors.Is(err, db.ErrNotFound) {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to delete secret")
	}

	// Audit log
	s.audit.Log(u.ID, "delete_secret", "secret:"+name, "", c.RealIP())

	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}
