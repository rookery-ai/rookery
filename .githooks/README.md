# Git hooks

Installed with `make hooks`, which points `core.hooksPath` here. Git does not
share hooks through a clone, so this is opt-in per checkout — `CONTRIBUTING.md`
names it in the setup block.

## Two pattern files, two severities

`patterns-block.txt` and `patterns-warn.txt` are read by BOTH `commit-msg` and
the `pr-description` job in `.github/workflows/pr.yml`, so local and CI
enforcement cannot drift apart. Add a pattern to either file and both surfaces
gain it.

They are split because a credential and a local-environment path are not the
same kind of problem:

- **`patterns-block.txt`** — credential shapes (`API_KEY=`/`PASSWORD=`/
  `SECRET=` assignments, `sk-…`, `gh[pousr]_…`, `xox[baprs]-…`, `AKIA…`,
  `AIza…`, a PEM private-key header, a Telegram bot-token shape). **Blocking**
  (exit 1). A leaked credential is a security incident — it is permanent,
  public the moment it's pushed, and the fix is to rotate the secret, not to
  edit the message.
- **`patterns-warn.txt`** — local-environment shapes: `/home/…`, `/Users/…`,
  a `.lan` host, and the RFC1918 + CGNAT/Tailscale address ranges. **Advisory
  only** — printed, but exits 0 and never blocks the commit or the PR check.

The split was not a style choice; it replaced a single blocking file that was
tried first and rejected on evidence. All three of these are legitimate,
on-topic commit messages for THIS repository, and the single-file version
blocked every one of them:

- `"Verified against a live instance at 192.168.1.50."` — self-hosted
  connector testing. Home Assistant, Immich and Paperless-ngx are
  RFC1918-addressed *by design* in this repo (see the connectors section of
  `CLAUDE.md`).
- `"A redirect to test.lan now fails validation as expected."` — the OAuth
  redirect-policy work is literally about `.lan` hosts being rejected.
- `"The failure only reproduced under /home/runner/work/rookery."` — routine
  CI debugging.

A hook that blocks people from accurately describing their own work gets
disabled at the first friction, and a disabled hook protects nothing — that
outcome is worse than never having shipped it. If you're tempted to fold
`patterns-warn.txt` back into `patterns-block.txt` "for simplicity", the three
examples above are what breaks; keep the two files and the two exit codes.

The patterns apply to **commit messages and PR descriptions only**, never to
file content. File content is covered by gitleaks and GitHub push protection.
That separation is why RFC1918/`.lan` literals can appear here at all without
breaking the connector YAML examples, which document self-hosted deployments
and are files, not messages.

## Portability note

`patterns-warn.txt`'s `.lan` line uses `([^A-Za-z0-9]|$)` instead of the GNU
`\b` word-boundary escape. `install.sh` supports macOS, whose default `grep`
is BSD grep and does not understand `\b` in a POSIX ERE — both `commit-msg`
and the CI job run `grep -Ef` against these files, so every pattern in both
files must stay POSIX-portable.
