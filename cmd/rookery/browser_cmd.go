package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/onboard"
)

// browserHostCmd is the hidden helper that actually runs Chromium.
//
// Its Name is a CONSTANT rather than a string literal, which is what exempts it
// from the docs-sync CLI-coverage check — the same treatment the sandbox helper
// gets, and for the same reason: it is not a command anyone types.
func browserHostCmd() *cli.Command {
	return &cli.Command{
		Name:   browser.HostCommand,
		Hidden: true,
		Action: func(ctx context.Context, _ *cli.Command) error {
			return browser.RunHost(ctx)
		},
	}
}

func browserCmd() *cli.Command {
	return &cli.Command{
		Name:  "browser",
		Usage: "Install and use the headless browser used for JavaScript-rendered pages",
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Download the browser runtime (Node driver + Chromium, ~500 MB on disk)",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "with-deps",
						Usage: "also install the system libraries Chromium needs (requires root)",
					},
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					fmt.Println("Downloading the browser runtime. This is a few hundred megabytes and runs once.")
					if err := browser.Install(os.Stdout, cmd.Bool("with-deps")); err != nil {
						return err
					}
					av := browser.Probe()
					if !av.OK {
						return fmt.Errorf("install finished but the runtime still looks incomplete: %s", av.Reason)
					}
					fmt.Println("\nBrowser runtime installed.")
					if hint := browser.SystemDepsHint(string(onboard.DetectManager(onboard.DefaultLookPath))); hint != "" && !cmd.Bool("with-deps") {
						fmt.Println("\nChromium also needs some system libraries. If pages fail to render, run:")
						fmt.Println("  " + hint)
					}
					return nil
				},
			},
			{
				Name:  "status",
				Usage: "Report whether the browser runtime is installed",
				Action: func(_ context.Context, _ *cli.Command) error {
					av := browser.Probe()
					if av.OK {
						fmt.Println("browser runtime: installed")
						return nil
					}
					return fmt.Errorf("browser runtime: not available — %s", av.Reason)
				},
			},
			{
				Name:      "read",
				Usage:     "Read a JavaScript-rendered page through the running server (used by CLI coders)",
				ArgsUsage: "<url>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "wait", Usage: "load | networkidle | selector:<css> | text:<substring>"},
					&cli.IntFlag{Name: "offset", Usage: "byte offset into the extracted text"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					url := strings.TrimSpace(cmd.Args().First())
					if url == "" {
						return fmt.Errorf("a url is required")
					}
					return callBrowserBridge(ctx, "/read", map[string]any{
						"url":      url,
						"wait_for": cmd.String("wait"),
						"offset":   cmd.Int("offset"),
					})
				},
			},
			{
				Name:      "act",
				Usage:     "Act on the page opened by this run (used by CLI coders)",
				ArgsUsage: "<click|fill|press|wait|read>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "ref", Usage: "element ref from the page listing, e.g. e12"},
					&cli.StringFlag{Name: "value", Usage: "text to type; ${SECRET_NAME} is resolved by the server"},
					&cli.StringFlag{Name: "key", Usage: "key to press, e.g. Enter"},
					&cli.StringFlag{Name: "wait", Usage: "wait condition"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					action := strings.TrimSpace(cmd.Args().First())
					if action == "" {
						return fmt.Errorf("an action is required")
					}
					return callBrowserBridge(ctx, "/act", map[string]any{
						"action":   action,
						"ref":      cmd.String("ref"),
						"value":    cmd.String("value"),
						"key":      cmd.String("key"),
						"wait_for": cmd.String("wait"),
					})
				},
			},
		},
	}
}

// callBrowserBridge posts to the loopback bridge the server exposes for CLI
// coders, exactly as `rookery connector exec` and `rookery mcp exec` do. The URL
// and token arrive in the subprocess environment and never touch disk.
func callBrowserBridge(ctx context.Context, path string, payload map[string]any) error {
	base := os.Getenv(browser.EnvBridgeURL)
	token := os.Getenv(browser.EnvBridgeToken)
	if base == "" || token == "" {
		return fmt.Errorf("the browser bridge is not available in this context")
	}
	out, err := browser.CallBridge(ctx, base, token, path, payload)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
