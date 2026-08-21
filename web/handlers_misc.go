package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/chat"
	"github.com/rookery-ai/rookery/internal/coder"
	// Aliased because this file has a local variable named `coder` that shadows
	// the package inside the chat handler.
	"github.com/labstack/echo/v4"
	codersvc "github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/mcp"
	"github.com/rookery-ai/rookery/internal/prompts"
	"github.com/rookery-ai/rookery/internal/reminder"
	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/rookery-ai/rookery/internal/websearch"
)

// ── Chats ───────────────────────────────────────────────────────────────────

// handleChatMessage STARTS one chat turn and returns immediately.
//
// It used to run the whole turn inline: the coder executed on the REQUEST
// context and both messages were persisted only after it returned. So for the
// entire turn — minutes, on a real question — the owner's message existed
// nowhere but the browser's component state, and leaving the page destroyed it;
// closing the tab cancelled the request context and killed the turn outright.
// Navigating WITHIN the SPA kept the fetch alive, which is why a turn that
// happened to finish while the owner was away did land both messages, and why
// the whole thing read as flakiness.
//
// The turn now runs on a detached context with the user's message persisted
// first (see startChatTurn), so this handler's job is only to validate and
// hand off. 202 + turn_id; the reply arrives via the chat query once the
// progress stream reports done.
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

	turnID, ok := s.startChatTurn(u.ID, id, text)
	if !ok {
		return c.JSON(http.StatusConflict, map[string]string{
			"error":   "turn_in_flight",
			"message": "This chat is already working on a turn.",
		})
	}
	return c.JSON(http.StatusAccepted, map[string]string{"turn_id": turnID})
}

