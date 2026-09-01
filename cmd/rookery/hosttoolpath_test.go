package main

import (
	"os"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/sandbox"
)

func TestOrdinaryCommandsAugmentThePath(t *testing.T) {
	for _, args := range [][]string{
		{"rookery", "serve"},
		{"rookery", "onboard"},
		{"rookery", "healthcheck"},
		{"rookery", "-c", "config.yaml", "serve"},
		{"rookery"},
	} {
		if !shouldAugmentHostToolPath(args) {
			t.Errorf("%v should augment PATH", args)
		}
	}
}

// Both helpers exist to re-exec something they were handed under an environment
// their caller built. Changing it underneath them is not this function's job,
// and the parent has already augmented anyway.
func TestTheHiddenHelpersDoNotAugmentThePath(t *testing.T) {
	if shouldAugmentHostToolPath([]string{"rookery", sandbox.HelperCommand, "<spec>"}) {
		t.Error("the sandbox helper must not have its environment altered")
	}
	if shouldAugmentHostToolPath([]string{"rookery", browser.HostCommand}) {
		t.Error("the browser host must not have its environment altered")
	}
}

// The wiring is the load-bearing half, and it is invisible from every unit test
// that exercises the pieces.
//
// internal/health's agreement test proves setup and /healthz give the same
// answer only AFTER augmentation has run; between process start and that call
// they genuinely differ, because the resolver knows about directories PATH does
// not. So this call happening before anything else is what keeps that window
// closed in a real process. An earlier draft of the health test asserted
// agreement before augmenting and failed on all four tools — that is what this
// is guarding.
func TestMainAugmentsThePathBeforeDoingAnythingElse(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	call := strings.Index(body, "augmentHostToolPath(os.Args)")
	if call < 0 {
		t.Fatal("main no longer augments PATH; setup and /healthz will disagree about host tools")
	}
	app := strings.Index(body, "app := &cli.Command{")
	if app >= 0 && call > app {
		t.Error("PATH augmentation must run before the command tree is built and dispatched")
	}
}
