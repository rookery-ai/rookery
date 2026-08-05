package gateway

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

type CredField struct {
	Key, Label, Placeholder string
	Secret                  bool
}

// BotIdentity is what a platform's Validate probe learns about the BOT account
// behind a set of credentials. Every field is a PUBLIC identifier — a bot's
// username and id are meant to be shared, and Discord's invite URL embeds the
// id — so this is persisted as a plain workspace setting rather than in the
// encrypted config blob. Keeping it out of ciphertext is what lets the
// connectors list endpoint stay DB-only and therefore cheap to poll.
type BotIdentity struct {
	Username string `json:"username,omitempty"` // display handle, e.g. "rookery_bot"
	UserID   string `json:"user_id,omitempty"`  // the bot's platform user id; on Discord this is ALSO the application id
	TeamID   string `json:"team_id,omitempty"`  // Slack only: the team id its DM deep link needs
}

// LinkTargets are the platform-specific URLs the wizard's linking step renders.
// They are built here rather than in the SPA so that adding a platform stays a
// gateway-package change alone.
type LinkTargets struct {
	DMURL     string // opens a DM with the bot
	InviteURL string // Discord only: adds the bot to a server, a PREREQUISITE for DMs
}

// MarshalSetting encodes the identity for db.SetSetting.
func (b BotIdentity) MarshalSetting() (string, error) {
	out, err := json.Marshal(b)
	return string(out), err
}

// BotIdentityFromSetting decodes a value written by MarshalSetting. It never
// errors: a missing, truncated or hand-edited setting degrades to an empty
// identity, and the wizard falls back to prose instructions.
func BotIdentityFromSetting(s string) BotIdentity {
	var b BotIdentity
	if json.Unmarshal([]byte(s), &b) != nil {
		return BotIdentity{}
	}
	return b
}

// BotIdentitySettingKey namespaces the identity per platform in the shared
// workspace settings table.
func BotIdentitySettingKey(platform string) string {
	return "bot_identity." + strings.ToLower(platform)
}

type CredSpec struct {
	Platform   string
	Label      string // human display name, e.g. "Discord"
	Blurb      string // one-line description for the connector card
	Fields     []CredField
	SetupURL   string
	SetupSteps []string
	// Validate probes the credentials against the platform and reports the bot
	// account behind them. Nil means "nothing to probe".
	Validate func(values map[string]string) (BotIdentity, error)
	// LinkURLs builds the deep links the wizard's linking step shows. Nil means
	// the step falls back to prose ("open a DM with the bot").
	LinkURLs func(b BotIdentity) LinkTargets
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
