package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/prompts"
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

// validateAssistRequest checks the request against the closed action set, the
// empty-selection guard, and the size cap — everything that can be decided
// without a coder call. It is a pure function, deliberately split out of the
// handler, so the cap's reject-not-truncate boundary (the property that
// matters most here: an off-by-one would silently rewrite text the user never
// selected) can be asserted directly without spending a real coder call — the
// handler's `path` check is excluded because it needs the vault and isn't part
// of that boundary.
func validateAssistRequest(req apiKBAssistRequest) (code, msg string, ok bool) {
	if !validAssistAction(req.Action) {
		return "invalid_request", "unknown action: " + req.Action, false
	}
	if strings.TrimSpace(req.Selection) == "" {
		return "invalid_request", "select some text first", false
	}
	if len(req.Selection) > maxAssistSelectionBytes {
		return "invalid_request", "that selection is too long — select a smaller passage", false
	}
	return "", "", true
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
	if code, msg, ok := validateAssistRequest(req); !ok {
		return jsonErr(c, http.StatusBadRequest, code, msg)
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
		// Defence in depth. prompts.APIEngineTextKickoffMessage is the actual fix
		// for the stray [CHAT] the panel used to show — the API engine was telling
		// every one-shot call to emit the agent output protocol — but a prompt
		// steers and does not guarantee, and a weak model will re-emit a marker it
		// has seen a thousand times. This is what makes this endpoint's contract
		// ("the result is a replacement passage") true rather than merely intended.
		Result: prompts.StripProtocolMarkers(result.Text),
	})
}
