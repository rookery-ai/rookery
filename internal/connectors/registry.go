// Package connectors owns the self-managed-OAuth connector layer: per-provider
// OAuth configs + curated action manifests (embedded data files), and the typed
// Execute path agents call. Adding a service = adding a providers/<p>.yaml and a
// connectors/<p>.yaml; no Go changes.
package connectors

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"

	"gopkg.in/yaml.v3"
)

//go:embed providers/*.yaml connectors/*.yaml
var files embed.FS

// Provider is one service's OAuth configuration.
type Provider struct {
	Name          string   `yaml:"name"`
	AuthorizeURL  string   `yaml:"authorize_url"`
	TokenURL      string   `yaml:"token_url"`
	UserinfoURL   string   `yaml:"userinfo_url"`
	IdentityPath  string   `yaml:"identity_path"`
	DefaultScopes []string `yaml:"default_scopes"`
}

// RequestTemplate describes how to turn typed args into a real provider HTTP request.
type RequestTemplate struct {
	Method      string            `yaml:"method"`
	URL         string            `yaml:"url"`
	Query       map[string]string `yaml:"query"`
	BodyBuilder string            `yaml:"body_builder"`
	BodyJSON    map[string]string `yaml:"body_json"`
}

// Action is one curated, typed operation on a provider.
type Action struct {
	Name            string          `yaml:"name"`
	Description     string          `yaml:"description"`
	Mutating        bool            `yaml:"mutating"`
	ParamsRaw       map[string]any  `yaml:"params"`
	Request         RequestTemplate `yaml:"request"`
	ResponseExtract string          `yaml:"response_extract"`
	Params          json.RawMessage `yaml:"-"` // compiled JSON schema from ParamsRaw
}

type manifest struct {
	Provider string   `yaml:"provider"`
	Actions  []Action `yaml:"actions"`
}

// Registry holds the loaded providers + actions.
type Registry struct {
	providers map[string]Provider
	actions   map[string][]Action // provider -> actions
}

// LoadBundled parses every embedded provider config + action manifest.
func LoadBundled() (*Registry, error) {
	r := &Registry{providers: map[string]Provider{}, actions: map[string][]Action{}}

	pents, err := files.ReadDir("providers")
	if err != nil {
		return nil, err
	}
	for _, e := range pents {
		b, err := files.ReadFile(path.Join("providers", e.Name()))
		if err != nil {
			return nil, err
		}
		var p Provider
		if err := yaml.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("provider %s: %w", e.Name(), err)
		}
		r.providers[p.Name] = p
	}

	cents, err := files.ReadDir("connectors")
	if err != nil {
		return nil, err
	}
	for _, e := range cents {
		b, err := files.ReadFile(path.Join("connectors", e.Name()))
		if err != nil {
			return nil, err
		}
		var m manifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", e.Name(), err)
		}
		for i := range m.Actions {
			raw, err := json.Marshal(m.Actions[i].ParamsRaw)
			if err != nil {
				return nil, fmt.Errorf("%s.%s params: %w", m.Provider, m.Actions[i].Name, err)
			}
			m.Actions[i].Params = raw
		}
		r.actions[m.Provider] = m.Actions
	}
	return r, nil
}

// ProviderByName returns the OAuth config for a provider.
func (r *Registry) ProviderByName(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Actions returns all actions declared for a provider.
func (r *Registry) Actions(provider string) []Action { return r.actions[provider] }

// Action returns one named action for a provider.
func (r *Registry) Action(provider, name string) (Action, bool) {
	for _, a := range r.actions[provider] {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// BoundConn is a runner/UI-facing view of a connection an agent is bound to.
type BoundConn struct {
	ID, Provider, AccountLabel, AccountIdentity string
}
