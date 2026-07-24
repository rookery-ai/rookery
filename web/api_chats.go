package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/labstack/echo/v4"
)

// registerChatsAPI registers the JSON chats endpoints on the given group
// (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI). The message-send endpoint re-registers the
// EXISTING s.handleChatMessage handler UNCHANGED — it already returns JSON,
// just with the legacy {"response":...}/{"error":...} shape, per the API
// plan's global constraints (do not touch it).
func (s *Server) registerChatsAPI(g *echo.Group) {
	g.GET("/chats", s.apiListChats)
	g.POST("/chats", s.apiCreateChat)
	g.GET("/chats/:id", s.apiGetChat)
	g.PATCH("/chats/:id", s.apiRenameChat)
	g.POST("/chats/:id/messages", s.handleChatMessage)
	g.POST("/chats/:id/resume", s.apiResumeChat)
	g.POST("/chats/:id/stop", s.apiStopChat)
	g.DELETE("/chats/:id", s.apiDeleteChat)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

// apiChat mirrors db.Chat. db.Chat has no separate UpdatedAt field — LastSeen
// is the closest analog (bumped by TouchChat on every message and by
// ResumeChat), so it's surfaced here as updated_at.
type apiChat struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toAPIChat(ch *db.Chat) apiChat {
	return apiChat{
		ID:        ch.ID,
		Name:      ch.Name,
		Platform:  ch.Platform,
		Active:    ch.Active,
		CreatedAt: ch.CreatedAt,
		UpdatedAt: ch.LastSeen,
	}
}

type apiChatMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func toAPIChatMessage(m db.ChatMessage) apiChatMessage {
	return apiChatMessage{Role: m.Role, Content: m.Content, CreatedAt: m.CreatedAt}
}

type apiCreateChatRequest struct {
	Name string `json:"name"`
}

type apiRenameChatRequest struct {
	Name string `json:"name"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// errChatNotFound is a sentinel signaling the caller should respond 404
// not_found — distinct from db errors so it's never confused with success.
var errChatNotFound = errors.New("chat not found")

// getOwnedChat loads a chat and verifies it belongs to the workspace.
func (s *Server) getOwnedChat(workspaceID, id string) (*db.Chat, error) {
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != workspaceID {
		return nil, errChatNotFound
	}
	return ch, nil
}

// apiListChats ports showChats.
// GET /api/v1/chats → 200 {"chats":[{...}]}
func (s *Server) apiListChats(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	chats, err := s.db.ListChats(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiChat, 0, len(chats))
	for _, ch := range chats {
		out = append(out, toAPIChat(ch))
	}
	return c.JSON(http.StatusOK, map[string]any{"chats": out})
}

// apiCreateChat ports handleCreateChat.
// POST /api/v1/chats {name?} → 201 chat DTO
func (s *Server) apiCreateChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req apiCreateChatRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	name := req.Name
	if name == "" {
		loc := profile.LoadLocation(s.db, u.ID)
		name = "Chat " + time.Now().In(loc).Format("2006-01-02 15:04")
	}
	ch := &db.Chat{
		ID:          uuid.New().String(),
		WorkspaceID: u.ID,
		Name:        name,
		Platform:    "web",
		Active:      true,
	}
	if err := s.db.CreateChat(ch); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(u.ID, "create_chat", "chat:"+ch.ID, name, c.RealIP())
	return c.JSON(http.StatusCreated, toAPIChat(ch))
}

// apiGetChat ports showChatDetail.
// GET /api/v1/chats/:id → 200 {"chat":{...},"messages":[...]}
func (s *Server) apiGetChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	ch, err := s.getOwnedChat(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "chat not found")
	}
	msgs, _ := s.db.ListChatMessages(ch.ID)
	out := make([]apiChatMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toAPIChatMessage(m))
	}
	return c.JSON(http.StatusOK, map[string]any{"chat": toAPIChat(ch), "messages": out})
}

// apiRenameChat sets a chat's user-facing title.
// PATCH /api/v1/chats/:id {name} → 200 chat DTO
func (s *Server) apiRenameChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	ch, err := s.getOwnedChat(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "chat not found")
	}
	var req apiRenameChatRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_name", "a name is required")
	}
	if err := s.db.UpdateChatName(ch.ID, name); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	ch.Name = name
	s.audit.Log(u.ID, "rename_chat", "chat:"+ch.ID, name, c.RealIP())
	return c.JSON(http.StatusOK, toAPIChat(ch))
}

// apiResumeChat ports handleResumeChat.
// POST /api/v1/chats/:id/resume → 200 {"ok":true}
func (s *Server) apiResumeChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	ch, err := s.getOwnedChat(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "chat not found")
	}
	_ = s.db.ResumeChat(ch.ID)
	s.audit.Log(u.ID, "resume_chat", "chat:"+ch.ID, ch.Name, c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiStopChat ports handleStopChat.
// POST /api/v1/chats/:id/stop → 200 {"ok":true}
func (s *Server) apiStopChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	ch, err := s.getOwnedChat(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "chat not found")
	}
	_ = s.db.StopChat(ch.ID)
	s.audit.Log(u.ID, "stop_chat", "chat:"+ch.ID, ch.Name, c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiDeleteChat ports handleDeleteChat.
// DELETE /api/v1/chats/:id → 200 {"ok":true}
func (s *Server) apiDeleteChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	ch, err := s.getOwnedChat(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "chat not found")
	}
	_ = s.db.DeleteChat(ch.ID)
	// The transcript was reflected into the vault when the chat stopped; without
	// this the deleted chat lives on in the KB browser and in search.
	if s.vault != nil {
		_ = s.vault.Reflector().UnreflectChat(u.ID, ch.ID)
	}
	s.audit.Log(u.ID, "delete_chat", "chat:"+ch.ID, ch.Name, c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}
