package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/approval"
	"github.com/rookery-ai/rookery/internal/auth"
	"github.com/rookery-ai/rookery/internal/backup"
	"github.com/rookery-ai/rookery/internal/buildinfo"
	"github.com/rookery-ai/rookery/internal/chat"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/config"
	"github.com/rookery-ai/rookery/internal/connalert"
	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
	"github.com/rookery-ai/rookery/internal/health"
	"github.com/rookery-ai/rookery/internal/mcp"
	"github.com/rookery-ai/rookery/internal/memory"
	"github.com/rookery-ai/rookery/internal/profile"
	"github.com/rookery-ai/rookery/internal/prompts"
	"github.com/rookery-ai/rookery/internal/reminder"
	"github.com/rookery-ai/rookery/internal/sandbox"
	"github.com/rookery-ai/rookery/internal/scheduler"
	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/rookery-ai/rookery/internal/skilldesigner"
	"github.com/rookery-ai/rookery/internal/skilllibrary"
	"github.com/rookery-ai/rookery/internal/skillstore"
	"github.com/rookery-ai/rookery/internal/vault"
	"github.com/rookery-ai/rookery/internal/websearch"
	"github.com/rookery-ai/rookery/web"
	"github.com/urfave/cli/v3"
)

func main() {
	app := &cli.Command{
		Name:  "rookery",
		Usage: "Multi-user AI Agents Control Plane",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to config.yaml",
				Value:   "config.yaml",
			},
		},
		Commands: []*cli.Command{
			serveCmd(),
			onboardCmd(),
			adminCmd(),
			sandboxExecCmd(),
			connectorCmd(),
			mcpCmd(),
			kbCmd(),
			backupCommand(),
			versionCmd(),
			healthcheckCmd(),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serveCmd() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the Rookery server",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.Root().String("config"))
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if err := os.MkdirAll(cfg.Data.Dir, 0o750); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}

			// Report the build identity and every host capability that degrades
			// SILENTLY when missing. The python3 warning is the load-bearing one:
			// without it the agent-tool AST guardrail self-skips, so a security
			// control switches itself off with no other signal.
			rep := health.Detect(cfg.Sandbox.Enabled, cfg.Coder.Mode)
			slog.Info("rookery starting",
				"version", rep.Version, "commit", rep.Commit,
				"sandbox_supported", rep.Sandbox.Supported,
				"sandbox_enabled", rep.Sandbox.Enabled,
				"sandbox_abi", rep.Sandbox.ABI,
				"coder_mode", rep.CoderMode)
			for _, warn := range rep.Warnings() {
				slog.Warn("capability degraded", "detail", warn)
			}

			// Order here is load-bearing. A staged restore must be swapped in
			// BEFORE the database is opened or migrated — that is the whole
			// reason the swap happens at startup rather than in-process. The
			// lock is then held for the server's entire lifetime so a
			// concurrent `backup restore` refuses instead of corrupting a live
			// install.
			if err := backup.ApplyPendingRestore(cfg.Data.Dir); err != nil {
				return fmt.Errorf("apply pending restore: %w", err)
			}
			installLock, err := backup.AcquireLock(cfg.Data.Dir)
			if err != nil {
				return err
			}
			defer installLock.Release()

			database, err := db.Open(cfg.Database.Path)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer database.Close()

			slog.Info("database ready", "path", cfg.Database.Path)

			// The workspace count decides how a first-run key is derived: an
			// install that already holds encrypted data must keep its legacy
			// hostname-derived key byte-for-byte, while a fresh one gets random
			// bytes. Either way the key is pinned to a file from now on.
			var wsCount int
			if err := database.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&wsCount); err != nil {
				return fmt.Errorf("count workspaces: %w", err)
			}
			sysKey, err := secrets.SystemKey(cfg.Data.Dir, wsCount > 0)
			if err != nil {
				return fmt.Errorf("system key: %w", err)
			}

			homesDir := filepath.Join(cfg.Data.Dir, "claude-homes")
			coderSvc := coder.New(cfg.Coder.Bin, cfg.Coder.Timeout, homesDir, cfg.Data.Dir).
				WithSandbox(cfg.Sandbox.Enabled)
			if cfg.Sandbox.Enabled && !sandbox.Supported() {
				slog.Warn("sandbox enabled but kernel has no Landlock support; falling back to detective vault guard")
			}

			// All per-user knowledge now lives under one vault root per user:
			// <data>/vaults/<workspaceID>/. Agent dirs, skills, and memory are scoped
			// inside it so each user's vault is a single browsable, backup-able unit.
			vaultsDir := filepath.Join(cfg.Data.Dir, "vaults")
			vlt := vault.New(cfg.Data.Dir)

			// secretsLookup resolves a single named secret for a workspace at run time.
			// The API coder (workspaces.coder_kind='api') uses it to fetch its provider
			// API key lazily on every call — the same path the scheduler uses to decrypt
			// the stored master password. Applied to both the shared default coder and the
			// per-workspace factory so the Telegram shared-coder path works too.
			secretsLookup := func(ctx context.Context, workspaceID, name string) (string, error) {
				w, err := database.GetWorkspaceByID(workspaceID)
				if err != nil || w == nil || w.EncryptedMasterPassword == "" {
					return "", err
				}
				masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, sysKey)
				if err != nil {
					return "", err
				}
				svc := secrets.New(database, workspaceID, masterPw, w.SecretsSalt)
				return svc.Get(ctx, name)
			}
			coderSvc = coderSvc.WithVault(vlt).WithSecretsLookup(secretsLookup)

			// coderFor builds a per-workspace coder honoring the workspace's inlined
			// coder config (local or api), instead of always using the shared CLI
			// coderSvc. Used by the agent designer, skill creator, and Telegram chat so
			// a workspace configured with coder_kind=api gets its own engine everywhere
			// — not just for agent runs and the web chat composer.
			coderFor := func(workspaceID string) *coder.Coder {
				w, err := database.GetWorkspaceByID(workspaceID)
				if err != nil || w == nil {
					return coderSvc
				}
				return coder.ForWorkspace(w, homesDir, cfg.Data.Dir, vlt,
					cfg.Coder.Bin, cfg.Coder.Timeout, cfg.Sandbox.Enabled,
					cfg.Coder.Mode == config.ModeFull).
					WithSecretsLookup(secretsLookup)
			}

			// titleGen produces a one-time content-derived chat title from the first
			// real exchange, via a text-only call to the workspace's own coder. Wired
			// into both the web send handler and the gateway router so auto-titling
			// behaves identically on every surface (see internal/chat.MaybeAutoTitle).
			titleGen := func(ctx context.Context, workspaceID, userMsg, reply string) (string, error) {
				res, err := coderFor(workspaceID).WithNoTools().
					Chat(ctx, workspaceID, nil, prompts.BuildChatTitlePrompt(), prompts.ChatTitleUserPrompt(userMsg, reply))
				if err != nil {
					return "", err
				}
				return res.Text, nil
			}

			agentsDir := vaultsDir
			skillsDir := vaultsDir
			designer := agentdesigner.NewDesigner(database, agentsDir)
			memStore := memory.New(vaultsDir)

			// One-time, idempotent migration of any pre-vault on-disk data
			// (<data>/agents, <data>/memory, <data>/skills) into per-user vaults.
			if err := vlt.MigrateLegacyLayout(); err != nil {
				slog.Warn("vault migration", "err", err)
			}
			if err := vlt.MigrateSessionsToChats(); err != nil {
				slog.Warn("vault sessions→chats migration", "err", err)
			}
			// Rename files/ → uploads/ and rewrite the two references every
			// imported note carries into it. Runs before anything serves the
			// tree, or a note's "Converted from" link would 404.
			if err := vlt.MigrateFilesToUploads(); err != nil {
				slog.Warn("vault files→uploads migration", "err", err)
			}
			// Sweep inbox/ notes written by builds that still reflected
			// notifications into the vault. The rows they projected are still in
			// inbox_messages; only the projection is gone.
			if err := vlt.RemoveLegacyInboxNotes(); err != nil {
				slog.Warn("vault legacy inbox sweep", "err", err)
			}

			// Consolidate any legacy UUID-keyed memory notes into GENERAL.md.
			// Must run after MigrateLegacyLayout (which may have just created UUID files
			// from legacy memory.jsonl via ImportJSONL).
			if userDirs, err := os.ReadDir(vaultsDir); err == nil {
				for _, d := range userDirs {
					if !d.IsDir() {
						continue
					}
					if err := memStore.MigrateToStructuredFiles(d.Name()); err != nil {
						slog.Warn("memory: migrate to structured files", "user", d.Name(), "err", err)
					}
					// Then bring memory/ up to the current identity layout:
					// USER.md → ABOUT.md, SOUL.md → STYLE.md, and a backfill
					// from the DB for any file still empty. The backfill is what
					// repairs an EXISTING install — setup values never reached
					// memory/ before, so every workspace's identity files are
					// the untouched scaffold, which isEffectivelyEmpty then
					// dropped from every prompt.
					w, err := database.GetWorkspaceByID(d.Name())
					if err != nil {
						// A vault dir with no workspace row: a deleted tenant's
						// leftovers. Skip rather than invent an identity.
						continue
					}
					p := profile.Load(database, w.ID)
					if err := memStore.MigrateIdentityFiles(w.ID, memory.Identity{
						WorkspaceName:  w.Name,
						WorkspaceAbout: w.About,
						DisplayName:    p.DisplayName,
						Email:          p.Email,
						Location:       p.Location,
						Notes:          p.Notes,
						Tone:           p.Tone,
						Language:       p.Language,
					}); err != nil {
						slog.Warn("memory: migrate identity files", "workspace", w.ID, "err", err)
					}
				}
			}

			// Idempotent startup migration: converts each agent's state.json to the
			// new markdown state.md format (readable in the KB), and absorbs the old
			// one-time skills-attachment cutover (agent_skills DB table becomes the
			// single source of truth for an agent's skills, seeded from legacy
			// manifest.Skills, skipping the legacy "all skills" fallback bloat) before
			// retiring agent.json. Must run before the scheduler starts: scheduled
			// runs read state.md via the new runner path.
			coreSkillNames := make([]string, 0)
			for _, s := range skilllibrary.LoadBundled() {
				coreSkillNames = append(coreSkillNames, s.Name)
			}
			if n, err := agentdesigner.MigrateAgentFilesToMarkdown(database, vaultsDir, coreSkillNames); err != nil {
				slog.Warn("migrate agent files to markdown", "err", err)
			} else if n > 0 {
				slog.Info("agent files migrated", "agents", n)
			}

			// Any run still flagged in-progress is a leftover from a crash/shutdown
			// mid-run — close it out so it can't show a permanently stuck "Running…"
			// badge (runs now execute on a detached context that outlives the request).
			if n, err := database.ReconcileStaleRuns(); err != nil {
				slog.Warn("reconcile stale runs", "err", err)
			} else if n > 0 {
				slog.Info("reconciled stale agent runs", "count", n)
			}

			// Self-managed OAuth connectors: the embedded registry + a DB-backed token
			// store (headless refresh) power the agent runner's + the build's native typed
			// tools and the background refresh loop.
			connReg, err := connectors.LoadBundled()
			if err != nil {
				return fmt.Errorf("load connectors: %w", err)
			}
			connStore := &connectors.DBTokenStore{DB: database, SystemKey: sysKey, Reg: connReg, OAuth: connectors.OAuthClient{}}
			// Loopback bridge so CLI coders reach connectors.Execute (auth stays host-side,
			// same path the API engine calls in-process).
			connBridge := connectors.NewBridge(connReg, connStore, nil)
			if _, err := connBridge.Start(ctx); err != nil {
				return fmt.Errorf("start connector bridge: %w", err)
			}

			// MCP: one client (pooling a session per server) plus its own loopback bridge
			// so CLI coders reach the SAME mcp.Execute path the API engine calls in-process.
			//
			// It is a sibling of the connector bridge rather than a route on it because
			// internal/connectors must not import internal/mcp to serve it; the CLI reads
			// its own ROOKERY_MCP_URL / ROOKERY_MCP_TOKEN pair, so the two never couple.
			mcpClient := mcp.NewClient(nil)
			defer mcpClient.Close()
			mcpBridge := mcp.NewBridge(mcpClient)
			if _, err := mcpBridge.Start(ctx); err != nil {
				return fmt.Errorf("start MCP bridge: %w", err)
			}

			// Loopback KB bridge so CLI coders reach conversion + search in-process
			// (the same vault.ImportFile / Searcher code the API engine calls directly).
			// Approval gate for irreversible public writes (posts, uploads). Off unless
			// a workspace sets a binding's approval_mode to 'approve', so this costs
			// nothing on an install that never enables it.
			approvalSvc := approval.New(database, connReg, connStore, nil)

			kbBridge := vault.NewBridge(vlt)
			if _, err := kbBridge.Start(ctx); err != nil {
				return fmt.Errorf("start kb bridge: %w", err)
			}

			designFlow := agentdesigner.NewFlow(coderFor, designer).
				WithDB(database).
				WithMemory(memStore).
				WithConnectors(connReg, connStore).
				WithMCPStore(database).
				WithMCPBuild(mcpClient, func(ctx context.Context, workspaceID string) []mcp.BoundServer {
					// A build sees every ENABLED server: it has not declared its
					// bindings yet, and auto-bind infers them from what it actually
					// uses. The build-time guard still refuses any tool the owner has
					// not marked read-only.
					bound, err := mcp.ActiveBoundServers(ctx, database, sysKey, workspaceID)
					if err != nil {
						return nil
					}
					return bound
				}).
				WithSecretsLoader(func(ctx context.Context, workspaceID string) (map[string]string, error) {
					user, err := database.GetWorkspaceByID(workspaceID)
					if err != nil || user.EncryptedMasterPassword == "" {
						return nil, err
					}
					masterPw, err := secrets.DecryptMasterPassword(user.EncryptedMasterPassword, sysKey)
					if err != nil {
						return nil, err
					}
					svc := secrets.New(database, workspaceID, masterPw, user.SecretsSalt)
					return svc.GetAll(ctx)
				}).
				WithVault(vlt)
			skillStore := skillstore.New(database, skillsDir)

			runner := agentrunner.New(database, sysKey, agentsDir, homesDir, cfg.Data.Dir, coderSvc, skillsDir).
				WithMemory(memStore).
				WithVault(vlt).
				WithConnectors(connReg, connStore, connBridge).
				WithApprovalGate(approvalSvc.ParkerFor).
				WithKBBridge(kbBridge).
				WithMCP(mcpClient, mcpBridge).
				WithCoderFactory(func(workspaceID string) *coder.Coder {
					w, err := database.GetWorkspaceByID(workspaceID)
					if err != nil || w == nil {
						return nil
					}
					return coder.ForWorkspace(w, homesDir, cfg.Data.Dir, vlt,
						cfg.Coder.Bin, cfg.Coder.Timeout, cfg.Sandbox.Enabled,
						cfg.Coder.Mode == config.ModeFull).
						WithSecretsLookup(secretsLookup)
				})

			// Conversational skill-creator: same shape as the agent designer.
			// Core skills are embedded (always-on) and need no seeding; the saver
			// writes user-created skills into the user's vault.
			skillSaver := skilldesigner.NewSaver(database, skillsDir)
			skillFlow := skilldesigner.NewSkillFlow(coderFor, skillSaver).
				WithDB(database).
				WithMemory(memStore).
				WithSecretsLoader(func(ctx context.Context, workspaceID string) (map[string]string, error) {
					user, err := database.GetWorkspaceByID(workspaceID)
					if err != nil || user.EncryptedMasterPassword == "" {
						return nil, err
					}
					masterPw, err := secrets.DecryptMasterPassword(user.EncryptedMasterPassword, sysKey)
					if err != nil {
						return nil, err
					}
					svc := secrets.New(database, workspaceID, masterPw, user.SecretsSalt)
					return svc.GetAll(ctx)
				}).
				WithVault(vlt)

			vaultSearcher := vlt.NewSearcher()

			textHandler := func(ctx context.Context, workspaceID string, history []db.ChatMessage, text string, send func(string)) error {
				root := vlt.Root(workspaceID)
				cd := coderFor(workspaceID).WithDir(root)

				// Connector + KB bridge wiring: the API engine exposes bound connections
				// AND save_to_kb as native in-process tools directly. A CLI coder instead
				// reaches them via loopback bridges (`rookery connector exec <tool>`,
				// `rookery kb convert|search`), mirroring agent runs.
				// Search-key wiring: resolve any configured SEARCH_KEY_BRAVE/SEARCH_KEY_TAVILY
				// secrets once, host-side, and inject them into the coder's env so its
				// web_search tool's searchProviders() picks the keyed provider over the
				// keyless scraping cascade — the same upgrade agent runs already get. The
				// key value itself never reaches the model: only the host process reads
				// subprocessEnv to build the provider before making the request.
				searchEnv := websearch.ResolveKeyEnv(ctx, workspaceID, secretsLookup)

				var connRefs []prompts.ConnectionRef
				var connTools []string
				var connBin string
				var mcpRefs []prompts.MCPServerRef
				var mcpTools []string
				var mcpBin string
				if cd.IsAPI() {
					if rows, err := database.ListServiceConnections(ctx, workspaceID); err == nil {
						bound := connectors.ActiveBoundConns(rows)
						if len(bound) > 0 {
							cd = cd.WithConnectors(connReg, connStore, bound)
							for _, b := range bound {
								connRefs = append(connRefs, prompts.ConnectionRef{Provider: b.Provider, Label: b.AccountLabel, Identity: b.AccountIdentity})
							}
							for _, d := range connReg.ToolDefs(bound) {
								connTools = append(connTools, d.Name)
							}
						}
					}
					// MCP servers attach the same way, and for the same reason connections do:
					// chat is not an agent, so there is no binding to narrow by — every
					// ENABLED server is offered.
					if bound, err := mcp.ActiveBoundServers(ctx, database, sysKey, workspaceID); err == nil && len(bound) > 0 {
						cd = cd.WithMCP(mcpClient, bound)
						for _, b := range bound {
							mcpRefs = append(mcpRefs, prompts.MCPServerRef{Name: b.Name})
						}
						for _, d := range mcp.ToolDefs(bound) {
							mcpTools = append(mcpTools, d.Name)
						}
					}
					if len(searchEnv) > 0 {
						cd = cd.WithExtraEnv(searchEnv)
					}
				} else {
					// WithExtraEnv REPLACES rather than merges, so both bridges' env vars
					// are assembled into one map and injected with a single call. A CLI
					// coder's own web search is native to the CLI, not this
					// searchProviders() cascade, but including the search keys here is
					// harmless (they're just unused env).
					extraEnv := map[string]string{}
					for k, v := range searchEnv {
						extraEnv[k] = v
					}
					var kbBin string
					if kbBridge != nil && kbBridge.URL() != "" {
						if p, err := os.Executable(); err == nil {
							kbBin = p
						}
						if kbBin != "" {
							kbTok := kbBridge.Register(workspaceID, false)
							defer kbBridge.Unregister(kbTok)
							extraEnv["ROOKERY_KB_URL"] = kbBridge.URL()
							extraEnv["ROOKERY_KB_TOKEN"] = kbTok
						}
					}
					if connBridge != nil && connBridge.Addr() != "" {
						if rows, err := database.ListServiceConnections(ctx, workspaceID); err == nil {
							bound := connectors.ActiveBoundConns(rows)
							if len(bound) > 0 {
								tok := connBridge.Register(workspaceID, bound, false)
								defer connBridge.Unregister(tok)
								extraEnv["ROOKERY_CONNECTOR_URL"] = connBridge.Addr()
								extraEnv["ROOKERY_CONNECTOR_TOKEN"] = tok
								for _, b := range bound {
									connRefs = append(connRefs, prompts.ConnectionRef{Provider: b.Provider, Label: b.AccountLabel, Identity: b.AccountIdentity})
								}
								for _, d := range connReg.ToolDefs(bound) {
									connTools = append(connTools, d.Name)
								}
								if p, err := os.Executable(); err == nil {
									connBin = p
								}
							}
						}
					}
					if mcpBridge != nil && mcpBridge.Addr() != "" {
						if bound, err := mcp.ActiveBoundServers(ctx, database, sysKey, workspaceID); err == nil && len(bound) > 0 {
							tok := mcpBridge.Register(workspaceID, bound, false)
							defer mcpBridge.Unregister(tok)
							extraEnv["ROOKERY_MCP_URL"] = mcpBridge.Addr()
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
						cd = cd.WithExtraEnv(extraEnv)
					}
					// CLI coders reach connectors/kb by running `<bin> connector exec …` /
					// `<bin> kb …` as shell commands; grant narrowly-scoped Bash permissions
					// for only those commands (chat stays file-only otherwise).
					cd = cd.WithAllowedTools(coder.ChatAllowedTools(connBin, kbBin, mcpBin))
				}
				sysCtx := prompts.BuildChatSystemPrompt(root, cd.BackendType(), connRefs, connTools, connBin) +
					prompts.MCPToolsBlock(mcpRefs, mcpTools, cd.BackendType(), mcpBin) +
					chat.BuildUserContext(database, memStore, workspaceID)
				result, err := cd.Chat(ctx, workspaceID, history, sysCtx, text)
				if err != nil {
					send("Sorry, I ran into an error: " + err.Error())
					return nil
				}
				send(result.Text)
				return nil
			}

			// AgentRunHandler: /run <name> from Telegram.
			// MasterPw is empty here — agents without secret injection still run.
			// Phase 7 adds a session-stored master password.
			agentRunHandler := func(ctx context.Context, workspaceID, agentName string, send func(string)) error {
				// Label the reply with the agent that produced it, at the send
				// site rather than inside the runner: runCoderAgent reuses
				// SendOutput as a collector for child-agent recursion, and that
				// text is fed into the PARENT agent's LLM prompt.
				labelled := func(msg string) {
					send(gateway.AgentPrefixed(agentName, msg))
				}
				return runner.RunByName(ctx, workspaceID, agentName, "", labelled)
			}

			// The SAME skillFlow instance the web layer uses — two would each hold
			// their own session map, and the one-session-at-a-time guarantee would
			// not hold across the web and chat surfaces.
			router := gateway.NewRouter(database, textHandler, agentRunHandler, designFlow, memStore).
				WithTimeParserFallback(buildLLMTimeParserFn(coderSvc)).
				WithSkillFlow(skillFlow).
				WithVault(vlt).
				WithTitleGenerator(titleGen).
				WithApproval(approvalSvc)
			gwManager := gateway.New(database, sysKey, router)

			// Chat delivery for park/outcome notices. Set after the manager exists
			// because the notifier IS the manager — the approval service is built
			// earlier so the runner can reference it.
			approvalSvc.WithNotifier(gwManager)

			// Connection re-auth alerts. Attached here rather than at connStore's
			// construction for the same reason approvalSvc is: the notifier needs
			// the gateway, which does not exist until the line above. Set before
			// RunRefreshLoop starts further down.
			connStore.WithNotifier(connalert.New(database, gwManager))

			// Deliver a DETACHED build's result to chat.
			//
			// This is the recovery channel chat has never had. The web surface
			// reconnects to a running build through the SSE stream and
			// GET /design/state; on chat the only delivery was the send() closure
			// belonging to the message that started the build, so a build that
			// outlived its deadline had nowhere to report and was simply lost — the
			// user saw a progress line and then silence.
			//
			// SendToUser is used rather than a request-scoped closure precisely
			// because the turn that started the build returned minutes earlier. A
			// workspace with no linked chat platform errors here, which is expected
			// and not worth surfacing: that user is on the web surface, which polls.
			designFlow.OnBuildComplete(func(workspaceID string, origin agentdesigner.Origin, response string, _ bool, _ string, err error) {
				text := response
				if err != nil {
					text = gateway.FriendlyDesignError("agent", err)
				}
				if strings.TrimSpace(text) == "" {
					return
				}
				// Deliver ONLY to the surface that owns the session. A web-owned
				// build needs nothing pushed here: the SPA reads the outcome out
				// of the session's History via /design/state. Announcing it in
				// chat anyway is the reported defect — the dry-run landed in
				// Telegram while the browser the user was watching stayed blank.
				if !agentdesigner.DeliverToChat(origin) {
					slog.Info("agentdesigner: build result withheld from chat",
						"workspace_id", workspaceID, "origin", origin.String(), "chat_suppressed", true)
					return
				}
				if sendErr := gwManager.SendToUser(workspaceID, text); sendErr != nil {
					// Warn, not Debug. For a chat-owned build this message is the
					// user's ONLY copy, so a failed send is the whole result going
					// missing — precisely the silent failure this change ends. At
					// Debug it was invisible under the default level.
					slog.Warn("agentdesigner: chat delivery of build result failed",
						"workspace_id", workspaceID, "err", sendErr)
					return
				}
				slog.Info("agentdesigner: build result delivered",
					"workspace_id", workspaceID, "target", "chat")
			})

			go func() {
				if err := gwManager.StartAll(ctx); err != nil {
					slog.Error("gateway start error", "err", err)
				}
			}()
			defer gwManager.StopAll()

			// Start scheduler and reminder service.
			sched := scheduler.New(database, runner, sysKey).WithSender(gwManager)
			go sched.Run(ctx)

			reminderSvc := reminder.New(database, gwManager).WithSearcher(vaultSearcher)
			go reminderSvc.Run(ctx)

			sessionSvc := chat.New(database).WithReflector(vlt.Reflector())
			go sessionSvc.Run(ctx)

			// Owner-level backup runs on its own ticker rather than through
			// internal/scheduler, whose agent_schedules rows are foreign-keyed
			// to a workspace that backup does not have.
			backupSched := backup.NewScheduler(database, database.DB, cfg.Data.Dir, sysKey)
			go backupSched.Run(ctx)
			slog.Info("backup scheduler started")

			// Self-managed OAuth connector token-refresh loop: proactively renews
			// service_connections access tokens before they expire so scheduled runs
			// never hit an expired token. Uses the system key (headless, no master pw).
			// connReg/connStore were built above and are shared with the agent runner.
			go connectors.RunRefreshLoop(ctx, connStore, 5*time.Minute)

			// Nightly GC for expired agent design drafts: drops the draft row and,
			// for create-mode drafts that reached StateVerifying, removes the
			// orphaned pre-approved agent directory left on disk by runGeneration.
			go func() {
				ticker := time.NewTicker(24 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						// Stale approvals: a post approved a week after it was drafted is
						// almost never what the owner meant, and an unbounded queue turns
						// into a list nobody reads.
						approvalSvc.ExpireStale(ctx, 7*24*time.Hour)

						expired, err := database.ListExpiredAgentDrafts()
						if err != nil {
							slog.Warn("draft gc: list expired", "err", err)
							continue
						}
						for _, d := range expired {
							// Create-mode drafts leave a readable draft_<name> working dir on
							// disk (kept through blocked/designing builds as well as verifying
							// ones), so sweep it on expiry. Edit drafts point their AgentID at
							// the LIVE agent (staging is a sibling dir already removed), so never
							// touch it.
							if !d.IsEdit && d.AgentName != "" {
								_ = os.RemoveAll(agentdesigner.DraftAgentDir(vaultsDir, d.WorkspaceID, d.AgentName))
							}
							_ = database.DeleteAgentDraft(d.WorkspaceID)
						}
						// Expired skill-creator drafts: drop the row and any orphaned
						// staging directory left by runGeneration.
						expiredSkillDrafts, err := database.ListExpiredSkillDrafts()
						if err != nil {
							slog.Warn("skill draft gc: list expired", "err", err)
						}
						for _, d := range expiredSkillDrafts {
							if d.SkillName != "" {
								_ = os.RemoveAll(filepath.Join(vaultsDir, d.WorkspaceID, "skills", ".staging-"+d.SkillName))
							}
							_ = database.DeleteSkillDraft(d.WorkspaceID)
						}
					}
				}
			}()

			srv, err := web.NewServer(cfg, database, gwManager, runner, designer, homesDir, skillStore, designFlow, skillFlow, memStore)
			if err != nil {
				return fmt.Errorf("create server: %w", err)
			}
			srv = srv.WithBridge(connBridge).WithKBBridge(kbBridge).WithMCP(mcpClient, mcpBridge).WithTitleGenerator(titleGen).WithApproval(approvalSvc).WithBackupScheduler(backupSched)

			addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
			slog.Info("listening", "addr", addr)
			return srv.Start(addr)
		},
	}
}

