# livecheck

A development harness that runs one connector action against a **real** stored
connection, so a provider YAML can be verified against the live API before it ships.

It is not part of the product: `.goreleaser.yaml` builds `./cmd/rookery` explicitly, so
this never ships as a release artifact, and `make build` does not touch it. It exists
because the connector catalog's stated verification bar — live-verify tier-A providers —
is unenforceable without it.

## Usage

    go run ./cmd/livecheck <provider> <action> '<json-args>'

Example:

    go run ./cmd/livecheck todoist todoist_list_projects '{}'

It reads the same SQLite database the server uses, decrypts the stored connection with
the system key, and calls `connectors.Execute` with an empty `Policy` — no build-phase
guard, no approval gate. **A mutating action will really run**, so prefer read-only
actions when verifying, and use a throwaway account when you cannot.

## What to check, beyond "it returned something"

The connector bridge caps a result at 8 KiB. An action that returns more is truncated
before the model sees it, and a truncated JSON value still parses — so it reads as
complete data. When verifying a list-shaped action, confirm both that the payload is
under the cap and that it is the narrowed shape the action's `response_extract`
promised.

## Why the whole catalog is not verified

CLAUDE.md records that non-Google connector configs are hand-authored and unverified
against live APIs. Every provider verified with this harness is one fewer in that
category; a provider that cannot be verified carries `unverified: true` in its YAML
rather than silently joining it.
