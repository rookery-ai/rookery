package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rookery-ai/rookery/internal/auth"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/config"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/onboard"
	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/urfave/cli/v3"
)

// onboardCmd walks a fresh install to a running server.
//
// It exists because installing the binary is the easy half. The half that
// actually loses people is everything after: which command creates the owner,
// where the data lives, which of the two keys matters if the machine dies, and
// how to make the thing start again after a reboot. install.sh and install.ps1
// deliberately do none of that — this is written once, in Go, and serves the
// operator who installed from an rpm, a deb or a tarball and never ran a script.
func onboardCmd() *cli.Command {
	return &cli.Command{
		Name:  "onboard",
		Usage: "Set up this install: owner account, keys, host tools, service and first run",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "non-interactive",
				Usage: "Never prompt; report what to do instead of doing it",
			},
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "Answer yes to every prompt (installs host tools and the service without asking)",
			},
			&cli.StringFlag{Name: "username", Aliases: []string{"u"}, Usage: "Owner username (skips the prompt)"},
			&cli.StringFlag{Name: "password", Aliases: []string{"p"}, Usage: "Owner password (skips the prompt)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.Root().String("config"))
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			o := &onboarder{
				cfg:    cfg,
				auto:   cmd.Bool("yes"),
				silent: cmd.Bool("non-interactive"),
				in:     bufio.NewReader(os.Stdin),
			}
			return o.run(cmd.String("username"), cmd.String("password"))
		},
	}
}

type onboarder struct {
	cfg    *config.Config
	auto   bool // --yes: act without asking
	silent bool // --non-interactive: report, never act on a prompt
	in     *bufio.Reader

	// Collected as we go, and replayed in the closing summary. A setup that
	// skipped three things and then says "Done" has told the user nothing.
	todo []string
}

func (o *onboarder) run(username, password string) error {
	o.banner()

	if err := o.stepKeys(); err != nil {
		return err
	}
	if err := o.stepOwner(username, password); err != nil {
		return err
	}
	o.stepHostTools()
	o.stepCoder()
	if err := o.stepService(); err != nil {
		return err
	}
	o.finish()
	return nil
}

// ── presentation ─────────────────────────────────────────────────────────────

func (o *onboarder) banner() {
	fmt.Println()
	fmt.Println("  Rookery setup")
	fmt.Printf("  %s/%s · data in %s\n", runtime.GOOS, runtime.GOARCH, o.cfg.Data.Dir)
	fmt.Println()
}

func (o *onboarder) step(title string) { fmt.Printf("\n==> %s\n", title) }
func (o *onboarder) info(format string, a ...any) {
	fmt.Printf("    "+format+"\n", a...)
}
func (o *onboarder) ok(format string, a ...any) {
	fmt.Printf("    ✓ "+format+"\n", a...)
}
func (o *onboarder) later(what string) { o.todo = append(o.todo, what) }