// runChatCoder builds the one-off-chat coder and runs a single turn.
//
// The construction below is unchanged from when it lived inline in
// handleChatMessage; only the context source and the progress sink are new. It
// must stay in step with the Telegram/Discord/Slack chat path in cmd/rookery —
// divergence would give one surface a capability the other lacks.
//
// history is passed IN rather than read here, because the caller must read it
// BEFORE persisting the new message: history comes from ListChatMessages, so
// reading it afterwards would feed this turn's own text twice, once as history
// and once as the message.
func (s *Server) runChatCoder(
	ctx context.Context,
	workspaceID, chatID string,
	history []db.ChatMessage,
	text string,
	onProgress func(string),
) (string, error) {
	// Test seams, checked before any coder construction so a unit test needs no
	// configured coder. They sit here rather than deeper because the property
	// under test is the ORDERING — that startChatTurn persisted the owner's
	// message before anything reached this function — and the goroutine gives a
	// test no other way to observe it.
	if s.testCoderHook != nil {
		s.testCoderHook()
	}
	if s.testCoderBlock != nil {
		<-s.testCoderBlock
	}
	if s.testCoderErr != "" {
		return "", errors.New(s.testCoderErr)
	}

	// System context: a read+write knowledge-base instruction (so the chat can retrieve
	// and edit notes on demand) + the user's always-on identity context (profile/memory/
	// agents/MCP). The coder runs with its CWD set to the vault root, which the sandbox
	// grants read+write access to, and the file toolset Read/Write/Edit/Glob/Grep (for a
	// CLI coder) or read_file/write_file/edit_file/list_dir tool calls (for an API coder).
	root := s.vault.Root(workspaceID)
	coder := s.coderForWorkspace(workspaceID).WithDir(root)

	// Connector + KB bridge wiring: the API engine exposes bound connections AND
	// save_to_kb as native in-process tools directly. A CLI coder instead reaches
	// them via loopback bridges (`rookery connector exec <tool>`,
	// `rookery kb convert|search`), the same mechanism agent runs use. This
	// list must stay in step with the Telegram/Discord/Slack chat path in
	// cmd/rookery; divergence would give one surface a capability the other
	// lacks.
	// Search-key wiring: resolve any configured SEARCH_KEY_BRAVE/SEARCH_KEY_TAVILY
	// secrets once, host-side, and inject them into the coder's env so its
	// web_search tool's searchProviders() picks the keyed provider over the
	// keyless scraping cascade — the same upgrade agent runs already get. The
	// key value itself never reaches the model: only the host process reads
	// subprocessEnv to build the provider before making the request.
	searchEnv := websearch.ResolveKeyEnv(ctx, workspaceID, s.secretsLookup)

	var connRefs []prompts.ConnectionRef
	var connTools []string
	var connBin string
	var mcpRefs []prompts.MCPServerRef
	var mcpTools []string
	var mcpBin string
	if coder.IsAPI() {
		if s.connStore != nil {
			if rows, err := s.db.ListServiceConnections(ctx, workspaceID); err == nil {
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
		}
		// Chat has no binding to narrow by, so every ENABLED server is offered —
		// the same rule connectors.ActiveBoundConns applies just above.
		if bound, err := mcp.ActiveBoundServers(ctx, s.db, s.systemKey, workspaceID); err == nil && len(bound) > 0 {
			coder = coder.WithMCP(s.mcpClient, bound)
			for _, b := range bound {
				mcpRefs = append(mcpRefs, prompts.MCPServerRef{Name: b.Name})
			}
			for _, d := range mcp.ToolDefs(bound) {
				mcpTools = append(mcpTools, d.Name)
			}
		}
		if len(searchEnv) > 0 {
			coder = coder.WithExtraEnv(searchEnv)
		}
	} else {
		// WithAllowedTools pre-approves the CLI subprocess's tool set so it never
		// blocks on an interactive permission prompt; it's meaningless for an API
		// coder (host tools are offered via native function-calling, not gated by
		// this flag), so skip it there — matches the Telegram chat path.
		// WithExtraEnv REPLACES rather than merges, so both bridges' env vars are
		// assembled into one map and injected with a single call. A CLI coder's
		// own web search is native to the CLI, not this searchProviders() cascade,
		// but including the search keys here is harmless (they're just unused env).
		extraEnv := map[string]string{}
		for k, v := range searchEnv {
			extraEnv[k] = v
		}
		var kbBin string
		if s.kbBridge != nil && s.kbBridge.URL() != "" {
			if p, err := os.Executable(); err == nil {
				kbBin = p
			}
			if kbBin != "" {
				kbTok := s.kbBridge.Register(workspaceID, false)
				defer s.kbBridge.Unregister(kbTok)
				extraEnv["ROOKERY_KB_URL"] = s.kbBridge.URL()
				extraEnv["ROOKERY_KB_TOKEN"] = kbTok
			}
		}
		if s.connBridge != nil && s.connBridge.Addr() != "" {
			if rows, err := s.db.ListServiceConnections(ctx, workspaceID); err == nil {
				bound := connectors.ActiveBoundConns(rows)
				if len(bound) > 0 {
					tok := s.connBridge.Register(workspaceID, bound, false)
					defer s.connBridge.Unregister(tok)
					extraEnv["ROOKERY_CONNECTOR_URL"] = s.connBridge.Addr()
					extraEnv["ROOKERY_CONNECTOR_TOKEN"] = tok
					for _, b := range bound {
						connRefs = append(connRefs, prompts.ConnectionRef{Provider: b.Provider, Label: b.AccountLabel, Identity: b.AccountIdentity})
					}
					for _, d := range s.connectors.ToolDefs(bound) {
						connTools = append(connTools, d.Name)
					}
					if p, err := os.Executable(); err == nil {
						connBin = p
					}
				}
			}
		}
		if s.mcpBridge != nil && s.mcpBridge.Addr() != "" {
			if bound, err := mcp.ActiveBoundServers(ctx, s.db, s.systemKey, workspaceID); err == nil && len(bound) > 0 {
				tok := s.mcpBridge.Register(workspaceID, bound, false)
				defer s.mcpBridge.Unregister(tok)
				extraEnv["ROOKERY_MCP_URL"] = s.mcpBridge.Addr()
				extraEnv["ROOKERY_MCP_TOKEN"] = tok
				for _, b := range bound {
					mcpRefs = append(mcpRefs, prompts.MCPServerRef{Name: b.Name})
				}
				for _, d := range mcp.ToolDefs(bound) {
					mcpTools = append(mcpTools, d.Name)
				}
				if p, err := os.Executable(); err == nil {
					mcpBin = p
				}
			}
		}
		if len(extraEnv) > 0 {
			coder = coder.WithExtraEnv(extraEnv)
		}
		// A CLI coder reaches connectors/kb by running `<bin> connector exec …` /
		// `<bin> kb …` as shell commands, so grant NARROWLY-SCOPED Bash permissions
		// for only those commands — chat stays file-only (no arbitrary shell) otherwise.
		coder = coder.WithAllowedTools(codersvc.ChatAllowedTools(connBin, kbBin, mcpBin))
	}
	sysCtx := prompts.BuildChatSystemPrompt(root, coder.BackendType(), connRefs, connTools, connBin, s.chatAppsFor(workspaceID)) +
		prompts.MCPToolsBlock(mcpRefs, mcpTools, coder.BackendType(), mcpBin) +
		chat.BuildUserContext(s.db, s.memory, workspaceID)

	// Re-activate the chat if it had been stopped, so history keeps flowing.
	if ch, err := s.db.GetChat(chatID); err == nil && !ch.Active {
		_ = s.db.ResumeChat(chatID)
	}

	// Progress reaches the browser from here: the API engine calls this sink
	// once per host-tool execution. A CLI coder never calls it, so those
	// workspaces simply see the typing indicator, exactly as before.
	if onProgress != nil {
		coder = coder.WithProgress(onProgress)
	}

	result, err := coder.Chat(ctx, workspaceID, history, sysCtx, text)
	if err != nil {
		return "", fmt.Errorf("couldn't reach %s: %w", coder.Name(), err)
	}
	return result.Text, nil
}

// ── Reminders ──────────────────────────────────────────────────────────────

// buildLLMTimeParser returns a reminder.TimeParserFunc backed by the given coder.
// It calls BuildReminderParsePrompt and parses the JSON response via ParseLLMReminderJSON.
// Shared by the JSON reminders API (api_home.go).
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
// so the server-side tick() can still deliver via Telegram. Shared by the JSON reminders API.
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

// ── Settings (shared cores) ──────────────────────────────────────────────────

// coderForm is the generic (transport-agnostic) input to saveWorkspaceCoderCore —
// mirrors the settings-page form fields exactly (TimeoutS stays a string, parsed
// inside the core), so the JSON API can feed it straight from its request format.
type coderForm struct {
	Kind, Bin, TimeoutS, Provider, Model, BaseURL, APIKey string
}

// saveWorkspaceCoderCore validates and persists a workspace's coder config. Two
// kinds: "local" (a host CLI binary) or "api" (a direct LLM provider API).
// userErrMsg is a user-facing validation problem (400-class, e.g. missing
// provider/model, bad API-key plan); err is an unexpected failure (500-class,
// e.g. can't decrypt the master password, can't write the secret, can't save
// to the DB). Persists the coder config on success; does not audit-log (the
// caller does, since only it has request context like IP). Shared by the JSON
// settings API (api_settings.go).
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
		// Precedence: a pasted key always wins; otherwise prefer a secret that
		// already matches THIS provider's reserved name (CODER_KEY_<PROVIDER>)
		// over w.CoderAPIKeySecret — which may be a stale reference left over
		// from a DIFFERENT provider (switching openai -> openrouter must not
		// silently keep CODER_KEY_OPENAI); only fall back to the current
		// reference when no provider-matched secret exists.
		currentSecret := w.CoderAPIKeySecret
		if strings.TrimSpace(f.APIKey) == "" {
			if names, lerr := s.db.ListSecretNames(w.ID); lerr == nil {
				want := coder.CoderKeySecretName(provider)
				for _, n := range names {
					if n == want {
						currentSecret = want
						break
					}
				}
			}
		}
		plan := coder.PlanKeySecret(provider, strings.TrimSpace(f.APIKey), currentSecret)
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
		// A local coder takes a model too, and it is not optional for all of
		// them: OpenCode has no default of its own, so with none it targets a
		// hardcoded OpenRouter default and 401s in a way that reads like broken
		// auth. Not validated — the valid set is the coder's business and
		// changes weekly, so an unknown string must not be rejected here.
		model = strings.TrimSpace(f.Model)
	}

	if err := s.db.UpdateWorkspaceCoder(w.ID, kind, bin, timeoutS, backendType, provider, model, apiKeySecret, baseURL); err != nil {
		return "", fmt.Errorf("Failed to save coder: %w", err)
	}
	return "", nil
}

