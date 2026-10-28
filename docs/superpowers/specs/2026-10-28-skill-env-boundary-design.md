# Skill environments: tell scripts where the knowledge base is, and keep bash out

Date: 2026-10-28
Status: approved by the owner in chat ("do this on your own"); builds on
`2026-10-26-skill-dependencies-design.md`.

## Why

A manual run of the trading agent on 2026-10-27 produced a correct message, but:

1. **The skill's script could not find the knowledge base.** It looked for the nearest
   folder containing `notes/` from its working directory; an agent runs from
   `agents/<id>/`, which has its own `notes/`, so the BTC note landed in the agent's
   folder and the agent moved it with `bash`. `run_in_env` passes a script only its
   PATH/HOME/TMPDIR and the named secrets — never the knowledge base location.
2. **The agent then ran ETH and SOL with the environment's interpreter from `bash`**,
   bypassing `run_in_env`. Through `bash` the desk saw every workspace secret, the run's
   bridge tokens, the coder login in the workspace home, and every skill's environment
   (where it could plant code other agents would later run). `run_in_env` would have
   given it one key and none of the home.

## Decided

- **Fix 1.** `run_in_env` and `rookery deps run` set `ROOKERY_VAULT_ROOT` (the knowledge
  base root; a path, not a secret) in the script's environment whenever the knowledge base
  is known. The skill builder's `<agent_runtime>` block and the `skill-creator` core skill
  tell skills to locate the knowledge base from it, and never by searching for `notes/`.
- **Fix 2.** The API engine's `bash` tool refuses a command that executes a skill
  environment's interpreter, `pip` or `activate` script, or points Python at an
  environment's packages; the refusal names `run_in_env`. Reading files inside an
  environment (a report a script left there) stays allowed. The agent runtime prompt says
  the same.
- **Do not limit bash's secrets yet** (owner's instruction).
- **Only ship fix 2 if it breaks nothing.** Checked 2026-10-28: no AGENT.md, agent tool
  script, user skill, core skill or prompt on this install invokes an environment
  through the shell; the only occurrence was the 2026-10-27 workaround itself.

## Non-goals

- Secret scoping for `bash` / `run_script` (follow-up).
- Moving environments out of the workspace home (the airtight version; follow-up).
- Guarding a CLI coder's own `Bash` — it is not Rookery's tool, so this guard cannot see
  it. Stated as residual.
- Rewriting the existing `trading-consultant` skill; a rebuild picks up the new instruction.

## Residual (stated)

The guard is pattern matching on the command text: `cd` into an environment and running
`bin/python`, or building the path in a variable, gets past it. It closes the route a
model takes by default (yesterday's run), not a determined script. The airtight version
is moving environments outside the agent sandbox.
