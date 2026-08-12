package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rookery-ai/rookery/internal/backup"
	"github.com/rookery-ai/rookery/internal/config"
	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/rookery-ai/rookery/migrations"
)

// readPassphrase reads the envelope passphrase from the terminal, or from
// stdin when --passphrase-stdin is given. It is never a flag: flags land in
// shell history and in ps output.
//
// Terminal echo is disabled with stty rather than golang.org/x/term, which is
// only a module-graph entry here — pulling it in would add a dependency for one
// call. If stty is unavailable the passphrase is still read, just visibly; that
// degrades privacy on screen but never correctness.
func readPassphrase(stdinMode bool) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	if stdinMode {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read passphrase from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "Passphrase: ")
	restore := disableEcho()
	line, err := reader.ReadString('\n')
	restore()
	fmt.Fprintln(os.Stderr)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// disableEcho turns terminal echo off and returns a function restoring it.
// Both calls are best-effort: a non-tty simply keeps its default behaviour.
func disableEcho() func() {
	if _, err := exec.LookPath("stty"); err != nil {
		return func() {}
	}
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if err := off.Run(); err != nil {
		return func() {}
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = os.Stdin
		_ = on.Run()
	}
}

// localDestFor resolves the destination used by the CLI. The CLI targets a
// local directory only; remote destinations are configured in settings and
// used by the scheduler.
func localDestFor(cmd *cli.Command, cfg *config.Config) backup.Destination {
	dir := cmd.String("dir")
	if dir == "" {
		dir = backup.DefaultLocalDir(cfg.Data.Dir)
	}
	return backup.NewLocalDestination(dir)
}

func openDBReadOnly(cfg *config.Config) (*sql.DB, error) {
	database, err := sql.Open("sqlite", cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return database, nil
}

// openSnapshot resolves the argument as a filesystem path first, then as a
// snapshot name in the backup directory.
func openSnapshot(ctx context.Context, cmd *cli.Command, cfg *config.Config) (io.ReadCloser, error) {
	arg := cmd.Args().First()
	if arg == "" {
		return nil, errors.New("a snapshot file or name is required")
	}
	if f, err := os.Open(arg); err == nil {
		return f, nil
	}
	return localDestFor(cmd, cfg).Get(ctx, arg)
}

// binarySchemaVersion reports the newest migration this build ships, which is
// what a snapshot's schema version is compared against. It reads the embedded
// set, so it does not depend on the working directory or the install layout.
func binarySchemaVersion() (string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return "", fmt.Errorf("read embedded migrations: %w", err)
	}
	newest := ""
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".up.sql") && name > newest {
			newest = name
		}
	}
	if newest == "" {
		return "", errors.New("no migrations found")
	}
	return newest, nil
}

// systemKeyFor resolves the install's system key, deciding first-run derivation
// from whether any workspace already exists.
func systemKeyFor(cfg *config.Config, database *sql.DB) ([]byte, error) {
	var wsCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&wsCount); err != nil {
		return nil, fmt.Errorf("count workspaces: %w", err)
	}
	return secrets.SystemKey(cfg.Data.Dir, wsCount > 0)
}

func backupCommand() *cli.Command {
	dirFlag := &cli.StringFlag{Name: "dir", Usage: "Local backup directory (default <data_dir>/backups)"}
	stdinFlag := &cli.BoolFlag{Name: "passphrase-stdin", Usage: "Read the passphrase from stdin instead of the terminal"}

	return &cli.Command{
		Name:  "backup",
		Usage: "Snapshot and restore the whole install",
		Commands: []*cli.Command{
			{
				Name:  "now",
				Usage: "Write a snapshot immediately",
				Flags: []cli.Flag{dirFlag, stdinFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					pw, err := readPassphrase(cmd.Bool("passphrase-stdin"))
					if err != nil {
						return err
					}
					database, err := openDBReadOnly(cfg)
					if err != nil {
						return err
					}
					defer database.Close()

					sysKey, err := systemKeyFor(cfg, database)
					if err != nil {
						return err
					}

					name, err := backup.Snapshot(ctx, backup.Options{
						DB: database, DBPath: cfg.Database.Path, DataDir: cfg.Data.Dir,
						SystemKey: sysKey, Passphrase: pw,
						Destination: localDestFor(cmd, cfg),
					})
					if err != nil {
						return err
					}
					fmt.Printf("wrote %s\n", name)
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "List stored snapshots",
				Flags: []cli.Flag{dirFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					entries, err := localDestFor(cmd, cfg).List(ctx)
					if err != nil {
						return err
					}
					if len(entries) == 0 {
						fmt.Println("no snapshots")
						return nil
					}
					for _, e := range entries {
						fmt.Printf("%s  %10d bytes  %s\n", e.Name, e.Size, e.ModTime.Format("2006-01-02 15:04"))
					}
					return nil
				},
			},
			{
				Name:      "verify",
				Usage:     "Decrypt and checksum a snapshot without restoring it",
				ArgsUsage: "<file|snapshot-name>",
				Flags:     []cli.Flag{dirFlag, stdinFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					rc, err := openSnapshot(ctx, cmd, cfg)
					if err != nil {
						return err
					}
					defer rc.Close()
					pw, err := readPassphrase(cmd.Bool("passphrase-stdin"))
					if err != nil {
						return err
					}
					schema, err := binarySchemaVersion()
					if err != nil {
						return err
					}
					m, err := backup.Verify(rc, pw, schema)
					if err != nil {
						return err
					}
					fmt.Printf("ok: %d files, %d workspaces, taken %s by %s\n",
						len(m.Files), m.WorkspaceCount,
						m.CreatedAt.Format("2006-01-02 15:04"), m.AppVersion)
					return nil
				},
			},
			{
				Name:      "restore",
				Usage:     "Restore a snapshot (the server must be stopped)",
				ArgsUsage: "<file|snapshot-name>",
				Flags:     []cli.Flag{dirFlag, stdinFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					lock, err := backup.AcquireLock(cfg.Data.Dir)
					if err != nil {
						return err
					}
					defer lock.Release()

					rc, err := openSnapshot(ctx, cmd, cfg)
					if err != nil {
						return err
					}
					defer rc.Close()
					pw, err := readPassphrase(cmd.Bool("passphrase-stdin"))
					if err != nil {
						return err
					}
					schema, err := binarySchemaVersion()
					if err != nil {
						return err
					}
					if _, err := backup.StageRestore(rc, cfg.Data.Dir, pw, schema); err != nil {
						return err
					}
					if err := backup.ApplyPendingRestore(cfg.Data.Dir); err != nil {
						return err
					}
					fmt.Println("restore complete; the previous data is in .pre-restore-* under the data dir")
					return nil
				},
			},
			{
				Name:  "cancel-restore",
				Usage: "Abandon a staged restore so it does not apply on the next start",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					if !backup.HasPendingRestore(cfg.Data.Dir) {
						fmt.Println("no restore is pending")
						return nil
					}
					if err := backup.CancelRestore(cfg.Data.Dir); err != nil {
						return err
					}
					fmt.Println("pending restore cancelled")
					return nil
				},
			},
		},
	}
}
