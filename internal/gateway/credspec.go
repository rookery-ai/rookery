package gateway

import (
	"encoding/json"
	"sort"
	"sync"
)

type CredField struct {
	Key, Label, Placeholder string
	Secret                  bool
}

type CredSpec struct {
	Platform   string
	Label      string // human display name, e.g. "Discord"
	Blurb      string // one-line description for the connector card
	Fields     []CredField
	SetupURL   string
	SetupSteps []string
	Validate   func(values map[string]string) (identity string, err error)
}

var (
	credMu    sync.RWMutex
	credSpecs = map[string]CredSpec{}
)

func RegisterCredSpec(s CredSpec) {
	credMu.Lock()
	defer credMu.Unlock()
	credSpecs[s.Platform] = s
}

func CredSpecFor(platform string) (CredSpec, bool) {
	credMu.RLock()
	defer credMu.RUnlock()
	s, ok := credSpecs[platform]
	return s, ok
}

func CredSpecs() []CredSpec {
	credMu.RLock()
	defer credMu.RUnlock()
	out := make([]CredSpec, 0, len(credSpecs))
	for _, s := range credSpecs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Platform < out[j].Platform })
	return out
}

// SplitCreds maps the "token" field to encrypted_token and all other fields to
// a stable-key-ordered JSON object for encrypted_config.
func SplitCreds(spec CredSpec, values map[string]string) (token, configJSON string, err error) {
	extra := map[string]string{}
	for _, f := range spec.Fields {
		if f.Key == "token" {
			token = values[f.Key]
			continue
		}
		extra[f.Key] = values[f.Key]
	}
	if len(extra) == 0 {
		return token, "", nil
	}
	b, err := json.Marshal(extra)
	if err != nil {
		return "", "", err
	}
	return token, string(b), nil
}
