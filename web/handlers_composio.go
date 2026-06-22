package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type composioPageData struct {
	*pageData
	Configured bool
}

func (s *Server) showComposio(c echo.Context) error {
	u := c.Get("user").(*db.User)
	configured, _ := s.db.SecretExists(u.ID, "COMPOSIO_API_KEY")
	return c.Render(http.StatusOK, "dashboard/composio.html", &composioPageData{
		pageData:   s.page(c, "External Services"),
		Configured: configured,
	})
}
