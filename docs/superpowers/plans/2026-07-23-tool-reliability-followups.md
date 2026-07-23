# Tool-reliability follow-ups (SP24)

Deferred items from the SP23 whole-branch review, plus the search-key UI the operator asked for. Each task ends at an independently-committed, deployed, reviewed state. Same discipline as SP23: TDD, one reviewer pass per task, fix wave, redeploy + commit after each.

**Branch:** `tool-reliability-followups` (off `main` after SP23 merged).

## Global constraints

- CGo-free (`CGO_ENABLED=0 go build ./...`).
- No tool ever returns an empty string; transient failures never surface as `error:`.
- Every network fetch uses the guarded client (`internal/nethttp` / `guardedHTTPClient`).
- Every vault write goes through `Resolve`; the shared `internal/iolimit.ReadCapped` bounds every ingest read.
- Any end-to-end verification runs against a TEMPORARY `SA_DATA_DIR`, never `~/.simple-agents-v2/`. No credential/secret mutation on the live instance. No broad `pkill`.
- Comments explain WHY.

---

## Task 1 — Panic recovery in the adapter dispatch (Tier A, highest value)

**Problem.** `GatewayManager.dispatch` (`internal/gateway/gateway.go:271`) is the single funnel for every inbound chat message, and nothing wraps it in `recover()`. Neither `discordgo` nor `telebot` recovers around handler dispatch, so an unrecovered panic anywhere in the message path — a nil deref in a handler, a bad type assertion — takes down the whole server process. On a home server with no supervisor that means the bot silently dies until a manual restart.

**Fix.** Wrap the per-message handling in a `recover()` that logs the panic with a stack trace and the platform/message context, then drops that one message and returns — the adapter loop and every other workspace keep running. Put the guard at the narrowest point that still protects every inbound message: inside `dispatch` (or a thin `safeDispatch` wrapper it calls), NOT around the whole adapter goroutine (which would kill the loop on first panic). A user whose message triggered the panic should get a generic "something went wrong" reply where a reply channel is available, not silence.

**Test.** A handler/router stub that panics; assert `dispatch` returns normally, logs, and the manager is still usable for the next message. Table a nil-message and a panic-in-render case.

---

## Task 2 — Per-workspace import mutex (Tier A)

**Problem.** `vault.ImportFile` serializes on one process-global `var importMu sync.Mutex` (`internal/vault/import.go:64`). The lock is held across conversion + the reserve-then-write sequence, so a slow PDF/OOXML conversion in workspace A blocks an unrelated import in workspace B. Four ingest doors share this path, so concurrent cross-workspace imports are ordinary.

**Fix.** Replace the single mutex with per-workspace locking — a `map[workspaceID]*sync.Mutex` guarded by a small map-lock held only during lookup/create, then the per-workspace mutex held across that workspace's reserve-and-write. Preserve the within-workspace correctness exactly (the SP23 concurrency test — N concurrent same-filename imports get distinct paths, `-race` clean — must still pass). Do not regress the fresh-allocation invariants.

**Test.** Two workspaces importing concurrently do not serialize on each other (structural assertion via the lock design, since a timing test is flaky — assert the map holds distinct mutexes); the existing same-workspace collision test still passes under `-race`.

---

## Task 3 — `vault.Bridge.Start(ctx)` parity (Tier A)

**Problem.** `vault.Bridge.Start()` (`internal/vault/bridge.go:69`) takes no context and needs an explicit `Close()`; `connectors.Bridge.Start(ctx)` auto-shuts-down on context cancellation. Wired correctly today (`defer kbBridge.Close()`), but a future adapter copying by analogy could leak the listener.

**Fix.** Change `Start()` → `Start(ctx context.Context) (string, error)` mirroring `connectors.Bridge.Start` — return the listen address, and shut the server down when `ctx` is cancelled. Update the one call site in `cmd/simple-agents/main.go`. Keep `Close()` for explicit teardown but make cancellation sufficient. All existing bridge tests pass (adapt their `Start()` calls).

**Test.** Cancelling the context stops the listener (a subsequent request fails to connect); existing round-trip tests still pass.

---

## Task 4 — pdftotext auto-install via cli-tool-installer

**Problem.** The PDF converter prefers `pdftotext` when on PATH and falls back to a pure-Go extractor that fuses words on complex/multi-column layouts. On a host without poppler every PDF gets the degraded path. The platform already has a `cli-tool-installer` skill for exactly this.

**Design.** `internal/convert` must stay pure (no side effects, no installs) — it is called from many places and must remain a deterministic function. So the install belongs at a layer that owns tool provisioning, not inside `convert`. Options, to be settled in the task:
- (a) A one-time best-effort provisioning step at `serve` startup that ensures `pdftotext` is present (via the same install path the cli-tool-installer skill uses), logged, non-fatal on failure. Simplest; makes the good path the default on every host.
- (b) Surface a clearer actionable warning in the note frontmatter when the pure-Go path ran, telling the user to install poppler.
Recommend (a) + keep (b)'s warning. Do NOT make `convert` shell out to an installer.

