package skilldeps

import (
	"slices"
	"strings"
	"testing"
)

// Install-time code (setup.py, a build backend) is the classic malicious-package
// vector, so the install sandbox may write ONLY its own environment and carries no
// secret: the environment is built from scratch, never inherited.
func TestInstallSpecGrantsOnlyTheEnv(t *testing.T) {
	env := "/h/.local/share/rookery/envs/x"
	spec := installSpec(env, []string{env + "/bin/python", "-m", "pip"}, "/usr/bin/python3", "/usr/bin/git")
	if !slices.Equal(spec.ReadWritePaths, []string{env}) {
		t.Errorf("RW = %v, want only %s", spec.ReadWritePaths, env)
	}
	if !slices.Contains(spec.Env, "PIP_CONFIG_FILE=/dev/null") {
		t.Errorf("env must disable pip config files: %v", spec.Env)
	}
	for _, kv := range spec.Env {
		if strings.HasPrefix(kv, "CODER_OPENROUTER_KEY=") || strings.HasPrefix(kv, "ROOKERY_CONNECTOR_TOKEN=") {
			t.Errorf("install env carries %s", kv)
		}
	}
	for _, p := range append(spec.ReadWritePaths, spec.ReadOnlyPaths...) {
		if p == "/h" || strings.HasPrefix(p, "/h/.claude") {
			t.Errorf("install sandbox grants %s", p)
		}
	}
}

// The script that imports a dependency runs without the workspace home: no coder
// credentials, no other skill's environment.
func TestRunSpecDoesNotGrantTheHome(t *testing.T) {
	env := "/h/.local/share/rookery/envs/x"
	spec := runSpec(env, "/agent", "/vault", []string{env + "/bin/python", "s.py"}, []string{"A=1"}, "/usr/bin/python3")
	for _, want := range []string{env, "/agent", "/vault"} {
		if !slices.Contains(spec.ReadWritePaths, want) {
			t.Errorf("RW %v lacks %s", spec.ReadWritePaths, want)
		}
	}
	for _, p := range append(spec.ReadWritePaths, spec.ReadOnlyPaths...) {
		if p == "/h" || strings.HasPrefix(p, "/h/.claude") || p == "/h/tmp" {
			t.Errorf("run sandbox grants %s", p)
		}
	}
	if !slices.Equal(spec.Env, []string{"A=1"}) {
		t.Errorf("run env must be exactly what the caller built, got %v", spec.Env)
	}
}
