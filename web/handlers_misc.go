package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/chat"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// ── Chats ───────────────────────────────────────────────────────────────────

type chatsPageData struct {
	*pageData
	Chats []*db.Chat
}

func (s *Server) showChats(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	chats, _ := s.db.ListChats(u.ID)
	return c.Render(http.StatusOK, "dashboard/chats.html", &chatsPageData{
		pageData: s.page(c, "Chats"),
		Chats:    chats,
	})
}

func (s *Server) handleCreateChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	name := c.FormValue("name")
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
		return err
	}
	s.audit.Log(u.ID, "create_chat", "chat:"+ch.ID, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats/"+ch.ID)
}

type chatDetailPageData struct {
	*pageData
	Chat     *db.Chat
	Messages []db.ChatMessage
}

// showChatDetail renders a chat's full message history plus a composer.
func (s *Server) showChatDetail(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	msgs, _ := s.db.ListChatMessages(id)
	return c.Render(http.StatusOK, "dashboard/chat_detail.html", &chatDetailPageData{
		pageData: s.page(c, "Chat"),
		Chat:     ch,
		Messages: msgs,
	})
}

// handleChatMessage sends one user message through the coder one-off-chat path,
// persists both turns, and returns the assistant reply as JSON. Used by the
// chat detail page's AJAX composer (mirrors the agent-designer chat flow).
func (s *Server) handleChatMessage(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "chat not found"})
	}

	// Accept JSON {message} (AJAX composer) or a form-encoded "message" field.
	var text string
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(c.Request().Body).Decode(&body)
		text = strings.TrimSpace(body.Message)
	} else {
		text = strings.TrimSpace(c.FormValue("message"))
	}
	if text == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "empty message"})
	}

	history, _ := s.db.ListChatMessages(id)

	// System context: a read+write knowledge-base instruction (so the chat can retrieve
	// and edit notes on demand) + the user's always-on identity context (profile/memory/
	// agents/MCP). The coder runs with its CWD set to the vault root, which the sandbox
	// grants read+write access to, and the file toolset Read/Write/Edit/Glob/Grep (for a
	// CLI coder) or read_file/write_file/edit_file/list_dir tool calls (for an API coder).
	root := s.vault.Root(u.ID)
	coder := s.coderForWorkspace(u.ID).WithDir(root)
	if !coder.IsAPI() {
		// WithAllowedTools pre-approves the CLI subprocess's tool set so it never
		// blocks on an interactive permission prompt; it's meaningless for an API
		// coder (host tools are offered via native function-calling, not gated by
		// this flag), so skip it there — matches the Telegram chat path.
		coder = coder.WithAllowedTools("Read,Write,Edit,Glob,Grep")
	}

	// Connector wiring: the API engine exposes bound connections as native
	// in-process tools directly. A CLI coder instead reaches them via the loopback
	// bridge (`simple-agents connector exec <tool>`), the same mechanism agent runs use.
	var connRefs []prompts.ConnectionRef
	var connTools []string
	var connBin string
	if coder.IsAPI() && s.connStore != nil {
		if rows, err := s.db.ListServiceConnections(c.Request().Context(), u.ID); err == nil {
			bound := connectors.ActiveBoundConns(rows)
			if len(bound) > 0 {
				coder = coder.WithConnectors(s.connectors, s.connStore, bound)
				for _, b := range bound {
					connRefs = append(connRefs, prompts.ConnectionRef{Provider: b.Provider, Label: b.AccountLabel, Identity: b.AccountIdentity})
				}
				for _, d := range s.connectors.ToolDefs(bound) {
					connTools = append(connTools, d.Name)
				}
			}
		}
	} else if !coder.IsAPI() && s.connBridge != nil && s.connBridge.Addr() != "" {
		if rows, err := s.db.ListServiceConnections(c.Request().Context(), u.ID); err == nil {
			bound := connectors.ActiveBoundConns(rows)
			if len(bound) > 0 {
				tok := s.connBridge.Register(u.ID, bound, false)
				defer s.connBridge.Unregister(tok)
				coder = coder.WithExtraEnv(map[string]string{
					"SA_CONNECTOR_URL":   s.connBridge.Addr(),
					"SA_CONNECTOR_TOKEN": tok,
				})
				for _, b := range bound {
					connRefs = append(connRefs, prompts.ConnectionRef{Provider: b.Provider, Label: b.AccountLabel, Identity: b.AccountIdentity})
				}
				for _, d := range s.connectors.ToolDefs(bound) {
					connTools = append(connTools, d.Name)
				}
				if p, err := os.Executable(); err == nil {
					connBin = p
				}
				if connBin != "" {
					// A CLI coder reaches connectors by running `<bin> connector exec …` as a
					// shell command, so grant a NARROWLY-SCOPED Bash permission for only that
					// command — chat stays file-only (no arbitrary shell) otherwise.
					coder = coder.WithAllowedTools("Read,Write,Edit,Glob,Grep,Bash(" + connBin + " connector exec:*)")
				}
			}
		}
	}
	sysCtx := prompts.BuildChatSystemPrompt(root, coder.BackendType(), connRefs, connTools, connBin) + chat.BuildUserContext(s.db, s.memory, u.ID)

	// Re-activate the chat if it had been stopped, so history keeps flowing.
	if !ch.Active {
		_ = s.db.ResumeChat(id)
	}

	result, err := coder.Chat(c.Request().Context(), u.ID, history, sysCtx, text)
	if err != nil {
		// Don't persist on failure — the client already shows the user bubble,
		// and a refresh clears the failed attempt (matches agent-designer behavior).
		return c.JSON(http.StatusOK, map[string]string{"error": "Couldn't reach " + coder.Name() + ": " + err.Error()})
	}

	_ = s.db.AddChatMessage(id, "user", text)
	_ = s.db.AddChatMessage(id, "assistant", result.Text)
	_ = s.db.TouchChat(id)
	return c.JSON(http.StatusOK, map[string]string{"response": result.Text})
}

