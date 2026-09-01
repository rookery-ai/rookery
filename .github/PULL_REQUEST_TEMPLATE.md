<!--
The PR TITLE must be a Conventional Commit — `type(scope): summary` — because
merges are squashes and the title becomes the commit that lands on main.
-->

## What this changes

## Why

## How it was verified

<!-- Commands actually run and their outcome. Not "tests pass" — which tests. -->

## Checklist

- [ ] Targeted checks run for what changed (`make ci-fmt` / `ci-vet` / `ci-ui` /
      `ci-docs`) — the full gate runs here, not on your machine
- [ ] Branch name matches `type/short-description`
- [ ] Documentation updated if this touches a connector provider, a `ROOKERY_*`
      variable, a CLI subcommand, a core skill, a chat adapter, a backup
      destination, an `/api/v1` route, or a packaging target
- [ ] No credentials, home directory paths, `.lan` hostnames or private IPs in
      the diff, the commit messages, or this description