**Test.** `convert` behavior unchanged (still pure, still prefers pdftotext when present). The provisioning step is idempotent and non-fatal when the install is unavailable (offline / already present).

---

## Task 5 — Web-search keys in the Connections UI + wire them into chat

**Problem (UI).** `SEARCH_KEY_BRAVE`/`SEARCH_KEY_TAVILY` are consumed by `websearch.KeyedProvider` but are settable only via `/secret` — no discoverability. The operator wants them in the Connections page with copy explaining the key powers general web search across the app and agents.

**Problem (wiring).** One-off chat only injects connector-bridge tokens into the coder env (`web/handlers_misc.go:~110-129`), NOT the user's secrets — so `h.subprocessEnv["SEARCH_KEY_*"]` is empty in chat and the keyed provider never activates there. Agent runs inject all secrets (`agentrunner` `svc.GetAll`→`WithExtraEnv`), so keyed search already works in runs. The feature is half-dead in chat until this is fixed.

**Design.**
- **UI:** add a third section to `ConnectionsPage.tsx` — "Web search" — beneath Chat apps and Services, with two API-key fields (Brave, Tavily) and copy: *"A search API key makes web search more reliable across your whole workspace — chat, agents, and skills. Without one, keyless search still works."* Saving writes the corresponding `SEARCH_KEY_*` secret; showing whether each is set (not the value). Reuse the existing secret create/delete API rather than inventing endpoints if it fits; otherwise a small `/api/v1/search-keys` GET/PUT/DELETE. Never return the key value to the client — only a boolean "configured".
- **Wiring:** in chat (`handleChatMessage` and the Telegram/Discord chat path), resolve the `SEARCH_KEY_*` secrets and inject them into the coder env so `searchProviders()` sees them. This does NOT expose secrets to the model — the key is used host-side to build the provider; `web_search` itself still carries no secret and the model never sees the value. Keep the injection narrow (only the search keys, not a blanket secret dump into chat) unless a blanket inject is already how runs work and is acceptable — decide and document.
- **Placement rationale:** a search key is app-wide capability, not a chat surface (Chat apps) nor an OAuth account with actions (Services), so it gets its own section rather than being modeled as a CredSpec connector or an OAuth service.

**Verify against a live key ONLY if the operator provides one**, stored as an encrypted secret — a single smoke query, never the key in logs.

**Test.** Setting a key writes the secret and the section reports "configured"; deleting clears it; the value is never returned by the API. A chat coder built for a workspace with a stored search key gets the key in its env (unit-level, against a temp workspace).

---

## Task 6 — Web-UI chat attachments (parity with Telegram/Discord chat)

**Problem.** Sending a file works in Telegram and Discord chat (→ `vault.ImportFile`), and the KB page has upload, but the **web chat** surface has no attachment affordance. The SP23 spec listed "web chat" in Task 15's scope; it was descoped.

**Design.** Add a file attach/drop affordance to the web chat composer. On attach, POST the file (multipart) to an endpoint that runs the same `vault.ImportFile` path the KB upload uses (reuse `apiUploadKBFile`'s core, or a thin chat-scoped wrapper) and post a chat message confirming the note path + any conversion warning — mirroring how the Telegram/Discord adapters reply. Respect the same 25 MiB cap and `iolimit.ReadCapped`. An unconvertible file gets a clear in-chat error (422 → readable message), not a silent drop.

**Test.** Endpoint converts + files a CSV and returns the note path; oversized → 413; unsupported → 422. Frontend: attach affordance is keyboard-reachable and triggers the upload; a returned warning renders in the chat.

---

## Task 7 — Slack attachments (the real refactor)

**Problem.** Slack's Socket Mode loop drops subtyped `file_share` messages, and `slack.Client.GetFile(url, io.Writer)` has no size bound (no stdlib `io.LimitWriter`) and no interface seam, so it can neither be bounded during streaming nor unit-tested against an httptest server.

**Design.**
- Add a capping writer (a small `io.Writer` that errors past N bytes — the write-side analogue of `iolimit.ReadCapped`) in `internal/iolimit`, so the download is bounded during streaming and consistent with every other door.
- Introduce an interface seam for the file download (a `slackFileDownloader` interface with the one method the adapter needs) so the attachment path gets an httptest-backed unit test, matching how `slackAPINew` already abstracts `AuthTest`.
- Stop dropping `file_share` in `mapSlackDM`; extract the file metadata (`slack.Msg.Files`), download the first file bounded + guarded, and route it through the shared `Attachment` → `ImportFile` path the other adapters use.
- Re-enable the Slack "send a file" help line (currently suppressed for Slack) once this lands.

**Test.** `mapSlackDM` surfaces a `file_share` attachment; the bounded download rejects an over-cap file; the capping writer has its own unit test; an httptest-backed download test proves the end-to-end path without a live Slack workspace.

---

## Ordering

1 → 2 → 3 → 4 → 5 → 6 → 7. Tier A (1-3) first (highest value / lowest risk), then pdftotext (4), then the two features (5, 6), then the refactor (7). Each independently mergeable; stop and reassess if any task uncovers a larger problem than its spec.
