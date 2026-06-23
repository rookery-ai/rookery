// Package sandbox provides a self-contained, dependency-free filesystem
// confinement for coder subprocesses using the Linux Landlock LSM.
//
// Confinement is applied by re-executing this same binary as a hidden helper
// subcommand (HelperCommand): the helper restricts *itself* with Landlock and a
// few resource limits, then exec()s the real command. Because Landlock is
// inherited across exec and by all child processes, the whole coder→bash→python
// tree ends up confined — without depending on any external tool (no firejail,
// no setuid, no namespaces, no sysctl toggle).
//
// This confines the *filesystem view* only: a confined agent can no longer read
// another user's vault, another user's CLI credentials, the SQLite database, or
// config.yaml — it sees only the paths granted in its Spec. It does NOT create
// OS-level uid separation (everything still runs as one OS user) and does NOT
// restrict the network. On kernels without Landlock, Supported() returns false
// and callers fall back to the detective vault.Guard.
package sandbox

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// HelperCommand is the hidden CLI subcommand that confines itself and then
// exec()s the real command. cmd/simple-agents registers it and routes it to Exec.
const HelperCommand = "__sandbox-exec"

// Spec describes one confined execution. It is JSON-serialised, base64-encoded,
// and passed as a single argv element to the helper subcommand.
type Spec struct {
	Command        []string `json:"command"`          // argv to exec (Command[0] resolved via PATH if not absolute)
	Dir            string   `json:"dir,omitempty"`    // working directory
	Env            []string `json:"env"`              // full environment for the exec'd process
	ReadWritePaths []string `json:"rw,omitempty"`     // directories granted read+write
	ReadOnlyPaths  []string `json:"ro,omitempty"`     // directories granted read-only (+execute)
	ReadWriteFiles []string `json:"rwf,omitempty"`    // individual files granted read+write (e.g. /dev/null)
	NoFile         uint64   `json:"nofile,omitempty"` // RLIMIT_NOFILE (0 = leave kernel default)
	MemoryMB       int      `json:"mem_mb,omitempty"` // RLIMIT_AS in MB (0 = no limit; node needs a large address space)
	CPUSeconds     int      `json:"cpu_s,omitempty"`  // RLIMIT_CPU in seconds (0 = no limit)
}

// EncodeSpec serialises a Spec to a base64 string safe to pass as one argv element.
func EncodeSpec(s Spec) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(raw), nil
}

// DecodeSpec is the inverse of EncodeSpec.
func DecodeSpec(enc string) (Spec, error) {
	var s Spec
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}

// Wrap returns the argv that runs spec.Command through the in-binary helper:
//
//	selfExe HelperCommand <base64-spec>
//
// selfExe must be the path to the running simple-agents binary (os.Executable()).
func Wrap(selfExe string, spec Spec) ([]string, error) {
	if selfExe == "" {
		return nil, fmt.Errorf("sandbox: empty self-executable path")
	}
	enc, err := EncodeSpec(spec)
	if err != nil {
		return nil, err
	}
	return []string{selfExe, HelperCommand, enc}, nil
}
