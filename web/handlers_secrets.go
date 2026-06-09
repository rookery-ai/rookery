package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
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
	name := c.FormValue("name")
	// Value and master password handled after Phase 2 secrets service.
	// For now just validate name.
	if name == "" {
		p := s.page(c, "Secrets")
		p.Error = "Secret name is required"
		names, _ := s.db.ListSecretNames(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/secrets.html", &secretsPageData{
			pageData:    p,
			SecretNames: names,
		})
	}

	s.audit.Log(u.ID, "create_secret", "secret:"+name, "", c.RealIP())

	p := s.page(c, "Secrets")
	p.Success = "Secret '" + name + "' saved"
	names, _ := s.db.ListSecretNames(u.ID)
	return c.Render(http.StatusOK, "dashboard/secrets.html", &secretsPageData{
		pageData:    p,
		SecretNames: names,
	})
}

func (s *Server) handleDeleteSecret(c echo.Context) error {
	u := c.Get("user").(*db.User)
	name := c.Param("name")

	if err := s.db.DeleteSecret(u.ID, name); err != nil && err != db.ErrNotFound {
		return err
	}

	s.audit.Log(u.ID, "delete_secret", "secret:"+name, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/secrets")
}
