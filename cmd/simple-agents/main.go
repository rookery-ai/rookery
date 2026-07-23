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

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/chat"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/memory"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/scheduler"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/skilldesigner"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/ilijad1/simple-agents/internal/skillstore"
	"github.com/ilijad1/simple-agents/internal/vault"
	"github.com/ilijad1/simple-agents/internal/websearch"
	"github.com/ilijad1/simple-agents/web"
	"github.com/urfave/cli/v3"
)

func main() {
	app := &cli.Command{
		Name:  "simple-agents",
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
			adminCmd(),
			sandboxExecCmd(),
			connectorCmd(),
			kbCmd(),
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
		Usage: "Start the Simple Agents server",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.Root().String("config"))
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if err := os.MkdirAll(cfg.Data.Dir, 0o750); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}

			migrationsDir := resolveDir("migrations")
			database, err := db.Open(cfg.Database.Path, migrationsDir)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer database.Close()

			slog.Info("database ready", "path", cfg.Database.Path)

			sysKey, err := secrets.SystemKeyFromEnv()
			if err != nil {
				return fmt.Errorf("system key: %w", err)
			}

			homesDir := filepath.Join(cfg.Data.Dir, "claude-homes")
			coderSvc := coder.New(cfg.Coder.ClaudeBin, cfg.Coder.Timeout, homesDir, cfg.Data.Dir).
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
					cfg.Coder.ClaudeBin, cfg.Coder.Timeout, cfg.Sandbox.Enabled).
					WithSecretsLookup(secretsLookup)
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

			// Loopback KB bridge so CLI coders reach conversion + search in-process
			// (the same vault.ImportFile / Searcher code the API engine calls directly).
			kbBridge := vault.NewBridge(vlt)
			if _, err := kbBridge.Start(ctx); err != nil {
				return fmt.Errorf("start kb bridge: %w", err)
			}

			designFlow := agentdesigner.NewFlow(coderFor, designer).
				WithDB(database).
				WithMemory(memStore).
				WithConnectors(connReg, connStore).
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
				WithKBBridge(kbBridge).
				WithCoderFactory(func(workspaceID string) *coder.Coder {
					w, err := database.GetWorkspaceByID(workspaceID)
					if err != nil || w == nil {
						return nil
					}
					return coder.ForWorkspace(w, homesDir, cfg.Data.Dir, vlt,
						cfg.Coder.ClaudeBin, cfg.Coder.Timeout, cfg.Sandbox.Enabled).
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
				// reaches them via loopback bridges (`simple-agents connector exec <tool>`,
				// `simple-agents kb convert|search`), mirroring agent runs.
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
							extraEnv["SA_KB_URL"] = kbBridge.URL()
							extraEnv["SA_KB_TOKEN"] = kbTok
						}
					}
					if connBridge != nil && connBridge.Addr() != "" {
						if rows, err := database.ListServiceConnections(ctx, workspaceID); err == nil {
							bound := connectors.ActiveBoundConns(rows)
							if len(bound) > 0 {
								tok := connBridge.Register(workspaceID, bound, false)
								defer connBridge.Unregister(tok)
								extraEnv["SA_CONNECTOR_URL"] = connBridge.Addr()
								extraEnv["SA_CONNECTOR_TOKEN"] = tok
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
					if len(extraEnv) > 0 {
						cd = cd.WithExtraEnv(extraEnv)
					}
					// CLI coders reach connectors/kb by running `<bin> connector exec …` /
					// `<bin> kb …` as shell commands; grant narrowly-scoped Bash permissions
					// for only those commands (chat stays file-only otherwise).
					cd = cd.WithAllowedTools(coder.ChatAllowedTools(connBin, kbBin))
				}
				sysCtx := prompts.BuildChatSystemPrompt(root, cd.BackendType(), connRefs, connTools, connBin) + chat.BuildUserContext(database, memStore, workspaceID)
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
				return runner.RunByName(ctx, workspaceID, agentName, "", send)
			}

			// The SAME skillFlow instance the web layer uses — two would each hold
			// their own session map, and the one-session-at-a-time guarantee would
			// not hold across the web and chat surfaces.
			router := gateway.NewRouter(database, textHandler, agentRunHandler, designFlow, memStore).
				WithTimeParserFallback(buildLLMTimeParserFn(coderSvc)).
				WithSkillFlow(skillFlow).
				WithVault(vlt)
			gwManager := gateway.New(database, sysKey, router)

			go func() {
				if err := gwManager.StartAll(ctx); err != nil {
					slog.Error("gateway start error", "err", err)
				}
			}()
			defer gwManager.StopAll()

			// Start scheduler and reminder service.
			sched := scheduler.New(database, runner, sysKey).WithSender(gwManager)
			go sched.Run(ctx)

			reminderSvc := reminder.New(database, gwManager).WithReflector(vlt.Reflector()).WithSearcher(vaultSearcher)
			go reminderSvc.Run(ctx)

			sessionSvc := chat.New(database).WithReflector(vlt.Reflector())
			go sessionSvc.Run(ctx)

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
			srv = srv.WithBridge(connBridge).WithKBBridge(kbBridge)

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
// run-scoped token come from the SA_CONNECTOR_URL / SA_CONNECTOR_TOKEN env vars the runner
// injects. Usage: simple-agents connector exec <tool> --args '<json>'
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
					base := os.Getenv("SA_CONNECTOR_URL")
					token := os.Getenv("SA_CONNECTOR_TOKEN")
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

// kbCmd is how a CLI coder reaches the knowledge base's conversion and search
// paths: it POSTs to the loopback KB bridge in the host process, which runs the
// SAME vault.ImportFile / Searcher code the API engine calls in-process. The
// bridge URL and a run-scoped token come from SA_KB_URL / SA_KB_TOKEN.
func kbCmd() *cli.Command {
	post := func(ctx context.Context, endpoint string, payload any) error {
		base, token := os.Getenv("SA_KB_URL"), os.Getenv("SA_KB_TOKEN")
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

					migrationsDir := resolveDir("migrations")
					database, err := db.Open(cfg.Database.Path, migrationsDir)
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

					migrationsDir := resolveDir("migrations")
					database, err := db.Open(cfg.Database.Path, migrationsDir)
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

// resolveDir returns the given subdir relative to the binary's location,
// falling back to the current working directory.
func resolveDir(sub string) string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), sub)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return sub
}
