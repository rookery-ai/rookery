package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rookery-ai/rookery/internal/buildinfo"
	"github.com/rookery-ai/rookery/internal/config"
	"github.com/rookery-ai/rookery/internal/onboard"
	"github.com/rookery-ai/rookery/internal/release"
)

// Uninstall and upgrade live in Go rather than beside install.sh/install.ps1.
//
// The installers do one job — fetch, verify, place, hand off to onboard — and
// that job is the same in both shell dialects. Removal is not: it has to decide
// what a package manager owns, what a service manager owns, and what is
// unrecoverable user data. That knowledge already exists here in Go, and
// install.ps1 cannot even be syntax-checked on the development host, so two
// more PowerShell files would double a surface nothing can verify. Deleting the
// wrong directory is also the one failure in this project with no recovery
// path, which is a poor thing to implement twice in languages we cannot test.

// ── uninstall ────────────────────────────────────────────────────────────────

func uninstallCmd() *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "Remove the Rookery service and binary from this machine",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "purge",
				Usage: "ALSO delete the data directory: database, vaults, system.key, backups",
			},
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "Skip the confirmation prompt",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Print what would be removed and exit without changing anything",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.Root().String("config"))
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			return runUninstall(ctx, cfg, uninstallOpts{
				purge:  cmd.Bool("purge"),
				yes:    cmd.Bool("yes"),
				dryRun: cmd.Bool("dry-run"),
				out:    os.Stdout,
				in:     os.Stdin,
			})
		},
	}
}

type uninstallOpts struct {
	purge, yes, dryRun bool
	out                io.Writer
	in                 io.Reader
}

func runUninstall(ctx context.Context, cfg *config.Config, o uninstallOpts) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate this binary: %w", err)
	}
	self, _ = filepath.EvalSymlinks(self)

	home, _ := os.UserHomeDir()
	unitPath := onboard.SystemdUnitPath(home)
	owner := onboard.OwnerOf(ctx, nil, nil, self)

	fmt.Fprintln(o.out, "Rookery uninstall")
	fmt.Fprintf(o.out, "  binary        %s\n", self)
	fmt.Fprintf(o.out, "  data          %s\n", cfg.Data.Dir)
	if owner.Managed {
		fmt.Fprintf(o.out, "  installed by  %s (package %q)\n", owner.Manager, owner.Package)
	}
	fmt.Fprintln(o.out)

	// 1. Service. Removed even under a package install: the package never wrote
	//    into the user's home, so this unit is ours either way.
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(unitPath); err == nil {
			fmt.Fprintf(o.out, "  • stop and disable the systemd user unit, and delete %s\n", unitPath)
		} else {
			fmt.Fprintln(o.out, "  • no systemd user unit installed — nothing to stop")
		}
		// Linger is deliberately left enabled: it is a user-level setting that
		// may predate Rookery and may be keeping something else alive.
		fmt.Fprintln(o.out, "  • leave `loginctl enable-linger` as it is (it may serve other services)")
	}

	// 2. Binary, unless a package owns it.
	switch {
	case owner.Managed:
		fmt.Fprintf(o.out, "  • KEEP the binary — %s owns it. Remove it with:\n      %s\n",
			owner.Manager, owner.RemoveCommand)
	default:
		fmt.Fprintf(o.out, "  • delete the binary %s\n", self)
	}

	// 3. Data.
	if o.purge {
		fmt.Fprintf(o.out, "  • DELETE the data directory %s\n", cfg.Data.Dir)
	} else {
		fmt.Fprintf(o.out, "  • keep your data at %s (pass --purge to delete it)\n", cfg.Data.Dir)
	}
	fmt.Fprintln(o.out)

	if o.dryRun {
		fmt.Fprintln(o.out, "Dry run — nothing was changed.")
		return nil
	}

	if o.purge && !o.yes {
		if err := confirmPurge(o.out, o.in, cfg.Data.Dir); err != nil {
			return err
		}
	} else if !o.yes {
		fmt.Fprint(o.out, "Continue? [y/N] ")
		if !readYes(o.in) {
			fmt.Fprintln(o.out, "Cancelled.")
			return nil
		}
	}

	// Execute, in the order printed.
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(unitPath); err == nil {
			_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", "rookery.service").Run()
			if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(o.out, "  warn: could not remove %s: %v\n", unitPath, err)
			} else {
				fmt.Fprintf(o.out, "  removed %s\n", unitPath)
			}
			_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
		}
	}

	if !owner.Managed {
		note, err := removeSelf(self)
		if err != nil {
			// On POSIX, removing a running executable is ordinary, so a failure
			// here means the binary lives somewhere this user does not own. On
			// Windows the running image cannot be deleted at all, which is why
			// removeSelf is per-platform — and why the advice below must not
			// say "privileges", which is the wrong diagnosis there.
			fmt.Fprintf(o.out, "  warn: could not remove %s: %v\n", self, err)
			fmt.Fprintf(o.out, "  %s\n", removeSelfHint(self))
		} else {
			fmt.Fprintf(o.out, "  removed %s\n", self)
			if note != "" {
				fmt.Fprintf(o.out, "  %s\n", note)
			}
		}
	}

	if o.purge {
		if err := os.RemoveAll(cfg.Data.Dir); err != nil {
			return fmt.Errorf("remove data directory: %w", err)
		}
		fmt.Fprintf(o.out, "  removed %s\n", cfg.Data.Dir)
	}

	fmt.Fprintln(o.out, "\nDone.")
	if !o.purge {
		fmt.Fprintf(o.out, "Your data is still at %s — reinstalling will pick it up.\n", cfg.Data.Dir)
	}
	return nil
}

