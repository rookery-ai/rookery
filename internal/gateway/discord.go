package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// discordAPIBase is the Discord REST base; overridable in tests.
var discordAPIBase = "https://discord.com/api/v10"

// validateDiscordToken confirms a bot token by fetching the bot user, returning its username.
func validateDiscordToken(token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, discordAPIBase+"/users/@me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("discord api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord rejected token (status %d)", resp.StatusCode)
	}
	var out struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Username == "" {
		return "", fmt.Errorf("invalid response from discord")
	}
	return out.Username, nil
}

func init() {
	RegisterCredSpec(CredSpec{
		Platform: "discord",
		Label:    "Discord",
		Blurb:    "Chat with your agents via a personal Discord bot (DMs)",
		Fields:   []CredField{{Key: "token", Label: "Bot Token", Placeholder: "your bot token", Secret: false}},
		SetupURL: "https://discord.com/developers/applications",
		SetupSteps: []string{
			"Open the Discord Developer Portal and create a New Application",
			"Open the Bot tab, click Reset Token, and copy the token",
			"On the Bot tab, enable the MESSAGE CONTENT INTENT (Privileged Gateway Intents)",
			"Invite the bot to a server OR just DM it after connecting; send /start to link",
		},
		Validate: func(v map[string]string) (string, error) { return validateDiscordToken(v["token"]) },
	})
}
