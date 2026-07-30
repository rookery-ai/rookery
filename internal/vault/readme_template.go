package vault

import "strings"

// readmeTemplate is the home note scaffolded into a vault.
//
// EnsureScaffold writes it when the file is absent, and ALSO replaces an
// existing README that is byte-identical to one of the legacy templates below
// — see legacyREADMEs for why. A README the user has touched at all is never
// overwritten.
//
// It doubles as orientation for the AGENTS reading this vault, not just the
// person: an agent that opens the KB gets the same map of what each folder is
// for and which ones it must not write into.
const readmeTemplate = `# Knowledge Base

This is your knowledge base — one folder of linked markdown notes, shared by
you, your agents, and chat. Anything worth remembering between sessions lives
here.

## Folders

- **memory/** — who you are and how you like to be talked to. Every ` + "`.md`" + `
  file here is injected into the context of every chat, agent run, and design
  session, so this is the fastest way to change how the assistant behaves.
  ` + "`ABOUT.md`" + ` holds what this workspace is for and who you are;
  ` + "`STYLE.md`" + ` holds tone and language. Both are filled in from what you
  entered during setup, and editing them here is how you change them — they are
  the source of truth, not a copy of a setting kept somewhere else. Add your own
  files here freely; they are all picked up. (A ` + "`GENERAL.md`" + ` appears once
  you use the ` + "`/memory`" + ` command from a connected chat app.)
- **notes/** — yours. Notes, journals, plans, todos, research, anything. The
  app does not write here unless you or an agent chooses to.
- **agents/** — one folder per agent, holding its instructions
  (` + "`AGENT.md`" + `), its memory between runs (` + "`state.md`" + `), any scripts it
  wrote, and its run logs. Managed by the app: read it freely, but let each
  agent own its own folder.
- **chats/** — a markdown transcript of each conversation, written
  automatically so past chats stay searchable. Managed by the app.
- **skills/** — the skills you have created or imported. Each is a folder with
  a ` + "`SKILL.md`" + ` and optionally scripts the skill runs.

Built-in skills are not shown here — they ship inside the app and are always
available to every agent. Reminders are not here either: they live in the app,
not as notes.

## What you can do here

- **Write and edit notes** — open any ` + "`.md`" + ` file to edit it, formatted or as
  raw markdown. Other file types open read-only.
- **Organise** — create folders, move, rename, and drag notes into the order
  you want. Your arrangement is remembered.
- **Link notes** — write ` + "`[[note name]]`" + ` to link one note to another. A
  note shows what links back to it.
- **Search** — full-text search across everything here.
- **Add files** — upload a PDF, Word, Excel, PowerPoint, CSV, or web page and
  it is converted to markdown so it becomes searchable and usable by agents.
  Conversion notes anything it could not extract cleanly.

## How agents use it

Agents read the whole knowledge base and can write to it — that is how what
they learn survives from one run to the next. If an agent should remember
something, telling it to write that down here is usually the answer.
`

