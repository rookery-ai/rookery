// Package health builds the capability report served at /healthz and logged at
// startup. It exists because several runtime dependencies degrade SILENTLY when
// absent — most importantly python3, whose absence makes the agent-tool AST
// guardrail self-skip (internal/agentdesigner/guardrails.go). On a developer
// machine that reads as a skipped test; on a shipped install it is a security
// control switching itself off. This package makes that visible.
package health

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/ilijad1/simple-agents/internal/buildinfo"
	"github.com/ilijad1/simple-agents/internal/sandbox"
)

// Tools reports presence only — never paths, never versions. /healthz is
// unauthenticated, so it must not disclose filesystem layout.
type Tools struct {
	Python3   bool `json:"python3"`
	Ripgrep   bool `json:"rg"`
	PDFToText bool `json:"pdftotext"`
	Tesseract bool `json:"tesseract"`
}

// Sandbox separates the operator's setting (Enabled) from the kernel's answer
// (Supported). Collapsing them would hide the "configured on but inactive" case.
type Sandbox struct {
	Supported bool `json:"supported"`
	Enabled   bool `json:"enabled"`
	ABI       int  `json:"abi"`
}

// Report is the /healthz body and the startup log's source.
type Report struct {
	Status    string  `json:"status"`
	Version   string  `json:"version"`
	Commit    string  `json:"commit"`
	Sandbox   Sandbox `json:"sandbox"`
	CoderMode string  `json:"coder_mode"`
	Tools     Tools   `json:"tools"`
}

// Detect probes the host. It is cheap (four PATH lookups plus one syscall) but
// not free, so callers should not put it on a hot path.
func Detect(sandboxEnabled bool, coderMode string) Report {
	return Report{
		Status:  "ok",
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Sandbox: Sandbox{
			Supported: sandbox.Supported(),
			Enabled:   sandboxEnabled,
			ABI:       sandbox.ABI(),
		},
		CoderMode: coderMode,
		Tools: Tools{
			Python3:   have("python3"),
			Ripgrep:   have("rg"),
			PDFToText: have("pdftotext"),
			Tesseract: have("tesseract"),
		},
	}
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// Warnings returns the degradations worth telling a human about. Order is
// stable: security-affecting first.
func (r Report) Warnings() []string {
	var w []string
	if !r.Tools.Python3 {
		w = append(w, "python3 not found — the agent-tool AST guardrail is INACTIVE; "+
			"generated tool scripts are not statically checked before they run")
	}
	if !r.Sandbox.Supported {
		w = append(w, fmt.Sprintf("filesystem sandbox unavailable on %s — coder "+
			"subprocesses run unconfined (Landlock is Linux-only)", runtime.GOOS))
	} else if !r.Sandbox.Enabled {
		w = append(w, "filesystem sandbox is supported but DISABLED (SA_SANDBOX) — "+
			"coder subprocesses run unconfined")
	}
	if !r.Tools.Ripgrep {
		w = append(w, "rg not found — knowledge-base search falls back to the slower pure-Go searcher")
	}
	if !r.Tools.PDFToText {
		w = append(w, "pdftotext not found — PDF text extraction uses the weaker pure-Go fallback")
	}
	if !r.Tools.Tesseract {
		w = append(w, "tesseract not found — image OCR is unavailable")
	}
	return w
}
