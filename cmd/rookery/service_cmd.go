package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rookery-ai/rookery/internal/config"
	"github.com/rookery-ai/rookery/internal/onboard"
	"github.com/urfave/cli/v3"
)

// `rookery service` registers Rookery to start on its own, and is the reason
// the installers do not do it themselves.
//
// The split is drawn where the knowledge is. External dependencies — python3,
// ripgrep, Poppler, Tesseract — are ordinary OS packages, and install.sh and
// install.ps1 install them directly through the host's own package manager.
// Autostart is not a dependency: it is Rookery's own configuration, and
// expressing it means generating a systemd unit or a Task Scheduler document
// against the binary's real path. Writing that twice, in two shell dialects
// neither of which CI can exercise and one of which is not even syntax-checked
// here, would put the platform knowledge in the two worst places to keep it.
//
// It also has to serve the operator who installed from an rpm, a deb or a
// tarball and never ran either script.
func serviceCmd() *cli.Command {
	return &cli.Command{
		Name:  "service",
		Usage: "Start Rookery automatically when you sign in",
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Register Rookery to start automatically, and start it now",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					env, err := serviceEnvFrom(cmd)
					if err != nil {
						return err
					}
					if err := installAutostart(env, os.Stdout); err != nil {
						return err
					}
					fmt.Printf("Rookery will start automatically (%s).\n", onboard.CurrentService().Kind)
					return nil
				},
			},
			{
				Name:  "uninstall",
				Usage: "Stop Rookery starting automatically",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return uninstallAutostart(os.Stdout)
				},
			},
			{
				Name:  "status",
				Usage: "Report whether Rookery is registered to start automatically",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					svc := onboard.CurrentService()
					if !svc.Managed {
						fmt.Println(svc.Note)
						fmt.Printf("Run it by hand with: %s\n", svc.Foreground)
						return nil
					}
					installed, detail, err := autostartStatus()
					if err != nil {
						return err
					}
					if !installed {
						fmt.Printf("not registered — install it with `rookery service install`\n")
						return nil
					}
					fmt.Printf("registered (%s)\n", svc.Kind)
					if detail != "" {
						fmt.Println(detail)
					}
					return nil
				},
			},
		},
	}
}

// serviceEnv is what registering autostart needs to know: which binary to
// start, where its data lives, and which configuration file it should read.
type serviceEnv struct {
	binary     string
	dataDir    string
	configPath string
}

func serviceEnvFrom(cmd *cli.Command) (serviceEnv, error) {
	cfg, err := config.Load(cmd.Root().String("config"))
	if err != nil {
		return serviceEnv{}, fmt.Errorf("load config: %w", err)
	}
	return serviceEnvFor(cfg, cmd.Root().String("config")), nil
}

// serviceEnvFor resolves the environment, and is separate from the command so
// onboard's own service step reaches the identical logic rather than a second
// copy of it.
func serviceEnvFor(cfg *config.Config, configFlag string) serviceEnv {
	// The binary that is running this command, not the name `rookery`. Someone
	// who installed via install.sh has it in ~/.local/bin, and a unit or task
	// naming a binary that is not there starts nothing — the same reason
	// onboard.UnitFileFor generates against the running binary rather than
	// copying the packaged unit, which hardcodes /usr/bin/rookery.
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "rookery"
	}

	return serviceEnv{
		binary:     self,
		dataDir:    cfg.Data.Dir,
		configPath: resolvedConfigPath(configFlag),
	}
}

// resolvedConfigPath returns an absolute path to the configuration file, or ""
// when there is no file to name.
//
// The root --config flag defaults to a RELATIVE "config.yaml", and neither a
// systemd unit nor a scheduled task runs in the directory the operator
// installed from. Passing the flag through unchanged would therefore select
// different configuration, or none, with nothing said. Naming a file that does
// not exist would be worse than saying nothing at all, so the flag is only
// forwarded when it resolves to something real.
func resolvedConfigPath(flag string) string {
	if flag == "" {
		return ""
	}
	abs, err := filepath.Abs(flag)
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		return ""
	}
	return abs
}