// ask returns true when the user consents. --yes consents to everything;
// --non-interactive consents to nothing and never blocks. A prompt with no
// terminal behind it is a hang, which is worse than a skipped step.
func (o *onboarder) ask(question string) bool {
	if o.auto {
		return true
	}
	if o.silent || !isTerminal(os.Stdin) {
		return false
	}
	fmt.Printf("    %s [y/N] ", question)
	line, err := o.in.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func (o *onboarder) prompt(label string) (string, error) {
	fmt.Printf("    %s: ", label)
	line, err := o.in.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptSecret reads without echoing. `stty` is shelled out to rather than
// pulling in golang.org/x/term for one call — the same choice the backup CLI
// already made, and this file should not disagree with it.
func (o *onboarder) promptSecret(label string) (string, error) {
	fmt.Printf("    %s: ", label)
	restore := disableEcho()
	line, err := o.in.ReadString('\n')
	restore()
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ── steps ────────────────────────────────────────────────────────────────────

// stepKeys resolves both keys so they exist on disk before anything encrypts
// anything with them, and explains which one actually matters.
//
// This step is mostly education, and deliberately so. The two keys are commonly
// confused, and the confusion is expensive in exactly one direction: someone who
// believes the system key is disposable discovers otherwise when a restore on
// new hardware produces an install that boots, looks healthy, and has silently
// lost every connector token and every scheduled agent.
func (o *onboarder) stepKeys() error {
	o.step("Keys")

	if err := os.MkdirAll(o.cfg.Data.Dir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	dbExists := false
	if _, err := os.Stat(o.cfg.Database.Path); err == nil {
		dbExists = true
	}

	if _, err := secrets.SystemKey(o.cfg.Data.Dir, dbExists); err != nil {
		return fmt.Errorf("resolve system key: %w", err)
	}
	if _, err := secrets.SessionKey(o.cfg.Data.Dir, o.cfg.Server.SessionKey); err != nil {
		return fmt.Errorf("resolve session key: %w", err)
	}

	o.ok("system key   %s", secrets.SystemKeyPath(o.cfg.Data.Dir))
	o.info("encrypts connector tokens, stored master passwords and bot tokens.")
	o.info("Lose it and that data is unrecoverable even with the database.")
	o.ok("session key  %s", secrets.SessionKeyPath(o.cfg.Data.Dir))
	o.info("signs browser sessions only. Losing it costs one sign-in.")
	fmt.Println()
	o.info("You do NOT need to copy these down. `rookery backup now` puts the")
	o.info("system key inside the encrypted snapshot, which is what makes a move")
	o.info("to new hardware one step. The snapshot passphrase is the one thing")
	o.info("that cannot be recovered — keep that in your password manager.")
	return nil
}

func (o *onboarder) stepOwner(username, password string) error {
	o.step("Owner account")

	database, err := db.Open(o.cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	if existing, err := database.GetOwner(); err == nil && existing != nil {
		o.ok("already set up: %s", existing.Username)
		o.info("Forgotten the password? `rookery owner reset-password -p <new>`")
		return nil
	}

	if username == "" || password == "" {
		if o.silent || !isTerminal(os.Stdin) {
			o.info("No owner account yet. Create one with:")
			o.info("  rookery owner bootstrap -u <username> -p <password>")
			o.later("create the owner account")
			return nil
		}
		o.info("There is one owner for this install. You log in as the owner, then")
		o.info("enter a workspace with that workspace's own master password.")
		if username == "" {
			if username, err = o.prompt("Username"); err != nil {
				return err
			}
		}
		if password == "" {
			if password, err = o.promptSecret("Password"); err != nil {
				return err
			}
			confirm, err := o.promptSecret("Confirm password")
			if err != nil {
				return err
			}
			if confirm != password {
				return fmt.Errorf("passwords did not match")
			}
		}
	}
	if username == "" || password == "" {
		return fmt.Errorf("username and password are both required")
	}

	owner, err := auth.BootstrapOwner(database, username, password)
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	o.ok("created: %s", owner.Username)
	return nil
}

func (o *onboarder) stepHostTools() {
	o.step("Host tools")

	missing := onboard.Missing(nil)
	if len(missing) == 0 {
		o.ok("all present")
		return
	}

	for _, t := range missing {
		mark := " "
		if t.Critical {
			mark = "!"
		}
		o.info("%s %-10s %s", mark, t.Bin, t.Purpose)
	}

	mgr := onboard.DetectManager(nil)
	cmds := onboard.InstallCommands(mgr, missing)
	if len(cmds) == 0 {
		o.info("No supported package manager found — install these with your own tools.")
		o.later("install the missing host tools")
		return
	}

	fmt.Println()
	for _, c := range cmds {
		o.info("%s", c)
	}
	if !o.ask("Run this now?") {
		o.later("install the missing host tools")
		return
	}
	for _, c := range cmds {
		if err := runShell(c); err != nil {
			o.info("failed: %s (%v)", c, err)
			o.later("install the missing host tools")
			return
		}
	}
	o.ok("installed")
	if runtime.GOOS == "windows" {
		o.info("Newly installed tools appear on PATH in a NEW terminal, not this one.")
	}
}

// stepCoder reports rather than configures. Choosing a coder means choosing a
// provider, a model and an API key, and it is per WORKSPACE — there is no
// workspace yet at this point in setup, and inventing one to hold the answer
// would put a decision in the wrong place.
func (o *onboarder) stepCoder() {
	o.step("Coder")
	if o.cfg.Coder.Mode == config.ModeSlim {
		o.info("This build is slim: no local CLI coder. Workspaces use the `api` kind,")
		o.info("configured in the web UI with a provider, a model and an API key.")
		return
	}
	installed := detectCoderSummary()
	if installed == "" {
		o.info("No CLI coder found on PATH. That is fine — a workspace can use the")
		o.info("`api` kind instead, which talks to a provider directly and needs no")
		o.info("binary. Set either one up in Settings once you are signed in.")
		return
	}
	o.ok("found: %s", installed)
	o.info("Pick one per workspace in Settings, or use the `api` kind instead.")
}

func (o *onboarder) stepService() error {
	o.step("Running the server")

	svc := onboard.CurrentService()
	if !svc.Managed {
		o.info("%s", svc.Note)
		o.info("Start it with: %s", svc.Foreground)
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		o.info("Cannot resolve your home directory; start manually with: %s", svc.Foreground)
		return nil
	}
	unitPath := onboard.SystemdUnitPath(home)
	if _, err := os.Stat(unitPath); err == nil {
		o.ok("systemd user unit already installed: %s", unitPath)
		o.info("Control it with: systemctl --user restart rookery")
		return nil
	}

	o.info("A systemd user unit starts Rookery at login and restarts it on failure.")
	if !o.ask("Install and enable it?") {
		o.info("Skipped. Start manually with: %s", svc.Foreground)
		o.later("install the systemd user unit")
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create unit dir: %w", err)
	}

	// The packaged unit hardcodes /usr/bin/rookery. Someone who installed via
	// install.sh or an archive has the binary in ~/.local/bin, so copying that
	// file would install a unit that starts nothing. Generate against the
	// binary actually running this command instead.
	self, err := os.Executable()
	if err != nil {
		self = "rookery"
	}
	unit := onboard.UnitFileFor(self, o.cfg.Data.Dir)
	if packaged, ok := onboard.FindPackagedUnit(); ok && self == "/usr/bin/rookery" {
		if b, err := os.ReadFile(packaged); err == nil {
			unit = string(b)
		}
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	o.ok("wrote %s", unitPath)

	for _, args := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", "rookery"},
	} {
		if err := runArgs(args); err != nil {
			o.info("`%s` failed: %v", strings.Join(args, " "), err)
			o.later("enable the service: systemctl --user enable --now rookery")
			return nil
		}
	}
	o.ok("service enabled")

	// Without lingering, a user unit stops when the last session closes — so a
	// headless box reboots and the scheduler never comes back. The failure is
	// invisible until an agent silently misses its schedule.
	if err := runArgs([]string{"loginctl", "enable-linger"}); err != nil {
		o.info("Could not enable lingering; the server will stop when you log out.")
		o.later("run: loginctl enable-linger")
	} else {
		o.ok("lingering enabled — survives logout and reboot")
	}
	return nil
}

func (o *onboarder) finish() {
	url := fmt.Sprintf("http://localhost:%d", o.cfg.Server.Port)
	fmt.Println()
	fmt.Println("==> Done")
	fmt.Println()
	if len(o.todo) > 0 {
		fmt.Println("    Still to do:")
		for _, t := range o.todo {
			fmt.Printf("      · %s\n", t)
		}
		fmt.Println()
	}
	fmt.Printf("    Open %s, sign in, and create your first workspace.\n", url)
	fmt.Println()
	fmt.Println("    A workspace has its own master password, vault, agents and")
	fmt.Println("    connections. You enter one by typing its password; switching")
	fmt.Println("    asks again. That is the isolation boundary.")
	fmt.Println()
}

// detectCoderSummary names the CLI coders on this host, or "" if there are none.
func detectCoderSummary() string {
	found := coder.DetectInstalled()
	if len(found) == 0 {
		return ""
	}
	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

// ── shelling out ─────────────────────────────────────────────────────────────

func runShell(command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func runArgs(args []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
