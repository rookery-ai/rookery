package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rookery-ai/rookery/internal/skilldeps"
	"github.com/urfave/cli/v3"
)

// depsRunner builds the runner a CLI coder's `rookery deps` uses. It runs INSIDE the
// coder's own sandbox, where config.yaml is unreadable, so the sandbox switch comes from
// ROOKERY_SANDBOX alone and defaults ON — nesting is safe because Landlock stacks: the
// install/run sandbox can only narrow what the coder already had.
func depsRunner() skilldeps.Runner {
	self, _ := os.Executable()
	v := os.Getenv("ROOKERY_SANDBOX")
	on := !(v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off"))
	return skilldeps.Runner{SelfExe: self, Sandbox: on, Checker: skilldeps.NewOSVClient("", nil)}
}

func depsCmd() *cli.Command {
	return &cli.Command{
		Name:  "deps",
		Usage: "Install and run a skill's Python dependencies in a contained environment (used by CLI coders)",
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install packages into a named environment: deps install --env <name> --package <spec> [--package <spec>]…",
				// A version range ("pandas>=3.0,<4") is ONE requirement; urfave/cli would
				// otherwise split it on the comma. Repeat --package for several.
				DisableSliceFlagSeparator: true,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "env", Usage: "environment name (usually the skill's name)", Required: true},
					&cli.StringSliceFlag{Name: "package", Usage: "a requirement: name[==version] or an https/git+https URL", Required: true},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if skilldeps.SkillBuildActive(os.Getenv) {
						return skilldeps.ErrSkillBuild
					}
					res, err := depsRunner().Install(ctx, skilldeps.InstallRequest{
						Home:  os.Getenv("HOME"),
						Env:   cmd.String("env"),
						Specs: cmd.StringSlice("package"),
					})
					if err != nil {
						return err
					}
					fmt.Printf("installed %s: %d packages (malware check: %s)\n", res.Env, len(res.Resolved), res.MalwareCheck)
					fmt.Print(res.Output)
					return nil
				},
			},
			{
				Name:      "run",
				Usage:     "Run a script with an environment's interpreter: deps run --env <name> [--secret NAME]… -- <script> [args…]",
				ArgsUsage: "-- <script> [args…]",
				// Secret names never contain commas; kept consistent with install.
				DisableSliceFlagSeparator: true,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "env", Usage: "environment name", Required: true},
					&cli.StringSliceFlag{Name: "secret", Usage: "name of a secret to pass to the script (repeatable); no other secret reaches it"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args().Slice()
					if len(args) == 0 {
						return fmt.Errorf("name the script to run after --")
					}
					secrets := map[string]string{}
					for _, name := range cmd.StringSlice("secret") {
						v, ok := os.LookupEnv(name)
						if !ok {
							return fmt.Errorf("secret %s is not set in this run", name)
						}
						secrets[name] = v
					}
					wd, _ := os.Getwd()
					stdout, stderr, err := depsRunner().Run(ctx, skilldeps.RunRequest{
						Home:      os.Getenv("HOME"),
						Env:       cmd.String("env"),
						WorkDir:   wd,
						VaultRoot: os.Getenv("ROOKERY_VAULT_ROOT"),
						Script:    args[0],
						Args:      args[1:],
						Secrets:   secrets,
					})
					fmt.Print(stdout)
					fmt.Fprint(os.Stderr, stderr)
					return err
				},
			},
		},
	}
}
