package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/memory"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/scheduler"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/session"
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
			// <data>/vaults/<userID>/. Agent dirs, skills, and memory are scoped
			// inside it so each user's vault is a single browsable, backup-able unit.
			vaultsDir := filepath.Join(cfg.Data.Dir, "vaults")
			vlt := vault.New(cfg.Data.Dir)
			agentsDir := vaultsDir
			skillsDir := vaultsDir
			designer := agentdesigner.NewDesigner(database, agentsDir)
			// Telegram uses a single system coder; wrap it in a resolver lambda.
			memStore := memory.New(vaultsDir)

			// One-time, idempotent migration of any pre-vault on-disk data
			// (<data>/agents, <data>/memory, <data>/skills) into per-user vaults.
			if err := vlt.MigrateLegacyLayout(); err != nil {
				slog.Warn("vault migration", "err", err)
			}

			// Any run still flagged in-progress is a leftover from a crash/shutdown
			// mid-run — close it out so it can't show a permanently stuck "Running…"
			// badge (runs now execute on a detached context that outlives the request).
			if n, err := database.ReconcileStaleRuns(); err != nil {
				slog.Warn("reconcile stale runs", "err", err)
			} else if n > 0 {
				slog.Info("reconciled stale agent runs", "count", n)
			}

			designFlow := agentdesigner.NewFlow(func(_ string) *coder.Coder { return coderSvc }, designer).
				WithDB(database).
				WithMemory(memStore).
				WithSecretsLoader(func(ctx context.Context, userID string) (map[string]string, error) {
					user, err := database.GetUserByID(userID)
					if err != nil || user.EncryptedMasterPassword == "" {
						return nil, err
					}
					masterPw, err := secrets.DecryptMasterPassword(user.EncryptedMasterPassword, sysKey)
					if err != nil {
						return nil, err
					}
					svc := secrets.New(database, userID, masterPw, user.SecretsSalt)
					return svc.GetAll(ctx)
				})
			skillStore := skillstore.New(database, skillsDir)
			runner := agentrunner.New(database, sysKey, agentsDir, homesDir, cfg.Data.Dir, coderSvc, skillsDir).
				WithMemory(memStore).
				WithVault(vlt)

			textHandler := func(ctx context.Context, userID string, history []db.ChatMessage, text string, send func(string)) error {
				sysCtx := buildUserContext(database, memStore, userID)
				result, err := coderSvc.Chat(ctx, userID, history, sysCtx, text)
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
			agentRunHandler := func(ctx context.Context, userID, agentName string, send func(string)) error {
				return runner.RunByName(ctx, userID, agentName, "", send)
			}

			router := gateway.NewRouter(database, textHandler, agentRunHandler, designFlow, memStore)
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

			reminderSvc := reminder.New(database, gwManager).WithReflector(vlt.Reflector())
			go reminderSvc.Run(ctx)

			sessionSvc := session.New(database).WithReflector(vlt.Reflector())
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
							if !d.IsEdit && d.State == "verifying" && d.AgentID != "" {
								_ = os.RemoveAll(agentdesigner.AgentDir(vaultsDir, d.UserID, d.AgentID))
							}
							_ = database.DeleteAgentDraft(d.UserID)
						}
					}
				}
			}()

			srv, err := web.NewServer(cfg, database, gwManager, runner, designer, homesDir, memStore, skillStore, designFlow)
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
		Name:  "admin",
		Usage: "Admin management commands",
		Commands: []*cli.Command{
			{
				Name:  "bootstrap",
				Usage: "Create the initial admin account",
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

					u, err := auth.BootstrapAdmin(database, cmd.String("username"), cmd.String("password"))
					if err != nil {
						return err
					}
					fmt.Printf("Admin account created: %s (id: %s)\n", u.Username, u.ID)
					return nil
				},
			},
		},
	}
}

// buildUserContext assembles a system context string for the coder that includes
// the user's persistent memory, their agents, and their enabled MCP tools.
func buildUserContext(database *db.DB, memStore interface{ ContextString(string) (string, error) }, userID string) string {
	var sb strings.Builder

	if p := profile.Load(database, userID).ContextString(); p != "" {
		sb.WriteString(p)
	}

	if mem, err := memStore.ContextString(userID); err == nil && mem != "" {
		sb.WriteString("[User memory]\n")
		sb.WriteString(mem)
		sb.WriteByte('\n')
	}

	if agents, err := database.ListAgents(userID); err == nil && len(agents) > 0 {
		sb.WriteString("[User's agents]\n")
		for _, a := range agents {
			sb.WriteString("- ")
			sb.WriteString(a.Name)
			if a.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(a.Description)
			}
			sb.WriteByte('\n')
		}
	}

	if mcpServers, err := database.ListMCPServers(userID); err == nil && len(mcpServers) > 0 {
		sb.WriteString("[User's MCP tools]\n")
		for _, s := range mcpServers {
			if s.Enabled {
				sb.WriteString("- ")
				sb.WriteString(s.Name)
				sb.WriteByte('\n')
			}
		}
	}

	return sb.String()
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