// handleResumeChat re-activates a previously stopped chat.
func (s *Server) handleResumeChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	_ = s.db.ResumeChat(id)
	s.audit.Log(u.ID, "resume_chat", "chat:"+id, ch.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats/"+id)
}

func (s *Server) handleStopChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	_ = s.db.StopChat(id)
	s.audit.Log(u.ID, "stop_chat", "chat:"+id, ch.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats")
}

func (s *Server) handleDeleteChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	_ = s.db.DeleteChat(id)
	s.audit.Log(u.ID, "delete_chat", "chat:"+id, ch.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats")
}

// ── Reminders ──────────────────────────────────────────────────────────────

type remindersPageData struct {
	*pageData
	Reminders []*db.Reminder
}

func (s *Server) showReminders(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	reminders, _ := s.db.ListReminders(u.ID)
	return c.Render(http.StatusOK, "dashboard/reminders.html", &remindersPageData{
		pageData:  s.page(c, "Reminders"),
		Reminders: reminders,
	})
}

func (s *Server) handleCreateReminder(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	message := c.FormValue("message")
	whenStr := strings.TrimSpace(c.FormValue("when"))

	p := s.page(c, "Reminders")
	renderErr := func(msg string) error {
		p.Error = msg
		rs, _ := s.db.ListReminders(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/reminders.html", &remindersPageData{pageData: p, Reminders: rs})
	}

	if message == "" {
		return renderErr("Reminder message is required")
	}
	if whenStr == "" {
		return renderErr(`When would you like to be reminded? Try: "in 10 minutes", "tomorrow at 3pm", "next Friday morning"`)
	}

	now := time.Now()
	loc := profile.LoadLocation(s.db, u.ID)
	llmFn := buildLLMTimeParser(s.coderForWorkspace(u.ID))

	remindAt, _, err := reminder.ParseNaturalTimeFull(c.Request().Context(), whenStr, now, loc, llmFn, u.ID)
	if err != nil {
		return renderErr(`Couldn't understand that time. Try: "in 10 minutes", "tomorrow at 3pm", "next Tuesday", "July 15 at 2pm"`)
	}
	if remindAt.IsZero() {
		return renderErr(`No time found in "` + whenStr + `". Try: "in 10 minutes", "tomorrow at 3pm", "next Friday"`)
	}

	r := &db.Reminder{
		ID:          uuid.New().String(),
		WorkspaceID: u.ID,
		Message:     message,
		RemindAt:    remindAt,
	}
	_ = s.db.CreateReminder(r)
	s.audit.Log(u.ID, "create_reminder", "reminder:"+r.ID, message, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/reminders")
}

// buildLLMTimeParser returns a reminder.TimeParserFunc backed by the given coder.
// It calls BuildReminderParsePrompt and parses the JSON response via ParseLLMReminderJSON.
func buildLLMTimeParser(coderSvc *coder.Coder) reminder.TimeParserFunc {
	if coderSvc == nil {
		return nil
	}
	return func(ctx context.Context, workspaceID, input string, now time.Time, loc *time.Location) (time.Time, string, error) {
		tz := "UTC"
		if loc != nil {
			tz = loc.String()
		}
		nowStr := now.In(loc).Format("2006-01-02 15:04 MST")
		prompt := prompts.BuildReminderParsePrompt(input, nowStr, tz)
		result, err := coderSvc.WithNoTools().Generate(ctx, workspaceID, prompt)
		if err != nil {
			return time.Time{}, input, err
		}
		when, msg, err := reminder.ParseLLMReminderJSON(result.Text, now)
		return when, msg, err
	}
}

// handlePollReminders returns due unsent reminders for the current user.
// For web-only users (no platform connected) it also marks them sent — this IS the delivery.
// For users with Telegram connected, it returns them for info display but does NOT mark sent
// so the server-side tick() can still deliver via Telegram.
func (s *Server) handlePollReminders(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	due, err := s.db.ListDueReminders(time.Now())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	hasPlatform := s.db.HasPlatformIdentity(u.ID)
	type item struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	var result []item
	for _, r := range due {
		if r.WorkspaceID != u.ID {
			continue
		}
		result = append(result, item{ID: r.ID, Message: r.Message})
		// Only mark sent here for web-only users. Platform users get marked sent
		// by the server-side reminder tick() after Telegram delivery.
		if !hasPlatform {
			_ = s.db.MarkReminderSent(r.ID)
		}
	}
	if result == nil {
		result = []item{}
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) handleDeleteReminder(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	r, err := s.db.GetReminder(id)
	if err != nil || r.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "reminder not found")
	}
	_ = s.db.DeleteReminder(id)
	return c.Redirect(http.StatusFound, "/dashboard/reminders")
}

// ── Settings ───────────────────────────────────────────────────────────────

type settingsPageData struct {
	*pageData
	DisplayName    string
	Email          string
	Location       string
	Timezone       string
	Tone           string
	Language       string
	Notes          string
	DetectedCoders []coder.Installed
	APIProviders   []coder.APIProvider
	SecretNames    []string // workspace's secret names, for the API-key dropdown

	CoderCatalogJSON template.JS // JSON array of the provider catalog for the coder-form JS
}

// buildSettingsData assembles the settings page view model (profile + detected
// coders + API providers + secret names). The active workspace comes through
// pageData.Workspace.
func (s *Server) buildSettingsData(p *pageData, w *db.Workspace) *settingsPageData {
	prof := profile.Load(s.db, w.ID)
	dn := prof.DisplayName
	if dn == "" {
		dn = w.Name
	}
	secretNames, _ := s.db.ListSecretNames(w.ID)

	return &settingsPageData{
		pageData:         p,
		DisplayName:      dn,
		Email:            prof.Email,
		Location:         prof.Location,
		Timezone:         prof.Timezone,
		Tone:             prof.Tone,
		Language:         prof.Language,
		Notes:            prof.Notes,
		DetectedCoders:   coder.DetectInstalled(),
		APIProviders:     coder.APIProviders(),
		SecretNames:      secretNames,
		CoderCatalogJSON: s.coderCatalogJSON(secretNames),
	}
}

// coderCatalogJSON marshals the provider catalog for the coder-form JS. secretNames
// is the workspace's existing secret names — used to flag providers that already have
// a stored CODER_KEY_<PROVIDER> key so the form can say "already set, paste to override".
// The catalog-slice construction itself lives in coderCatalogSlice (api_settings.go)
// so the template path and the JSON API build it in exactly one place.
func (s *Server) coderCatalogJSON(secretNames []string) template.JS {
	catJSON, _ := json.Marshal(s.coderCatalogSlice(secretNames))
	return template.JS(catJSON)
}

func (s *Server) showSettings(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	return c.Render(http.StatusOK, "dashboard/settings.html", s.buildSettingsData(s.page(c, "Settings"), w))
}

func (s *Server) handleSaveSettings(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	tone := c.FormValue("tone_custom")
	if tone == "" {
		tone = c.FormValue("tone")
	}
	prof := profile.Profile{
		DisplayName: c.FormValue("display_name"),
		Email:       c.FormValue("email"),
		Location:    c.FormValue("location"),
		Timezone:    c.FormValue("timezone"),
		Tone:        tone,
		Language:    c.FormValue("language"),
		Notes:       c.FormValue("notes"),
	}
	p := s.page(c, "Settings")

	if err := profile.Save(s.db, w.ID, prof); err != nil {
		p.Error = "Failed to save settings: " + err.Error()
	} else {
		s.audit.Log(w.ID, "update_settings", "workspace:"+w.ID, "profile", c.RealIP())
		p.Success = "Settings saved"
	}
	return c.Render(http.StatusOK, "dashboard/settings.html", s.buildSettingsData(p, w))
}

// handleSaveWorkspaceMeta updates the workspace name + about from the settings page.
func (s *Server) handleSaveWorkspaceMeta(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	name := c.FormValue("name")
	about := c.FormValue("about")
	p := s.page(c, "Settings")
	if name == "" {
		p.Error = "Workspace name is required"
	} else if err := s.db.UpdateWorkspaceMeta(w.ID, name, about); err != nil {
		p.Error = "Failed to save: " + err.Error()
	} else {
		s.audit.Log(w.ID, "update_workspace_meta", "workspace:"+w.ID, "", c.RealIP())
		p.Success = "Workspace details saved"
		w, _ = s.db.GetWorkspaceByID(w.ID) // reflect new values in the view
		p.Workspace = w
	}
	return c.Render(http.StatusOK, "dashboard/settings.html", s.buildSettingsData(p, w))
}

// coderForm is the generic (transport-agnostic) input to saveWorkspaceCoderCore —
// mirrors the settings-page form fields exactly (TimeoutS stays a string, parsed
// inside the core), so both the template handler and the JSON API can feed it
// straight from their respective request formats.
type coderForm struct {
	Kind, Bin, TimeoutS, Provider, Model, BaseURL, APIKey string
}

// saveWorkspaceCoderCore validates and persists a workspace's coder config. Two
// kinds: "local" (a host CLI binary) or "api" (a direct LLM provider API).
// userErrMsg is a user-facing validation problem (400-class, e.g. missing
// provider/model, bad API-key plan); err is an unexpected failure (500-class,
// e.g. can't decrypt the master password, can't write the secret, can't save
// to the DB). Persists the coder config on success; does not audit-log (the
// caller does, since only it has request context like IP).
func (s *Server) saveWorkspaceCoderCore(w *db.Workspace, f coderForm) (string, error) {
	kind := f.Kind
	if kind == "" {
		kind = "local"
	}
	timeoutS := 0
	if v, err := strconv.Atoi(f.TimeoutS); err == nil && v > 0 {
		timeoutS = v
	}

	var (
		bin, backendType      string
		provider, model       string
		apiKeySecret, baseURL string
	)
	if kind == "api" {
		provider = f.Provider
		model = strings.TrimSpace(f.Model)
		baseURL = strings.TrimSpace(f.BaseURL)
		backendType = "api"
		if provider == "" {
			return "Provider is required for an API coder", nil
		}
		if model == "" {
			return "Model is required for an API coder", nil
		}
		// Custom (generic) requires an explicit base URL; catalog providers resolve theirs from llm.
		isCustom := provider == "generic"
		if isCustom && baseURL == "" {
			return "A base URL is required for a Custom (OpenAI-compatible) provider", nil
		}
		// Decide the API-key secret from the pasted key + existing reference.
		plan := coder.PlanKeySecret(provider, strings.TrimSpace(f.APIKey), w.CoderAPIKeySecret)
		if plan.Err != "" {
			// No pasted key and no coder_api_key_secret already on record — but a
			// secret matching this provider's reserved name may still exist (e.g.
			// saved directly via the secrets API, or left over from switching away
			// from this provider and back). Reuse it instead of forcing a re-paste.
			if names, lerr := s.db.ListSecretNames(w.ID); lerr == nil {
				want := coder.CoderKeySecretName(provider)
				for _, n := range names {
					if n == want {
						plan = coder.KeySecretPlan{SecretName: want}
						break
					}
				}
			}
		}
		if plan.Err != "" {
			return plan.Err, nil
		}
		if plan.WriteSecret {
			if w.SecretsSalt == "" || w.EncryptedMasterPassword == "" {
				return "Complete workspace setup before configuring an API coder", nil
			}
			masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
			if err != nil {
				return "", errors.New("Could not decrypt master password — re-run workspace setup")
			}
			svc := secrets.New(s.db, w.ID, masterPw, w.SecretsSalt)
			if err := svc.Set(context.Background(), plan.SecretName, plan.WriteValue); err != nil {
				return "", fmt.Errorf("Failed to store API key: %w", err)
			}
		}
		apiKeySecret = plan.SecretName
	} else {
		kind = "local"
		bin = f.Bin
		backendType = coder.BackendForBin(bin) // derive from the chosen binary; empty bin => "" (auto-detect)
	}

	if err := s.db.UpdateWorkspaceCoder(w.ID, kind, bin, timeoutS, backendType, provider, model, apiKeySecret, baseURL); err != nil {
		return "", fmt.Errorf("Failed to save coder: %w", err)
	}
	return "", nil
}

// handleSaveWorkspaceCoder updates the workspace's coder config from settings.
// Two kinds: "local" (a host CLI binary) or "api" (a direct LLM provider API).
func (s *Server) handleSaveWorkspaceCoder(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	f := coderForm{
		Kind:     c.FormValue("coder_kind"),
		Bin:      c.FormValue("coder_bin"),
		TimeoutS: c.FormValue("coder_timeout_s"),
		Provider: c.FormValue("coder_provider"),
		Model:    c.FormValue("coder_model"),
		BaseURL:  c.FormValue("coder_base_url"),
		APIKey:   c.FormValue("coder_api_key"),
	}

	p := s.page(c, "Settings")
	userErrMsg, err := s.saveWorkspaceCoderCore(w, f)
	if userErrMsg != "" {
		p.Error = userErrMsg
		return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, w))
	}
	if err != nil {
		p.Error = err.Error()
		return c.Render(http.StatusInternalServerError, "dashboard/settings.html", s.buildSettingsData(p, w))
	}

	// Reload so the audit detail + rendered view reflect exactly what was saved
	// (the core may have normalized kind to "local"/"api" and resolved the secret name).
	if w2, err := s.db.GetWorkspaceByID(w.ID); err == nil {
		w = w2
	}
	detail := w.CoderBin
	if w.CoderKind == "api" {
		detail = w.CoderProvider + "/" + w.CoderModel
	}
	s.audit.Log(w.ID, "configure_coder", "workspace:"+w.ID, w.CoderKind+":"+detail, c.RealIP())
	p.Success = "Coder settings saved"
	p.Workspace = w
	return c.Render(http.StatusOK, "dashboard/settings.html", s.buildSettingsData(p, w))
}

