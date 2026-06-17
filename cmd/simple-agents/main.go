package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
	"github.com/ilijad1/simple-agents/internal/scheduler"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/session"
	"github.com/ilijad1/simple-agents/internal/skillstore"
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
			coderSvc := coder.New(cfg.Coder.ClaudeBin, cfg.Coder.Timeout, homesDir, cfg.Data.Dir)

			agentsDir := filepath.Join(cfg.Data.Dir, "agents")
			skillsDir := filepath.Join(cfg.Data.Dir, "skills")
			designer := agentdesigner.NewDesigner(database, agentsDir)
			// Telegram uses a single system coder; wrap it in a resolver lambda.
			designFlow := agentdesigner.NewFlow(func(_ string) *coder.Coder { return coderSvc }, designer).WithDB(database)
			skillStore := skillstore.New(database, skillsDir)
			runner := agentrunner.New(database, sysKey, agentsDir, homesDir, cfg.Data.Dir, coderSvc, skillsDir)

			memStore := memory.New(filepath.Join(cfg.Data.Dir, "memory"))

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

			reminderSvc := reminder.New(database, gwManager)
			go reminderSvc.Run(ctx)

			sessionSvc := session.New(database)
			go sessionSvc.Run(ctx)

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
