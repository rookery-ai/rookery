---
name: ssh
description: Use this skill whenever an agent needs to reach another machine over SSH — running a command on a server, checking a remote service, copying a file with scp, or reading a remote log. Triggers include "ssh into", "run this on my server", "check the remote host", "copy this to the server", "restart the service on".
version: 1.0.0
license: MIT-0
category: Integrations
metadata:
  requires:
    bins: [ssh]
---

# SSH

There is no SSH tool on this platform, so this is a command — but the key
handling has three ways to go wrong silently, which is why it is written down.

## The key lives in a secret, never on disk

Store the private key as a secret (Settings → Secrets), then read it from the
environment at run time. **Never** write a key into a script, a note, or the
agent's own directory.

```bash
KEY="$TMPDIR/id_agent"
printf '%s\n' "$SSH_KEY" > "$KEY"
chmod 600 "$KEY"
```

Three things there are not optional:

- **`$TMPDIR`, never `/tmp`.** The sandbox does not grant `/tmp`, so a key
  written there fails with a permission error that reads like an SSH problem.
- **`chmod 600` before use.** OpenSSH refuses a key whose file is group- or
  world-readable, with `UNPROTECTED PRIVATE KEY FILE` — a message that sends
  people looking at the key's contents rather than its mode.
- **`printf '%s\n'`, not `echo`.** A key must end with a newline; some shells'
  `echo` will not add one, and OpenSSH rejects the truncated result as
  malformed.

Delete it when you are done:

```bash
shred -u "$KEY" 2>/dev/null || rm -f "$KEY"
```

## Running a command

```bash
ssh -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes \
    -o ConnectTimeout=10 user@host 'systemctl --user status myservice'
```

Each option earns its place:

- **`IdentitiesOnly=yes`** — without it SSH also offers every key the agent's
  environment happens to expose, and a server with a low `MaxAuthTries` closes
  the connection before reaching yours. The failure reads as "permission
  denied", not "too many keys".
- **`BatchMode=yes`** — never prompt. An agent has no terminal, so a password
  prompt is a hang until the run times out rather than an error.
- **`ConnectTimeout`** — an unreachable host otherwise consumes the run.

## Host keys: decide, do not disable

The tempting `-o StrictHostKeyChecking=no` accepts *any* host presenting itself
at that address, which is exactly the check that would notice you are talking to
the wrong machine.

Pin the host key instead. Capture it once, store it as a secret alongside the
private key, and write it out per run:

```bash
printf '%s\n' "$SSH_KNOWN_HOSTS" > "$TMPDIR/known_hosts"
ssh -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes \
    -o UserKnownHostsFile="$TMPDIR/known_hosts" \
    -o StrictHostKeyChecking=yes user@host 'uptime'
```

If the host key genuinely rotates and you cannot pin it, say so in your output
rather than silently disabling the check — the user should know which of their
hosts is unverified.

## Copying files

```bash
scp -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes \
    "$TMPDIR/report.md" user@host:/srv/reports/
```

Pull a remote file into the knowledge base rather than leaving it in `$TMPDIR`,
where the next run will not find it.

## Reading the result

`ssh` exits with the remote command's status, so check it. A command that failed
on the far side otherwise looks like a command that produced no output:

```bash
if ! out=$(ssh -i "$KEY" -o BatchMode=yes user@host 'df -h /'); then
  echo "remote command failed: $out" >&2
  exit 1
fi
```

## What not to do from an agent

Do not run anything destructive or irreversible on a remote host without the
user having asked for that specific action — a restart, a delete, a deploy.
Reading state, checking services and copying files are the ordinary cases. If
the task genuinely needs a destructive step, say what you are about to do and
why in your output, so the record shows it.