// handleSmokeCoder runs a fail-loud end-to-end check of the workspace's currently
// saved coder and returns the result as JSON for the settings page.
func (s *Server) handleSmokeCoder(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	cd := s.coderForWorkspace(w.ID)
	ctx, cancel := context.WithTimeout(c.Request().Context(), 100*time.Second)
	defer cancel()
	reply, err := cd.Smoke(ctx, w.ID)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "reply": reply})
}

// msgWrongMasterPassword is the exact user-facing text produced by
// changeMasterPasswordCore when the supplied current password fails to
// decrypt an existing secret. It is a single source shared by the core (which
// emits it) and apiPutSettingsMasterPassword (which compares against it to
// decide 401 wrong_master_password vs 400 invalid_master_password_change) so
// the two never drift apart the way a duplicated string literal could.
const msgWrongMasterPassword = "Old master password is incorrect"

// changeMasterPasswordCore verifies the old master password (trusting it when
// there are no secrets to check against, to avoid lockout), re-encrypts every
// stored secret under the new one, and persists the new encrypted master
// password. Confirmation-matching and length are the caller's job (they need
// the raw "confirm" value, which isn't part of this signature). userErrMsg is
// a user-facing validation problem (400-class; msgWrongMasterPassword
// specifically is the one callers may want to surface as 401); err is an
// unexpected failure (500-class). Does not audit-log (the caller does).
//
// Also reused by the setup wizard's step-2 handlers (apiSetupMasterPassword /
// handleSetupMasterPassword) for a Back-then-resubmit-with-a-different-
// password re-post — which is why this persists via UpdateWorkspaceMasterPassword
// rather than UpdateWorkspaceSetup: the latter also flips needs_setup to 0,
// which would be premature and wrong mid-wizard (setup only completes at the
// wizard's own "finish" step). For the settings-page callers (post-setup,
// needs_setup already 0) this is behavior-identical — it just no longer
// re-asserts a flag that's already unset.
func (s *Server) changeMasterPasswordCore(u *db.Workspace, oldPw, newPw string) (string, error) {
	if u.SecretsSalt == "" {
		return "Account setup not complete", nil
	}

	// Verify old password by attempting to decrypt an existing secret.
	// If there are no secrets, trust the provided old password to avoid lockout.
	ctx := context.Background()
	names, _ := s.db.ListSecretNames(u.ID)
	if len(names) > 0 {
		oldSvc := secrets.New(s.db, u.ID, oldPw, u.SecretsSalt)
		if _, err := oldSvc.Get(ctx, names[0]); err != nil {
			return msgWrongMasterPassword, nil
		}
	}

	// Re-encrypt all secrets with the new key (same salt, new password → new derived key).
	oldSvc := secrets.New(s.db, u.ID, oldPw, u.SecretsSalt)
	newSvc := secrets.New(s.db, u.ID, newPw, u.SecretsSalt)
	for _, name := range names {
		val, err := oldSvc.Get(ctx, name)
		if err != nil {
			return "Failed to re-encrypt secrets: " + err.Error(), nil
		}
		if err := newSvc.Set(ctx, name, val); err != nil {
			return "Failed to re-encrypt secrets: " + err.Error(), nil
		}
	}

	// Update encrypted master password stored for scheduler.
	encMasterPw, err := secrets.EncryptMasterPassword(newPw, s.systemKey)
	if err != nil {
		return "", err
	}
	if err := s.db.UpdateWorkspaceMasterPassword(u.ID, encMasterPw, u.SecretsSalt); err != nil {
		return "", err
	}
	return "", nil
}

