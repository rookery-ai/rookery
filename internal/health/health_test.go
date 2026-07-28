package health

import (
	"runtime"
	"strings"
	"testing"
)

func TestDetectReportsCoderModeAndStatus(t *testing.T) {
	r := Detect(true, "slim")
	if r.Status != "ok" {
		t.Errorf("Status = %q, want ok", r.Status)
	}
	if r.CoderMode != "slim" {
		t.Errorf("CoderMode = %q, want slim", r.CoderMode)
	}
	if r.Version == "" {
		t.Error("Version must be populated from buildinfo")
	}
}

// Sandbox.Enabled is the operator's setting; Supported is the kernel's answer.
// Reporting only one of them would hide "configured on, silently inactive".
func TestSandboxEnabledIsDistinctFromSupported(t *testing.T) {
	off := Detect(false, "full")
	if off.Sandbox.Enabled {
		t.Error("Sandbox.Enabled must follow the passed-in setting")
	}
	on := Detect(true, "full")
	if !on.Sandbox.Enabled {
		t.Error("Sandbox.Enabled must be true when enabled")
	}
	if runtime.GOOS != "linux" && on.Sandbox.Supported {
		t.Error("Sandbox.Supported must be false off Linux")
	}
}

// The one absence that weakens a security control must be a warning, and the
// warning must name the consequence, not just the missing binary.
func TestMissingPython3WarnsAboutGuardrail(t *testing.T) {
	r := Detect(true, "full")
	r.Tools.Python3 = false

	warns := strings.Join(r.Warnings(), "\n")
	if !strings.Contains(warns, "python3") {
		t.Errorf("warnings must mention python3, got: %q", warns)
	}
	if !strings.Contains(strings.ToLower(warns), "guardrail") {
		t.Errorf("warnings must name the consequence (guardrail), got: %q", warns)
	}
}

func TestUnsupportedSandboxWarns(t *testing.T) {
	r := Detect(true, "full")
	r.Sandbox.Supported = false

	warns := strings.Join(r.Warnings(), "\n")
	if !strings.Contains(strings.ToLower(warns), "sandbox") {
		t.Errorf("warnings must mention the sandbox, got: %q", warns)
	}
}

func TestNoWarningsWhenHealthy(t *testing.T) {
	r := Report{
		Status:  "ok",
		Sandbox: Sandbox{Supported: true, Enabled: true, ABI: 8},
		Tools:   Tools{Python3: true, Ripgrep: true, PDFToText: true, Tesseract: true},
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("healthy report produced warnings: %v", w)
	}
}