// confirmPurge requires the user to type the data directory's path back.
//
// Not "y". The entire risk is someone purging a directory they did not realise
// was the live one, and a single keystroke does not distinguish "I read that
// path" from "I pressed y". The sentence about system.key is the same fact the
// config data-dir warning has to state, for the same reason: it is the one
// thing here that cannot be undone by restoring a file from somewhere else.
func confirmPurge(out io.Writer, in io.Reader, dataDir string) error {
	fmt.Fprintln(out, "--purge deletes, permanently:")
	fmt.Fprintln(out, "  · the database — every workspace, agent, chat and schedule")
	fmt.Fprintln(out, "  · every workspace's knowledge base")
	fmt.Fprintln(out, "  · local backups")
	fmt.Fprintln(out, "  · system.key, which encrypts every stored master password, connector")
	fmt.Fprintln(out, "    token and bot token. It is not derivable from anything else, so a copy")
	fmt.Fprintln(out, "    of the database taken beforehand is useless without it.")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Type the data directory to confirm (%s): ", dataDir)

	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		return fmt.Errorf("cancelled")
	}
	if strings.TrimSpace(sc.Text()) != dataDir {
		return fmt.Errorf("that did not match %s — nothing was removed", dataDir)
	}
	return nil
}

func readYes(in io.Reader) bool {
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "y", "yes":
		return true
	}
	return false
}

// ── upgrade ──────────────────────────────────────────────────────────────────

func upgradeCmd() *cli.Command {
	return &cli.Command{
		Name:  "upgrade",
		Usage: "Upgrade this Rookery binary to the latest or a named release",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "version",
				Usage: "Install this tag (e.g. v0.1.4) instead of the latest release",
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "Report whether an upgrade is available and exit non-zero if one is",
			},
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "Skip the confirmation prompt",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runUpgrade(ctx, upgradeOpts{
				version: cmd.String("version"),
				check:   cmd.Bool("check"),
				yes:     cmd.Bool("yes"),
				out:     os.Stdout,
				in:      os.Stdin,
			})
		},
	}
}

type upgradeOpts struct {
	version    string
	check, yes bool
	out        io.Writer
	in         io.Reader
}