// sandboxExecCmd is the hidden helper invoked by the coder via re-exec. It
// applies Landlock confinement + resource limits to itself and then exec()s the
// real command encoded in its single argument. It must do no other startup work.
func sandboxExecCmd() *cli.Command {
	return &cli.Command{
		Name:   sandbox.HelperCommand,
		Hidden: true,
		Action: func(_ context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) != 1 {
				return fmt.Errorf("%s expects exactly one encoded spec argument", sandbox.HelperCommand)
			}
			spec, err := sandbox.DecodeSpec(args[0])
			if err != nil {
				return fmt.Errorf("decode sandbox spec: %w", err)
			}
			// On success this never returns (the process image is replaced).
			return sandbox.Exec(spec)
		},
	}
}

// connectorCmd is how a CLI coder acts on a connected service: it POSTs to the loopback
// connector bridge in the host process, which runs the SAME connectors.Execute path the
// API engine uses in-process (auth/token-refresh stay host-side). The bridge URL + a
// run-scoped token come from the ROOKERY_CONNECTOR_URL / ROOKERY_CONNECTOR_TOKEN env vars the runner
// injects. Usage: rookery connector exec <tool> --args '<json>'
func connectorCmd() *cli.Command {
	return &cli.Command{
		Name:  "connector",
		Usage: "Act on a connected service account (used by CLI coders)",
		Commands: []*cli.Command{
			{
				Name:      "exec",
				Usage:     "Run a connector tool: connector exec <tool> --args '<json-object>'",
				ArgsUsage: "<tool>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "args", Usage: "JSON object of arguments", Value: "{}"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					tool := cmd.Args().First()
					if tool == "" {
						return fmt.Errorf("usage: connector exec <tool> --args '<json>'")
					}
					base := os.Getenv("ROOKERY_CONNECTOR_URL")
					token := os.Getenv("ROOKERY_CONNECTOR_TOKEN")
					if base == "" || token == "" {
						return fmt.Errorf("no connected-service bridge available in this run")
					}
					var args map[string]any
					if err := json.Unmarshal([]byte(cmd.String("args")), &args); err != nil {
						return fmt.Errorf("--args must be a JSON object: %w", err)
					}
					body, _ := json.Marshal(map[string]any{"tool": tool, "args": args})
					req, _ := http.NewRequestWithContext(ctx, "POST", base+"/exec", bytes.NewReader(body))
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						return fmt.Errorf("connector bridge unreachable: %w", err)
					}
					defer resp.Body.Close()
					out, _ := io.ReadAll(resp.Body)
					fmt.Print(string(out))
					return nil
				},
			},
		},
	}
}

