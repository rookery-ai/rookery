# SP9 smoke test — Telegram parity

Manual checks for `/skill` and `/remind list|delete`. Unit tests cover the logic
boundaries; this covers the round-trip through a real chat platform, which nothing
in CI exercises.

Run after `make deploy`, from a Telegram account linked to a workspace.

## `/skill`

- [ ] `/help` lists the three `/skill` forms and the two new `/remind` forms.
- [ ] `/skill list` shows your own skills *and* a "Built-in skills" section. A
      workspace with no skills of its own still shows the built-ins — an empty
      list here would be wrong, since core skills are always available.
- [ ] `/skill create pdf-summarizer` replies asking what the skill should do.
      **It must not start generating yet** — this is the `StateDescribing` turn,
      which had never executed before SP9.
- [ ] Reply with a description. Generation starts and streams `🔧 …` milestones
      into the placeholder rather than sitting frozen.
- [ ] Approve when it asks. The skill appears in the web UI's skills list and in
      `<vault>/skills/<name>/SKILL.md`.
- [ ] The skill is now attachable to an agent from the agent page.

## One session at a time

- [ ] `/agent create daily-digest`, then — without finishing — `/skill create x`.
      Refused, naming `daily-digest` and `/agent cancel`. The agent session
      survives: the next plain message still goes to the agent designer.
- [ ] The mirror: `/skill create y`, then `/agent create z`. Refused, naming `y`
      and `/skill cancel`.

## Cancel must not eat the other draft

This is the data-loss case the unit tests pin; worth confirming live once.

- [ ] Start an agent (`/agent create a`), send one message, `/agent cancel`, reply
      `save`. You now hold an agent draft.
- [ ] Start a skill (`/skill create b`), send one message, `/skill cancel`, reply
      `discard`.
- [ ] `/agent create a` — it still offers to resume the agent draft. If the agent
      draft is gone, flow-aware pending-cancel has regressed.

## `/remind`

- [ ] `/remind in 10 minutes to check the oven` — still creates (unchanged path).
- [ ] `/remind list` — numbered, times in **your** timezone, not UTC. Verify
      against the profile timezone (Europe/Skopje → UTC+2).
- [ ] `/remind delete 1` — removes the one listed as #1 and names it in the reply.
- [ ] `/remind delete 99` — reports how many exist, deletes nothing.
- [ ] `/remind list` on an empty workspace — an empty-state sentence, not a blank
      list.
- [ ] **The shadow guard:** `/remind in 10 minutes to list the groceries` still
      *creates* a reminder. Same for `…to delete the old note`. If either lists or
      deletes instead, exact-match subcommand parsing has regressed.

## Other platforms

`Router` is platform-neutral, so both commands should appear on Discord and Slack
with no extra work.

- [ ] `/help` on Discord shows the new commands.
- [ ] `/remind list` works on Discord.
