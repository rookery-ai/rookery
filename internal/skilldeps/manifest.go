package skilldeps

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// manifestName is the record of every install into one environment, kept beside it.
const manifestName = "rookery-env.json"

// Package is one resolved distribution.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InstallRecord is one install into an environment.
type InstallRecord struct {
	At        time.Time `json:"at"`
	Agent     string    `json:"agent,omitempty"`
	Requested []string  `json:"requested"`
	Resolved  []Package `json:"resolved,omitempty"`
	// MalwareCheck is "clean", or "skipped: <reason>" when OSV could not be asked.
	MalwareCheck string `json:"malware_check,omitempty"`
}

// Manifest is the install history of one environment.
type Manifest struct {
	Env      string          `json:"env"`
	Installs []InstallRecord `json:"installs"`
}

// ReadManifest reads envDir's manifest. A missing one is an empty manifest, not an
// error: an environment nobody installed into simply has no history.
func ReadManifest(envDir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(envDir, manifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// AppendInstall adds rec to envDir's manifest, writing atomically.
func AppendInstall(envDir string, rec InstallRecord) error {
	m, err := ReadManifest(envDir)
	if err != nil {
		return err
	}
	m.Env = filepath.Base(envDir)
	m.Installs = append(m.Installs, rec)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(envDir, ".manifest-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(envDir, manifestName))
}

// Satisfies reports whether every spec was requested by some recorded install. It
// compares the requirement strings as written, which is what the skill declares and
// what the agent was told to install.
func (m Manifest) Satisfies(specs []string) bool {
	have := map[string]bool{}
	for _, r := range m.Installs {
		for _, s := range r.Requested {
			have[s] = true
		}
	}
	for _, s := range specs {
		if !have[s] {
			return false
		}
	}
	return true
}