// handleSmokeCoder runs a fail-loud end-to-end check of the workspace's currently
// saved coder and returns the result as JSON. Shared by the JSON settings API.
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
// Also reused by the setup wizard's step-2 handler (apiSetupMasterPassword) for
// a Back-then-resubmit-with-a-different-password re-post — which is why this
// persists via UpdateWorkspaceMasterPassword, which leaves needs_setup
// untouched, rather than also flipping it to 0: that would be premature and
// wrong mid-wizard (setup only completes at the wizard's own "finish" step).
// For the settings-page callers (post-setup, needs_setup already 0) this is
// behavior-identical — it just no longer re-asserts a flag that's already unset.
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
// Shared by apiSetupMasterPassword.
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

// chatAppsFor lists the workspace's connected chat platforms for the chat
// system prompt's platform primer.
//
// A failure returns nil rather than propagating: the chat apps are context, and
// a chat that opens without knowing about Telegram is far better than a chat
// that refuses to open. Mirrors agentrunner's own resolution.
func (s *Server) chatAppsFor(workspaceID string) []prompts.ChatAppInfo {
	conns, err := s.db.ListWorkspacePlatformConnections(workspaceID)
	if err != nil {
		return nil
	}
	platforms := make([]string, 0, len(conns))
	for _, c := range conns {
		platforms = append(platforms, c.Platform)
	}
	return prompts.ChatAppsForPlatforms(platforms)
}
