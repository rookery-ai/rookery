package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/chat"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
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

			// Resolve template and static dirs relative to binary location.
			if cfg.Server.TemplatesDir == "" {
				cfg.Server.TemplatesDir = resolveDir("web/templates")
			}
			if cfg.Server.StaticDir == "" {
				cfg.Server.StaticDir = resolveDir("web/static")
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

			// One-time cutover: make the agent_skills DB table the single source of
			// truth for an agent's skills. Pre-refactor agents had their declared
			// skills in manifest.Skills (agent.json) only; the designer never wrote the
			// DB table. Seed the DB from each manifest (skipping the legacy "all skills"
			// fallback bloat), then clear manifest.Skills. AGENT.md is for the LLM; the
			// DB is the skill record. Idempotent — a no-op once manifest.Skills is empty.
			coreSkillNames := make([]string, 0)
			for _, s := range skilllibrary.LoadBundled() {
				coreSkillNames = append(coreSkillNames, s.Name)
			}
			if n, err := agentdesigner.ReconcileSkillAttachmentsToDB(database, vaultsDir, coreSkillNames); err != nil {
				slog.Warn("reconcile skill attachments to db", "err", err)
			} else if n > 0 {
				slog.Info("reconciled skill attachments to db", "count", n)
			}

			// Any run still flagged in-progress is a leftover from a crash/shutdown
			// mid-run — close it out so it can't show a permanently stuck "Running…"
			// badge (runs now execute on a detached context that outlives the request).
			if n, err := database.ReconcileStaleRuns(); err != nil {
				slog.Warn("reconcile stale runs", "err", err)
			} else if n > 0 {
				slog.Info("reconciled stale agent runs", "count", n)
			}

			designFlow := agentdesigner.NewFlow(coderFor, designer).
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
				WithKBLister(vlt)
			skillStore := skillstore.New(database, skillsDir)
			runner := agentrunner.New(database, sysKey, agentsDir, homesDir, cfg.Data.Dir, coderSvc, skillsDir).
				WithMemory(memStore).
				WithVault(vlt).
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
				WithKBLister(vlt)

			vaultSearcher := vlt.NewSearcher()

			textHandler := func(ctx context.Context, workspaceID string, history []db.ChatMessage, text string, send func(string)) error {
				root := vlt.Root(workspaceID)
				cd := coderFor(workspaceID).WithDir(root)
				if !cd.IsAPI() {
					cd = cd.WithAllowedTools("Read,Write,Edit,Glob,Grep")
				}
				sysCtx := prompts.BuildChatSystemPrompt(root, cd.BackendType()) + chat.BuildUserContext(database, memStore, workspaceID)
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

			router := gateway.NewRouter(database, textHandler, agentRunHandler, designFlow, memStore).
				WithTimeParserFallback(buildLLMTimeParserFn(coderSvc))
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