// mcpCmd is how a CLI coder calls a tool on a connected MCP server: it POSTs to the
// loopback MCP bridge in the host process, which runs the SAME mcp.Execute path the
// API engine uses in-process, so the build guard and the approval gate apply to both
// coder kinds identically.
//
// The server credential never reaches this subprocess — only a run-scoped bearer
// token, which the host resolves. That is the property native --mcp-config
// passthrough would have given up.
//
// Usage: rookery mcp exec <tool> --args '<json>'
func mcpCmd() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "Call a tool on a connected MCP server (used by CLI coders)",
		Commands: []*cli.Command{
			{
				Name:      "exec",
				Usage:     "Run an MCP tool: mcp exec <tool> --args '<json-object>'",
				ArgsUsage: "<tool>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "args", Usage: "JSON object of arguments", Value: "{}"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					tool := cmd.Args().First()
					if tool == "" {
						return fmt.Errorf("usage: mcp exec <tool> --args '<json>'")
					}
					base := os.Getenv("ROOKERY_MCP_URL")
					token := os.Getenv("ROOKERY_MCP_TOKEN")
					if base == "" || token == "" {
						return fmt.Errorf("no MCP bridge available in this run")
					}
					var args map[string]any
					if err := json.Unmarshal([]byte(cmd.String("args")), &args); err != nil {
						return fmt.Errorf("--args must be a JSON object: %w", err)
					}
					body, _ := json.Marshal(map[string]any{"tool": tool, "args": args})
					req, _ := http.NewRequestWithContext(ctx, "POST", base+"/mcp/exec", bytes.NewReader(body))
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						return fmt.Errorf("MCP bridge unreachable: %w", err)
					}
					defer resp.Body.Close()
					out, _ := io.ReadAll(resp.Body)
					fmt.Print(string(out))
					return nil
				},
			},
		},
	}
}

