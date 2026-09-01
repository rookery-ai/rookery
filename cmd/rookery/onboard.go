package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
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
				cfg:        cfg,
				configPath: cmd.Root().String("config"),
				auto:       cmd.Bool("yes"),
				silent:     cmd.Bool("non-interactive"),
				in:         bufio.NewReader(os.Stdin),
			}
			return o.run(cmd.String("username"), cmd.String("password"))
		},
	}
}

type onboarder struct {
	cfg *config.Config
	// configPath is the --config flag as given, forwarded so a registered
	// service reads the same configuration file this run just used. A scheduled
	// task or a systemd unit does not run in the operator's directory, so a
	// relative default would silently select something else.
	configPath string
	auto       bool // --yes: act without asking
	silent     bool // --non-interactive: report, never act on a prompt
	in         *bufio.Reader

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
	// After the host tools, because it is the same kind of decision — an
	// optional capability the install degrades without — and before the coder,
	// which is the first step that asks the owner to go and do something
	// elsewhere.
	o.stepBrowser()
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

	// Re-resolve rather than announcing success and moving on.
	//
	// A package manager that has just installed a tool frequently leaves it
	// somewhere this process's PATH does not reach — winget writes its shims to
	// a directory resolved before they existed, and Tesseract's installer does
	// not touch PATH at all. The old advice ("they appear in a NEW terminal")
	// was true and useless: it asked the user to go away and come back, and the
	// next run of setup would offer the same tools again anyway, because the
	// probe was PATH and only PATH.
	onboard.AugmentProcessPath()
	if still := onboard.MissingOn(onboard.CurrentHost()); len(still) > 0 {
		o.ok("installed")
		for _, t := range still {
			o.info("· %s is installed but could not be located; a new terminal may be needed", t.Bin)
		}
		o.later("confirm the host tools resolve in a new terminal")
		return
	}
	o.ok("installed and in use")
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

// stepService offers autostart, and delegates the registering to the same code
// `rookery service install` runs.
//
// It used to carry its own copy of the systemd logic. Sharing it is what lets
// the installers ask the question and hand the work to Go, and it means Windows
// gained autostart here without this file learning anything about Task
// Scheduler.
func (o *onboarder) stepService() error {
	o.step("Running the server")

	svc := onboard.CurrentService()
	if !svc.Managed {
		o.info("%s", svc.Note)
		o.info("Start it with: %s", svc.Foreground)
		return nil
	}

	// Already registered says one line and moves on. Re-offering something the
	// installer has already done is the whole complaint this change set exists
	// to answer.
	if installed, detail, err := autostartStatus(); err == nil && installed {
		o.ok("already starts automatically (%s)", svc.Kind)
		if detail != "" {
			for _, line := range strings.Split(strings.TrimSpace(detail), "\n") {
				o.info("%s", strings.TrimSpace(line))
			}
		}
		return nil
	}

	o.info("Rookery can start on its own when you sign in, using a %s.", svc.Kind)
	o.info("Without it, agents and reminders only run while you have it open in a terminal.")
	if !o.ask("Set that up?") {
		o.info("Skipped. Start manually with: %s", svc.Foreground)
		o.later("set up autostart: rookery service install")
		return nil
	}

	if err := installAutostart(serviceEnvFor(o.cfg, o.configPath), os.Stdout); err != nil {
		o.info("failed: %v", err)
		o.later("set up autostart: rookery service install")
		return nil
	}
	o.ok("Rookery will start automatically")
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
