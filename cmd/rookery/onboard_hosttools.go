package main

import (
	"fmt"
	"strings"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// runInstallSteps runs every step and returns the ones that failed.
//
// It deliberately does not stop at the first failure. winget ids sort with
// uppercase first, so UB-Mannheim.TesseractOCR — the one install that needs a
// Windows permission prompt — ran before oschwartz10612.Poppler, and a missed
// prompt used to skip Poppler too, with nothing saying so.
func runInstallSteps(steps []onboard.InstallStep, run func(string) error) []onboard.InstallStep {
	var failed []onboard.InstallStep
	for _, s := range steps {
		if err := run(s.Command); err != nil {
			failed = append(failed, s)
		}
	}
	return failed
}

// adminNotice is printed BEFORE the installs run, when one of them will raise a
// Windows permission prompt. Said up front because the prompt can open behind
// the console, and a prompt nobody expected is the one that gets dismissed.
func adminNotice(steps []onboard.InstallStep) []string {
	var names []string
	for _, s := range steps {
		if !s.NeedsAdmin {
			continue
		}
		for _, t := range s.Tools {
			names = append(names, t.Bin)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []string{
		fmt.Sprintf("%s installs for all users, so Windows will ask for administrator permission.", strings.Join(names, ", ")),
		"Accept the prompt when it appears — it may open behind this window. Everything else installs without it.",
	}
}

// failureAdvice explains one failed step. An elevated step most likely failed
// because the permission prompt was missed or declined, and the fix is a
// command the user can run themselves — so it names both.
func failureAdvice(s onboard.InstallStep, err error) []string {
	lines := []string{fmt.Sprintf("failed: %s (%v)", s.Command, err)}
	if !s.NeedsAdmin {
		return lines
	}
	return append(lines,
		"  This one needs Windows administrator permission; the prompt was probably missed or declined.",
		"  To install it, open PowerShell with \"Run as administrator\" and run:",
		"    "+s.Command,
		"  Rookery itself works without it; only OCR of scanned documents and images is affected.",
	)
}