// resubmitPasswordOverExistingSecrets handles a setup-wizard step-2 re-post
// once secrets already exist under the workspace's current salt (e.g. the
// user went Back to step 2 after step 3 wrote a coder API key). The old
// destructive behavior silently regenerated the salt, orphaning those
// secrets. This decrypts the CURRENT master password (via the system key —
// the wizard's step-2 form has no "current password" field, unlike the
// Settings page's change-password form) and compares it to the resubmitted
// one:
//   - same password → no-op (nothing to do, matches the old "silent" outcome
//     for the common case of a user just clicking Back and Next again)
//   - different password → re-encrypts every existing secret in place via
//     changeMasterPasswordCore, so a genuine password change mid-wizard
//     isn't silently discarded
//
// Shared by apiSetupMasterPassword and handleSetupMasterPassword.
func (s *Server) resubmitPasswordOverExistingSecrets(w *db.Workspace, newPw string) (string, error) {
	oldPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return "", fmt.Errorf("could not decrypt current master password: %w", err)
	}
	if newPw == oldPw {
		return "", nil
	}
	return s.changeMasterPasswordCore(w, oldPw, newPw)
}

func (s *Server) handleChangeMasterPassword(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	oldPw := c.FormValue("current")
	newPw := c.FormValue("new_password")
	confirm := c.FormValue("confirm")

	renderErr := func(msg string) error {
		p := s.page(c, "Settings")
		p.Error = msg
		return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, u))
	}

	if oldPw == "" || newPw == "" {
		return renderErr("Old and new master passwords are required")
	}
	if len(newPw) < 8 {
		return renderErr("New master password must be at least 8 characters")
	}
	if newPw != confirm {
		return renderErr("New passwords do not match")
	}

	userErrMsg, err := s.changeMasterPasswordCore(u, oldPw, newPw)
	if err != nil {
		return err
	}
	if userErrMsg != "" {
		return renderErr(userErrMsg)
	}

	s.audit.Log(u.ID, "change_master_password", "workspace:"+u.ID, "", c.RealIP())
	p := s.page(c, "Settings")
	p.Success = "Master password changed successfully"
	return c.Render(http.StatusOK, "dashboard/settings.html", s.buildSettingsData(p, u))
}