// legacyREADMEs are every home note this app has ever scaffolded, byte for byte.
//
// A vault created before the richer template above already has a README, and
// EnsureScaffold's write-only-if-absent rule would leave that install stuck
// with the old four-line folder list forever — the improvement would reach new
// vaults only, which is precisely backwards for the people who already have
// one.
//
// So an existing README is upgraded IFF it exactly equals one of these. Exact
// equality is the whole safety argument: a user who edited so much as a
// character keeps their file, because their bytes cannot match. That is why
// this is a literal list rather than a heuristic like "looks short and mentions
// the folders" — a heuristic can destroy work, and this cannot.
//
// Three templates shipped historically; two of them said [[sessions]], which
// MigrateSessionsToChats rewrote in place to [[chats]], so their post-migration
// forms are listed too. (The oldest one's rewrite is a distinct string; the
// middle one's rewrite is byte-identical to the newest, so it needs no entry.)
var legacyREADMEs = []string{
	// The CURRENT template. Listed so the invariant is self-enforcing:
	// TestCurrentTemplateIsInLegacyList fails the moment readmeTemplate changes
	// without its outgoing text being moved into this list, which is the exact
	// mistake that would strand every existing install on the old note.
	// Harmless to match: "upgrading" a README to itself is a no-op write.
	readmeTemplate,

	// The template shipped immediately before the ABOUT.md/STYLE.md rename.
	// It is in this list for the reason the block comment above gives: an
	// install that already has THIS text would otherwise keep it forever.
	`# Knowledge Base

This is your knowledge base — one folder of linked markdown notes, shared by
you, your agents, and chat. Anything worth remembering between sessions lives
here.

## Folders

- **memory/** — who you are and how you like to be talked to. Every ` +
		"`" +
		`.md` +
		"`" +
		`
  file here is injected into the context of every chat, agent run, and design
  session, so this is the fastest way to change how the assistant behaves.
  ` +
		"`" +
		`USER.md` +
		"`" +
		` holds your name, location, role and background;
  ` +
		"`" +
		`SOUL.md` +
		"`" +
		` holds tone and communication style; ` +
		"`" +
		`GENERAL.md` +
		"`" +
		` collects
  quick notes. Add your own files here freely — they are all picked up.
- **notes/** — yours. Notes, journals, plans, todos, research, anything. The
  app does not write here unless you or an agent chooses to.
- **agents/** — one folder per agent, holding its instructions
  (` +
		"`" +
		`AGENT.md` +
		"`" +
		`), its memory between runs (` +
		"`" +
		`state.md` +
		"`" +
		`), any scripts it
  wrote, and its run logs. Managed by the app: read it freely, but let each
  agent own its own folder.
- **chats/** — a markdown transcript of each conversation, written
  automatically so past chats stay searchable. Managed by the app.
- **skills/** — the skills you have created or imported. Each is a folder with
  a ` +
		"`" +
		`SKILL.md` +
		"`" +
		` and optionally scripts the skill runs.

Built-in skills are not shown here — they ship inside the app and are always
available to every agent.

## What you can do here

- **Write and edit notes** — open any ` +
		"`" +
		`.md` +
		"`" +
		` file to edit it, formatted or as
  raw markdown. Other file types open read-only.
- **Organise** — create folders, move, rename, and drag notes into the order
  you want. Your arrangement is remembered.
- **Link notes** — write ` +
		"`" +
		`[[note name]]` +
		"`" +
		` to link one note to another. A
  note shows what links back to it.
- **Search** — full-text search across everything in the vault.
- **Add files** — upload a PDF, Word, Excel, PowerPoint, CSV, or web page and
  it is converted to markdown so it becomes searchable and usable by agents.
  Conversion notes anything it could not extract cleanly.

## How agents use it

Agents read the whole knowledge base and can write to it — that is how what
they learn survives from one run to the next. If an agent should remember
something, telling it to write that down here is usually the answer.
`,

	// Most recent legacy template.
	"# Knowledge Base\n\n" +
		"This is your personal knowledge base. Everything you and your agents " +
		"create lives here as interlinked markdown notes.\n\n" +
		"- [[notes]] — your notes, journals, plans and todos\n" +
		"- [[memory]] — your profile and context (USER.md, SOUL.md, and more)\n" +
		"- [[agents]] — your agents and their run logs\n" +
		"- [[chats]] — chat transcripts\n",

	// Same, before the sessions → chats rename.
	"# Knowledge Base\n\n" +
		"This is your personal knowledge base. Everything you and your agents " +
		"create lives here as interlinked markdown notes.\n\n" +
		"- [[notes]] — your notes, journals, plans and todos\n" +
		"- [[memory]] — your profile and context (USER.md, SOUL.md, and more)\n" +
		"- [[agents]] — your agents and their run logs\n" +
		"- [[sessions]] — chat transcripts\n",

	// The original template, which also listed a reminders/ folder.
	"# Knowledge Base\n\n" +
		"This is your personal knowledge base. Everything you and your agents " +
		"create lives here as interlinked markdown notes.\n\n" +
		"- [[notes]] — your notes, journals, plans and todos\n" +
		"- [[memory]] — facts your agents remember\n" +
		"- [[agents]] — your agents and their run logs\n" +
		"- [[sessions]] — chat transcripts\n" +
		"- [[reminders]] — reminders\n",

	// The original, after the sessions → chats rename.
	"# Knowledge Base\n\n" +
		"This is your personal knowledge base. Everything you and your agents " +
		"create lives here as interlinked markdown notes.\n\n" +
		"- [[notes]] — your notes, journals, plans and todos\n" +
		"- [[memory]] — facts your agents remember\n" +
		"- [[agents]] — your agents and their run logs\n" +
		"- [[chats]] — chat transcripts\n" +
		"- [[reminders]] — reminders\n",
}

// isPristineREADME reports whether content is an untouched scaffolded home
// note, i.e. safe to replace with the current template.
//
// Trailing whitespace is ignored on both sides. On a real install, notes saved
// through the KB editor came back one byte shorter than the template that wrote
// them — the trailing newline had been stripped — so a strictly byte-exact
// comparison would have skipped the very vaults this upgrade exists for.
// Ignoring only TRAILING whitespace keeps the safety argument intact: any
// actual content a user added still fails the comparison.
func isPristineREADME(content []byte) bool {
	s := strings.TrimRight(string(content), " \t\r\n")
	for _, legacy := range legacyREADMEs {
		if s == strings.TrimRight(legacy, " \t\r\n") {
			return true
		}
	}
	return false
}
