package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// Setup stopped at the first failed install. winget ids sort uppercase first,
// so UB-Mannheim.TesseractOCR ran before oschwartz10612.Poppler, and a missed
// Tesseract permission prompt silently skipped Poppler as well.
func TestInstallStepsContinuePastAFailure(t *testing.T) {
	steps := onboard.InstallSteps(onboard.ManagerWinget, onboard.HostTools)
	var ran []string
	failed := runInstallSteps(steps, func(cmd string) error {
		ran = append(ran, cmd)
		if strings.Contains(cmd, "TesseractOCR") {
			return errors.New("exit status 1")
		}
		return nil
	})
	if len(ran) != len(steps) {
		t.Fatalf("ran %d of %d steps; a failure must not skip the rest", len(ran), len(steps))
	}
	if len(failed) != 1 || !strings.Contains(failed[0].Command, "TesseractOCR") {
		t.Fatalf("failed = %+v, want exactly the Tesseract step", failed)
	}
}

func TestAdminNoticeOnlyWhenAStepNeedsIt(t *testing.T) {
	win := onboard.InstallSteps(onboard.ManagerWinget, onboard.HostTools)
	notice := strings.Join(adminNotice(win), "\n")
	for _, want := range []string{"tesseract", "administrator", "accept"} {
		if !strings.Contains(strings.ToLower(notice), want) {
			t.Errorf("notice %q does not mention %q", notice, want)
		}
	}

	var noAdmin []onboard.InstallStep
	for _, s := range win {
		if !s.NeedsAdmin {
			noAdmin = append(noAdmin, s)
		}
	}
	if got := adminNotice(noAdmin); len(got) != 0 {
		t.Errorf("no step needs admin, but a notice was printed: %v", got)
	}
	if got := adminNotice(onboard.InstallSteps(onboard.ManagerDNF, onboard.HostTools)); len(got) != 0 {
		t.Errorf("Linux printed a Windows admin notice: %v", got)
	}
}

// A failed elevated install has to say what probably happened and hand over
// the exact command, because "failed: … (exit status 1)" names nothing.
func TestFailureAdviceForAnElevatedStep(t *testing.T) {
	var step onboard.InstallStep
	for _, s := range onboard.InstallSteps(onboard.ManagerWinget, onboard.HostTools) {
		if s.NeedsAdmin {
			step = s
		}
	}
	advice := strings.Join(failureAdvice(step, errors.New("exit status 1")), "\n")
	for _, want := range []string{"permission", "Run as administrator", step.Command} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice %q lacks %q", advice, want)
		}
	}

	plain := onboard.InstallStep{Command: "winget install -e --id BurntSushi.ripgrep.MSVC"}
	if got := strings.Join(failureAdvice(plain, errors.New("boom")), "\n"); strings.Contains(got, "administrator") {
		t.Errorf("a non-elevated failure must not blame permissions: %q", got)
	}
}
