# Per-workspace chat bots — implementation plan

Spec: `docs/superpowers/specs/2026-10-19-per-workspace-chat-bots-design.md`.
Branch: `fix/per-workspace-chat-bots`. Each task is test-first.

1. **Migration 021** — `migrations/021_per_workspace_identities.{up,down}.sql`
   rebuilding `platform_identities` without `UNIQUE(platform, platform_user_id)`.
   Test (`internal/db`): one user id linked to two workspaces on one platform;
   a second chat for the same workspace+platform is refused; rows present before
   the migration survive it.
2. **DB layer** (`internal/db/repositories.go`) — `GetPlatformIdentity` takes the
   workspace; `UpsertPlatformIdentity` becomes `LinkPlatformIdentity` (plain
   insert, `ErrAlreadyLinked` on conflict); add `RekeyPlatformIdentity`. Update
   the 13 test call sites.
3. **Dispatch** (`internal/gateway/gateway.go`) — look up the identity in
   `msg.WorkspaceID`; never rewrite it. Unlinked + `msg.Group` + not `/start` →
   silent. `msg.MigrateFromID` → rekey and return. Tests in a new
   `multiworkspace_test.go`: two workspaces, one user, messages land in the
   receiving bot's workspace and replies leave through that bot.
4. **`/start`** (`internal/gateway/router.go`) — check the identity in this
   workspace; refuse when another chat holds it (including the race, via
   `ErrAlreadyLinked`).
5. **Telegram adapter** (`internal/gateway/telegram.go`) — set `Group` for
   non-private chats; strip `@<own username>` from the command; register
   `OnMigration`. Pure helpers so they are unit-testable without a live bot.
6. **Setup step + comments** — Telegram `CredSpec` setup step about privacy mode
   (update `setupsteps_test.go` if it pins the list); rewrite `ensureBotUnused`'s
   justification.
7. **Docs** — run the `docs-sync` skill (README, CLAUDE.md, rookery-web).
8. **Verify** — `go test ./internal/db/ ./internal/gateway/ ./web/ ./internal/reminder/`,
   `make ci-fmt ci-vet ci-docs`.
9. **Ship** — commit, push, draft-free PR; snapshot `~/.rookery/rookery.db`,
   stop the live server (pid from `~/rookery-main`), `make deploy` from this
   worktree, assert `/healthz` reports this commit.
