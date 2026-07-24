package vault

// readmeTemplate is the home note scaffolded into a brand-new vault.
//
// It is written once, by EnsureScaffold, and never rewritten — see the call
// site. Existing vaults keep whatever their README has become.
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
  ` + "`USER.md`" + ` holds your name, location, role and background;
  ` + "`SOUL.md`" + ` holds tone and communication style; ` + "`GENERAL.md`" + ` collects
  quick notes. Add your own files here freely — they are all picked up.
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
available to every agent.

## What you can do here

- **Write and edit notes** — open any ` + "`.md`" + ` file to edit it, formatted or as
  raw markdown. Other file types open read-only.
- **Organise** — create folders, move, rename, and drag notes into the order
  you want. Your arrangement is remembered.
- **Link notes** — write ` + "`[[note name]]`" + ` to link one note to another. A
  note shows what links back to it.
- **Search** — full-text search across everything in the vault.
- **Add files** — upload a PDF, Word, Excel, PowerPoint, CSV, or web page and
  it is converted to markdown so it becomes searchable and usable by agents.
  Conversion notes anything it could not extract cleanly.

## How agents use it

Agents read the whole knowledge base and can write to it — that is how what
they learn survives from one run to the next. If an agent should remember
something, telling it to write that down here is usually the answer.
`
