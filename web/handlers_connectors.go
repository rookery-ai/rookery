package web

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type connectorsPageData struct {
	*pageData
	Connections []*db.PlatformConnection
	Platforms   []string
}

var supportedPlatforms = []string{"telegram", "discord"}

func (s *Server) showConnectors(c echo.Context) error {
	u := c.Get("user").(*db.User)
	var connections []*db.PlatformConnection
	for _, p := range supportedPlatforms {
		if conn, err := s.db.GetPlatformConnection(u.ID, p); err == nil {
			connections = append(connections, conn)
		}
	}
	return c.Render(http.StatusOK, "dashboard/connectors.html", &connectorsPageData{
		pageData:    s.page(c, "Chat Connectors"),
		Connections: connections,
		Platforms:   supportedPlatforms,
	})
}

func (s *Server) handleSaveConnector(c echo.Context) error {
	u := c.Get("user").(*db.User)
	platform := c.FormValue("platform")
	token := c.FormValue("token")

	if token == "" || platform == "" {
		p := s.page(c, "Chat Connectors")
		p.Error = "Platform and token are required"
		return s.renderConnectors(c, u, p)
	}

	// Token encryption happens in Phase 2 with secrets service.
	// For now store as-is with a marker prefix.
	conn := &db.PlatformConnection{
		ID:             uuid.New().String(),
		UserID:         u.ID,
		Platform:       platform,
		EncryptedToken: fmt.Sprintf("__plain__%s", token),
		Active:         true,
	}

	if err := s.db.UpsertPlatformConnection(conn); err != nil {
		p := s.page(c, "Chat Connectors")
		p.Error = "Failed to save connector: " + err.Error()
		return s.renderConnectors(c, u, p)
	}

	// Link platform identity (user registered their own bot, so they ARE the user)
	// Real linking happens when the user sends their first message to the bot.
	s.audit.Log(u.ID, "connect_platform", "platform:"+platform, "", c.RealIP())

	p := s.page(c, "Chat Connectors")
	p.Success = "Connected to " + platform + " successfully!"
	return s.renderConnectors(c, u, p)
}

func (s *Server) handleDeleteConnector(c echo.Context) error {
	u := c.Get("user").(*db.User)
	platform := c.Param("platform")

	if err := s.db.DeletePlatformConnection(u.ID, platform); err != nil {
		return err
	}

	s.audit.Log(u.ID, "disconnect_platform", "platform:"+platform, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/connectors")
}

func (s *Server) handleTestConnector(c echo.Context) error {
	// In Phase 3 this will do a real bot.Me() ping.
	// For now return a placeholder JSON response.
	platform := c.Param("platform")
	return c.JSON(http.StatusOK, map[string]string{
		"status":   "ok",
		"platform": platform,
		"message":  "Connection test will be available after gateway integration",
	})
}

func (s *Server) renderConnectors(c echo.Context, u *db.User, p *pageData) error {
	var connections []*db.PlatformConnection
	for _, pl := range supportedPlatforms {
		if conn, err := s.db.GetPlatformConnection(u.ID, pl); err == nil {
			connections = append(connections, conn)
		}
	}
	return c.Render(http.StatusOK, "dashboard/connectors.html", &connectorsPageData{
		pageData:    p,
		Connections: connections,
		Platforms:   supportedPlatforms,
	})
}
