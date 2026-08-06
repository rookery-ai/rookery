package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ilijad1/rookery/internal/agentrunner"
	"github.com/ilijad1/rookery/internal/coder"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/prompts"
	"github.com/labstack/echo/v4"
)

// maxAssistSelectionBytes caps the passage sent to the model. Deliberately NOT
// internal/iolimit's 25 MiB: that cap governs ingest doors (uploads,
// attachments, the KB bridge), and reusing it here would admit a payload no
// single LLM call should carry. Over-cap is REJECTED rather than truncated — a
// silently shortened passage comes back as a rewrite of something the user
// never selected.
const maxAssistSelectionBytes = 16 << 10 // 16 KiB

type apiKBAssistRequest struct {
	Action    string `json:"action"`
	Path      string `json:"path"`
	Selection string `json:"selection"`
}

type apiKBAssistResponse struct {
	Action string `json:"action"`
	Result string `json:"result"`
}

func validAssistAction(action string) bool {
	for _, a := range prompts.KBAssistActions() {
		if a == action {
			return true
		}
	}
	return false
}

// apiKBAssist runs one text-only coder call over a selected passage.
//
// POST /api/v1/kb/assist {action,path,selection} → 200 {action,result}
func (s *Server) apiKBAssist(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)

	var req apiKBAssistRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if !validAssistAction(req.Action) {
		return jsonErr(c, http.StatusBadRequest, "invalid_request",
			"unknown action: "+req.Action)
	}
	if strings.TrimSpace(req.Selection) == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_request",
			"select some text first")
	}
	if len(req.Selection) > maxAssistSelectionBytes {
		return jsonErr(c, http.StatusBadRequest, "invalid_request",
			"that selection is too long — select a smaller passage")
	}
	// The path is only prompt context, but it still goes through the vault's
	// safety primitive: an endpoint that echoes an unvalidated path into a
	// model prompt is the kind of thing that quietly becomes a read later.
	if _, err := s.vault.Resolve(w.ID, req.Path); err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "invalid note path")
	}

	prompt := prompts.BuildKBAssistPrompt(req.Action, req.Path, req.Selection)
	result, err := s.coderForWorkspace(w.ID).WithNoTools().Generate(c.Request().Context(), w.ID, prompt)
	if err != nil {
		if errors.Is(err, coder.ErrUsageLimit) ||
			errors.Is(err, coder.ErrRateLimited) ||
			errors.Is(err, coder.ErrAPIAuth) {
			// One wording for a quota/auth failure across the whole product:
			// a scheduled run and the note editor must not disagree.
			return jsonErr(c, http.StatusServiceUnavailable, "coder_unavailable",
				agentrunner.FriendlyRunError(err, ""))
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}

	return c.JSON(http.StatusOK, apiKBAssistResponse{
		Action: req.Action,
		Result: strings.TrimSpace(result.Text),
	})
}
