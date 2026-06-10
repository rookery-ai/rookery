package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/scheduler"
	"github.com/ilijad1/simple-agents/internal/secrets"
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
			coderSvc := coder.New(cfg.Coder.ClaudeBin, cfg.Coder.Timeout, homesDir)

			agentsDir := filepath.Join(cfg.Data.Dir, "agents")
			designer := agentdesigner.NewDesigner(database, agentsDir)
			designFlow := agentdesigner.NewFlow(coderSvc, designer)
			runner := agentrunner.New(database, sysKey, agentsDir)

			textHandler := func(ctx context.Context, userID, text string, send func(string)) error {
				result, err := coderSvc.Chat(ctx, userID, "", text)
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

			router := gateway.NewRouter(database, textHandler, agentRunHandler, designFlow)
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

			srv, err := web.NewServer(cfg, database, gwManager, runner, designFlow)
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
