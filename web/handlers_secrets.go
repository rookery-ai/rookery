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

type secretsPageData struct {
	*pageData
	SecretNames []string
	Unlocked    bool
}

func (s *Server) showSecrets(c echo.Context) error {
	u := c.Get("user").(*db.User)
	names, _ := s.db.ListSecretNames(u.ID)
	return c.Render(http.StatusOK, "dashboard/secrets.html", &secretsPageData{
		pageData:    s.page(c, "Secrets"),
		SecretNames: names,
	})
}

func (s *Server) handleCreateSecret(c echo.Context) error {
	u := c.Get("user").(*db.User)
	name := strings.TrimSpace(c.FormValue("name"))
	value := strings.TrimSpace(c.FormValue("value"))

	names, _ := s.db.ListSecretNames(u.ID)
	renderErr := func(status int, msg string) error {
		p := s.page(c, "Secrets")
		p.Error = msg
		return c.Render(status, "dashboard/secrets.html", &secretsPageData{
			pageData:    p,
			SecretNames: names,
		})
	}

	if name == "" {
		return renderErr(http.StatusBadRequest, "Secret name is required")
	}
	if value == "" {
		return renderErr(http.StatusBadRequest, "Secret value is required")
	}
	if u.SecretsSalt == "" || u.EncryptedMasterPassword == "" {
		return renderErr(http.StatusBadRequest, "Complete account setup before managing secrets")
	}

	// Decrypt the stored master password — no need for the user to re-enter it when adding.
	masterPw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return renderErr(http.StatusInternalServerError, "Could not decrypt master password — re-run account setup")
	}

	svc := secrets.New(s.db, u.ID, masterPw, u.SecretsSalt)
	if err := svc.Set(context.Background(), name, value); err != nil {
		return renderErr(http.StatusInternalServerError, "Failed to save secret: "+err.Error())
	}

	s.audit.Log(u.ID, "create_secret", "secret:"+name, "", c.RealIP())

	names, _ = s.db.ListSecretNames(u.ID)
	p := s.page(c, "Secrets")
	p.Success = "Secret '" + name + "' saved"
	return c.Render(http.StatusOK, "dashboard/secrets.html", &secretsPageData{
		pageData:    p,
		SecretNames: names,
		Unlocked:    true,
	})
}

func (s *Server) handleDeleteSecret(c echo.Context) error {
	u := c.Get("user").(*db.User)
	name := c.Param("name")
	masterPw := c.FormValue("master_password")

	names, _ := s.db.ListSecretNames(u.ID)
	renderErr := func(status int, msg string) error {
		p := s.page(c, "Secrets")
		p.Error = msg
		return c.Render(status, "dashboard/secrets.html", &secretsPageData{
			pageData:    p,
			SecretNames: names,
		})
	}

	if masterPw == "" {
		return renderErr(http.StatusBadRequest, "Master password is required to delete a secret")
	}
	if u.SecretsSalt == "" {
		return renderErr(http.StatusBadRequest, "Account setup incomplete")
	}

	// Verify the master password by attempting to decrypt the secret.
	svc := secrets.New(s.db, u.ID, masterPw, u.SecretsSalt)
	if _, err := svc.Get(context.Background(), name); err != nil {
		if errors.Is(err, secrets.ErrWrongPassword) {
			return renderErr(http.StatusUnauthorized, "Wrong master password")
		}
		if !errors.Is(err, db.ErrNotFound) {
			return renderErr(http.StatusInternalServerError, "Verification failed: "+err.Error())
		}
		// Secret not found — let the delete proceed (idempotent).
	}

	if err := s.db.DeleteSecret(u.ID, name); err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}

	s.audit.Log(u.ID, "delete_secret", "secret:"+name, "", c.RealIP())

	names, _ = s.db.ListSecretNames(u.ID)
	p := s.page(c, "Secrets")
	p.Success = "Secret '" + name + "' deleted"
	return c.Render(http.StatusOK, "dashboard/secrets.html", &secretsPageData{
		pageData:    p,
		SecretNames: names,
	})
}
