package gateway

import (
	"fmt"
	"strings"
)

// Command is one chat command, described once and consumed by everything that
// needs to know about it: the /help text, Telegram's setMyCommands, Discord's
// application-command registration, and the Slack app manifest.
//
// Before this table the list existed three times over — the dispatch switch in
// Handle, the hand-maintained helpText string, and Discord's lone startCommand —
// none derived from another. Adding three platform registrations would have made
// five copies drifting independently, which is why the table came first.
type Command struct {
	// Name is what the router dispatches on, WITHOUT the leading slash. It is
	// also the Telegram and Discord command name, so it must satisfy the
	// strictest of the three: Telegram allows only lowercase letters, digits and
	// underscores, 1-32 characters.
	Name string

	// Description is the one-liner the platform menus show. Discord caps this at
	// 100 characters and Telegram requires 3-256, so it must fit in 3-100.
	Description string

	// UsageHint is the argument shape, e.g. "create <name>". Empty for commands
	// that take none. Shown as the Discord option description and the Slack
	// usage hint.
	UsageHint string

	// SlackName overrides Name on Slack only. Slack's slash-command namespace is
	// WORKSPACE-GLOBAL and shared with Slack's own built-ins, so /help and
	// /remind cannot be claimed. Empty means "same as Name".
	//
	// The override is Slack-only on purpose: it carries a hyphen, which Telegram
	// rejects outright in a command name.
	SlackName string

	// Help is the detailed subcommand breakdown /help prints, one line each. A
	// single-line command has exactly one entry with an empty Args.
	Help []HelpLine
}

// HelpLine is one row of /help: the argument form and what it does.
type HelpLine struct {
	Args string // e.g. "create <name>"; empty for the bare command
	What string
}

// Commands is the single source of truth. Order is the order /help prints and
// the order the platform menus list.
var Commands = []Command{
	{
		Name:        "agent",
		Description: "Create, edit and list your agents",
		UsageHint:   "list | create <name> | edit <name> | cancel",
		Help: []HelpLine{
			{"list", "list your agents"},
			{"create <name>", "build a new agent with AI wizard"},
			{"edit <name>", "change an existing agent with AI wizard"},
			{"cancel", "cancel active agent creation or edit"},
		},
	},
	{
		Name:        "skill",
		Description: "Create and list your skills",
		UsageHint:   "list | create <name> | cancel",
		Help: []HelpLine{
			{"list", "list your skills and the built-in ones"},
			{"create <name>", "build a new skill with AI wizard"},
			{"cancel", "cancel active skill creation"},
		},
	},
	{
		Name:        "run",
		Description: "Run an agent now",
		UsageHint:   "<name>",
		Help:        []HelpLine{{"<name>", "run an agent"}},
	},
	{
		Name:        "secret",
		Description: "List, reveal and delete stored secrets",
		UsageHint:   "list | show <name> | delete <name>",
		Help: []HelpLine{
			{"list", "list stored secret names"},
			{"show <name>", "reveal a secret value (requires master password)"},
			{"delete <name>", "delete a secret (requires master password)"},
		},
	},
	{
		Name:        "remind",
		Description: "Set, list and delete reminders",
		UsageHint:   "<when> to <message> | list | delete <n>",
		// Slack's built-in /remind owns this name workspace-wide.
		SlackName: "rookery-remind",
		Help: []HelpLine{
			{"<when> to <message>", "set a reminder (e.g. /remind in 10 minutes to check oven)"},
			{"list", "list your reminders"},
			{"delete <n>", "delete a reminder by number"},
		},
	},
	{
		Name:        "chat",
		Description: "Start, resume and manage saved chats",
		UsageHint:   "start [name] | list | stop | resume <id> | delete <id>",
		Help: []HelpLine{
			{"start [name]", "start a chat (saves history)"},
			{"list", "list all chats with IDs"},
			{"stop", "stop current chat"},
			{"resume <id>", "resume a previous chat"},
			{"delete <id>", "delete a chat and its history"},
		},
	},
	{
		Name:        "memory",
		Description: "Add, list and delete saved memory entries",
		UsageHint:   "list | add <text> | delete <n>",
		Help: []HelpLine{
			{"list", "list saved memory entries"},
			{"add <text>", "save a new memory entry"},
			{"delete <n>", "delete entry by number"},
		},
	},
	{
		Name:        "pending",
		Description: "List posts waiting for your approval",
		Help:        []HelpLine{{"", "list posts waiting for your approval"}},
	},
	{
		Name:        "approve",
		Description: "Publish a waiting post",
		UsageHint:   "<id>",
		Help:        []HelpLine{{"<id>", "publish a waiting post"}},
	},
	{
		Name:        "reject",
		Description: "Decline a waiting post",
		UsageHint:   "<id>",
		Help:        []HelpLine{{"<id>", "decline a waiting post"}},
	},
	{
		Name:        "start",
		Description: "Link your account to this workspace",
		Help:        []HelpLine{{"", "link your account to this workspace"}},
	},
	{
		Name:        "help",
		Description: "Show every command",
		// Slack reserves /help for its own help launcher.
		SlackName: "rookery-help",
		Help:      []HelpLine{{"", "this message"}},
	},
}

// PlatformCommandName returns the name a command is registered under on the
// given platform: the Slack override where one exists, else the canonical name.
func (c Command) PlatformCommandName(platform string) string {
	if platform == "slack" && c.SlackName != "" {
		return c.SlackName
	}
	return c.Name
}

// CanonicalCommandName reverses PlatformCommandName, mapping a name received
// from a platform back to the one the router dispatches on. An unrecognised name
// is returned unchanged, so the router's own unknown-command branch reports it
// rather than this silently rewriting it to something else.
func CanonicalCommandName(platform, received string) string {
	received = strings.TrimPrefix(received, "/")
	for _, c := range Commands {
		if c.PlatformCommandName(platform) == received {
			return c.Name
		}
	}
	return received
}

// helpText renders /help from Commands. All three platforms implement
// attachment download, so the file-upload line applies uniformly.
func helpText(platform string) string {
	var b strings.Builder
	b.WriteString("**Rookery — Commands**\n\n")
	for _, c := range Commands {
		name := "/" + c.PlatformCommandName(platform)
		for _, h := range c.Help {
			if h.Args == "" {
				fmt.Fprintf(&b, "%s — %s\n", name, h.What)
				continue
			}
			fmt.Fprintf(&b, "%s %s — %s\n", name, h.Args, h.What)
		}
	}
	b.WriteString("\nSend a file (document/photo) to save it to your knowledge base.\n")
	b.WriteString("\n_Add secrets at the web dashboard — no master password needed to add_")
	return b.String()
}