func runUpgrade(ctx context.Context, o upgradeOpts) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate this binary: %w", err)
	}
	self, _ = filepath.EvalSymlinks(self)

	// Refuse under a package manager for the same reason uninstall does:
	// replacing a packaged file behind the package database's back leaves it
	// describing something that is no longer there.
	if owner := onboard.OwnerOf(ctx, nil, nil, self); owner.Managed {
		return fmt.Errorf("this Rookery was installed by %s — upgrade it with:\n    %s",
			owner.Manager, strings.Replace(owner.RemoveCommand, "remove", "upgrade", 1))
	}

	current := buildinfo.Version
	target := o.version
	if target == "" {
		target, err = release.Latest(ctx)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(o.out, "installed %s\nlatest    %s\n", current, target)
	if sameVersion(current, target) {
		fmt.Fprintln(o.out, "\nAlready up to date.")
		return nil
	}
	if o.check {
		// Non-zero so a cron line can act on it without parsing output.
		return cli.Exit(fmt.Sprintf("an upgrade is available: %s → %s", current, target), 1)
	}
	if isDowngrade(current, target) {
		// Migrations are forward-only, so an older binary may not understand a
		// database a newer one has already migrated. Allowed, but never silent.
		fmt.Fprintf(o.out, "\nWARNING: %s is OLDER than the installed %s. Migrations are\n"+
			"forward-only, so a database already migrated by the newer build may not open.\n",
			target, current)
	}
	if !o.yes {
		fmt.Fprintf(o.out, "\nReplace %s with %s? [y/N] ", self, target)
		if !readYes(o.in) {
			fmt.Fprintln(o.out, "Cancelled.")
			return nil
		}
	}

	archive := release.CurrentArchiveName(target)
	fmt.Fprintf(o.out, "\nDownloading %s\n", archive)
	data, err := fetch(ctx, release.ArchiveURL(target, runtime.GOOS, runtime.GOARCH))
	if err != nil {
		return fmt.Errorf("download %s: %w", archive, err)
	}

	sums, err := fetch(ctx, release.ChecksumsURL(target))
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	parsed, err := release.ParseChecksums(strings.NewReader(string(sums)))
	if err != nil {
		return err
	}
	want, ok := parsed[archive]
	if !ok {
		return &release.ErrNoAsset{Version: target, OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
	if err := release.Verify(data, want); err != nil {
		return err
	}
	fmt.Fprintln(o.out, "Checksum verified.")

	bin, err := extractBinary(data, archive)
	if err != nil {
		return err
	}

	// Write beside the target and rename. An interrupted upgrade must never
	// leave a half-written executable on PATH — rename is atomic within a
	// filesystem, and writing to the same directory keeps it one.
	if err := replaceBinary(self, bin); err != nil {
		return err
	}
	fmt.Fprintf(o.out, "Replaced %s\n", self)

	// Report what the binary on disk now SAYS it is, rather than what we
	// intended to install. An upgrade that silently left the old one in place
	// is the failure worth spending a check on.
	if out, err := exec.CommandContext(ctx, self, "version").Output(); err == nil {
		fmt.Fprintf(o.out, "Now running: %s", out)
	}
	// The restart command comes from the platform, never from here. Printing
	// systemctl on macOS and Windows told people to run a command that does not
	// exist; hardcoding it behind `if svc.Managed` fixed that only for as long
	// as Linux was the one managed platform, and Windows is now another.
	if svc := onboard.CurrentService(); svc.Managed && svc.Restart != "" {
		fmt.Fprintf(o.out, "\nRestart the service to pick it up:\n    %s\n", svc.Restart)
	} else {
		fmt.Fprintf(o.out, "\nRestart the server to pick it up (stop the running one, then `%s`).\n", svc.Foreground)
	}
	return nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := release.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// sameVersion compares tags tolerantly: buildinfo.Version may carry a leading
// v, a -dev suffix, or the literal "dev" in a local build.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}