// kbCmd is how a CLI coder reaches the knowledge base's conversion and search
// paths: it POSTs to the loopback KB bridge in the host process, which runs the
// SAME vault.ImportFile / Searcher code the API engine calls in-process. The
// bridge URL and a run-scoped token come from ROOKERY_KB_URL / ROOKERY_KB_TOKEN.
func kbCmd() *cli.Command {
	post := func(ctx context.Context, endpoint string, payload any) error {
		base, token := os.Getenv("ROOKERY_KB_URL"), os.Getenv("ROOKERY_KB_TOKEN")
		if base == "" || token == "" {
			return fmt.Errorf("no knowledge-base bridge available in this run")
		}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(ctx, "POST", base+endpoint, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("kb bridge unreachable: %w", err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		fmt.Print(string(out))
		return nil
	}
	return &cli.Command{
		Name:  "kb",
		Usage: "Knowledge-base actions (used by CLI coders)",
		Commands: []*cli.Command{
			{
				Name:      "convert",
				Usage:     "Convert a file to markdown and save it: kb convert <path> [--dest notes/x] [--title T]",
				ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dest", Usage: "vault folder for the note"},
					&cli.StringFlag{Name: "title", Usage: "override the derived title"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					src := cmd.Args().First()
					if src == "" {
						return fmt.Errorf("usage: kb convert <path> [--dest <dir>]")
					}
					data, err := os.ReadFile(src)
					if err != nil {
						return fmt.Errorf("read %s: %w", src, err)
					}
					return post(ctx, "/convert", map[string]any{
						"filename": filepath.Base(src),
						"content":  base64.StdEncoding.EncodeToString(data),
						"dest_dir": cmd.String("dest"),
						"title":    cmd.String("title"),
					})
				},
			},
			{
				Name:      "search",
				Usage:     "Search the knowledge base: kb search <query>",
				ArgsUsage: "<query>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					q := strings.Join(cmd.Args().Slice(), " ")
					if strings.TrimSpace(q) == "" {
						return fmt.Errorf("usage: kb search <query>")
					}
					return post(ctx, "/search", map[string]any{"query": q})
				},
			},
		},
	}
}

func adminCmd() *cli.Command {
	return &cli.Command{
		Name:  "owner",
		Usage: "Owner account management",
		Commands: []*cli.Command{
			{
				Name:  "bootstrap",
				Usage: "Create the initial owner account",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "username", Aliases: []string{"u"}, Required: true},
					&cli.StringFlag{Name: "password", Aliases: []string{"p"}, Required: true},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.Root().String("config"))
					if err != nil {
						return fmt.Errorf("load config: %w", err)
					}

					database, err := db.Open(cfg.Database.Path)
					if err != nil {
						return fmt.Errorf("open db: %w", err)
					}
					defer database.Close()

					o, err := auth.BootstrapOwner(database, cmd.String("username"), cmd.String("password"))
					if err != nil {
						return err
					}
					fmt.Printf("Owner account created: %s (id: %s)\n", o.Username, o.ID)
					return nil
				},
			},
			{
				Name:  "reset-password",
				Usage: "Reset the owner's password (single-owner model — no login required)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "password", Aliases: []string{"p"}, Required: true},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.Root().String("config"))
					if err != nil {
						return fmt.Errorf("load config: %w", err)
					}

					database, err := db.Open(cfg.Database.Path)
					if err != nil {
						return fmt.Errorf("open db: %w", err)
					}
					defer database.Close()

					o, err := database.GetOwner()
					if err != nil {
						return fmt.Errorf("no owner account found (run 'owner bootstrap' first): %w", err)
					}
					if err := auth.ChangePassword(database, o.ID, cmd.String("password")); err != nil {
						return fmt.Errorf("reset password: %w", err)
					}
					fmt.Printf("Password reset for owner: %s\n", o.Username)
					return nil
				},
			},
		},
	}
}

// buildLLMTimeParserFn returns a reminder.TimeParserFunc backed by coderSvc.
// It uses BuildReminderParsePrompt and parses the JSON via ParseLLMReminderJSON.
func buildLLMTimeParserFn(coderSvc *coder.Coder) reminder.TimeParserFunc {
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

// versionCmd prints the build identity stamped in at link time. An installed
// binary that cannot say what it is cannot be supported.
func versionCmd() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print the build version and exit",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			fmt.Println(buildinfo.String())
			return nil
		},
	}
}

// healthcheckCmd probes the local server's /healthz and exits non-zero if it is
// not serving. It exists so the container HEALTHCHECK can shell the binary
// itself: the runtime image ships no curl, and adding one purely for a health
// probe is dead weight in every layer and every scan.
func healthcheckCmd() *cli.Command {
	return &cli.Command{
		Name:  "healthcheck",
		Usage: "Probe the local server's /healthz and exit non-zero if unhealthy",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.Root().String("config"))
			if err != nil {
				return err
			}
			url := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Server.Port)
			client := &http.Client{Timeout: 4 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				return fmt.Errorf("healthcheck: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
			}
			return nil
		},
	}
}
