# Demo Workspace and README Screenshots Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild this host's Rookery install as a single fictional **Demo** workspace populated through real product flows, then capture README screenshots from it.

**Architecture:** Throwaway Python tooling in the session scratchpad (`$S` below) — a `requests`-based API client for seeding, and Playwright drivers for the setup wizard, both designers, chat and capture. The only artefacts committed to the repo are the spec, this plan, the designer behaviour log, screenshots under `docs/assets/screenshots/`, and the README edit.

**Tech Stack:** Python 3 + `requests` + Playwright (Chromium, already installed), the running `rookery` binary from `main`, `pngquant` if present.

**Spec:** `docs/superpowers/specs/2026-10-13-demo-workspace-design.md`

`$S` = `/tmp/claude-1000/-home-rookie-rookery/ac1d17b7-584b-483f-80d4-3ee828d92f0f/scratchpad/demo`. Base URL `http://127.0.0.1:8899`.

## Global Constraints

- Agents and skills are created **only** through the designers, driven via the SPA by Playwright.
- No database inserts. Everything else goes through `/api/v1` or the SPA.
- Persona: **Alex Morgan**, Lisbon, `Europe/Lisbon`, English. No real name, email, Telegram ID, hostname or token of the owner in any screenshot.
- Coder: `api` kind, provider `openrouter`, model `~deepseek/deepseek-v4-flash-latest` (the value the old install used).
- `rm -rf ~/.rookery` only after the owner's explicit go-ahead in chat (CLAUDE.md rule 2).
- No product-code changes; defects found are recorded in the designer log and filed separately.
- Screenshots: 1440×900, `device_scale_factor=2`, light and dark, `docs/assets/screenshots/<name>-{light,dark}.png`.

## Review Focus

1. **Privacy leak in a capture** — an account identity, the owner's email or a token visible on Connections/Settings. Expected: every PNG is opened and inspected before commit (Task 12 step).
2. **A1 not refused on run 1** — if Toolshop's checkout path no longer contains `checkout`/`payment`, the guard never fires and the hero story is false. Expected: Task 9 asserts the refusal text is in the run transcript before granting.
3. **Weak-model designer dead end** — a build that never offers Build/Allow-and-build. Expected: the driver times out per turn, screenshots the state, logs it, and retries with the prompt's follow-up once before falling back to a hand-typed turn.
4. **Schedules fire in the wrong zone** — a Friday 17:00 Lisbon agent firing at 17:00 host time. Expected: Task 8 checks `agent_schedules.timezone` via the agent API shows `Europe/Lisbon`.
5. **Stale server on the port** — `make deploy` says deployed while an old process still serves. Expected: `/healthz.commit` asserted equal to `git rev-parse --short HEAD` of `main`.

---

### Task 1: Scratchpad tooling — API client

**Files:**
- Create: `$S/rk.py`

**Interfaces:**
- Produces: `class RK(base: str)` with `.login(user, pw)`, `.unlock(pw)`, `.get(path)`, `.post(path, json=None, files=None, data=None)`, `.put(path, json)`, `.delete(path)`; each returns parsed JSON and raises `RKError(status, body)` on ≥400. `RK.session` is a `requests.Session` whose cookies `export_cookies()` returns for Playwright.

- [ ] **Step 1: Write the client**

```python
# $S/rk.py
import requests

class RKError(Exception):
    def __init__(self, status, body):
        super().__init__(f"{status}: {body[:400]}")
        self.status, self.body = status, body

class RK:
    def __init__(self, base="http://127.0.0.1:8899"):
        self.base = base.rstrip("/")
        self.session = requests.Session()

    def _do(self, method, path, **kw):
        r = self.session.request(method, self.base + "/api/v1" + path, timeout=600, **kw)
        if r.status_code >= 400:
            raise RKError(r.status_code, r.text)
        return r.json() if r.content and "json" in r.headers.get("content-type", "") else {}

    def get(self, p): return self._do("GET", p)
    def post(self, p, json=None, files=None, data=None): return self._do("POST", p, json=json, files=files, data=data)
    def put(self, p, json): return self._do("PUT", p, json=json)
    def delete(self, p): return self._do("DELETE", p)

    def login(self, user, pw): return self.post("/auth/login", {"username": user, "password": pw})
    def unlock(self, pw): return self.post("/auth/unlock", {"password": pw})

    def export_cookies(self):
        return [{"name": c.name, "value": c.value, "domain": "127.0.0.1", "path": "/"} for c in self.session.cookies]
```

- [ ] **Step 2: Smoke it against the current server**

Run: `cd $S && python3 -c "from rk import RK; print(RK().get('/auth/session'))"`
Expected: a JSON dict (logged-out session) — no exception.

### Task 2: Seed content files

**Files:**
- Create: `$S/seed/finance/expenses-2026.csv`, `$S/seed/clients/northwind-sow.pdf`, `$S/seed/clients/brand-refresh-brief.docx`, `$S/seed/notes/*.md`, `$S/seed/make_seed.py`

**Interfaces:**
- Produces: `make_seed.py` writes all files deterministically; `NOTES: dict[str, str]` (vault path → markdown) importable from it.

- [ ] **Step 1: Write `make_seed.py`** generating:
  - `expenses-2026.csv` — columns `Date,Vendor,Description,Category,Amount,Currency,IVA %,Payment`; 120 rows Jul–Sep 2026, categories `Software`, `Hardware`, `Travel`, `Meals`, `Coworking`, `Professional services`, `Telecom`; ~25% USD (GitHub, Figma, Vercel, OpenRouter, Linear), rest EUR (Second Home Lisboa coworking, CP Comboios, NOS, Pingo Doce, TAP, an accountant). Seeded `random.Random(2026)` so reruns are identical.
  - `northwind-sow.pdf` — 2-page Statement of Work for "Northwind Logistics" (scope, milestones table, rate €95/h), written with headless Chromium `page.pdf()` from inline HTML (no new dependency).
  - `brand-refresh-brief.docx` — built with stdlib `zipfile` + minimal WordprocessingML (heading, 2 paragraphs, a 3-row table, one embedded PNG logo generated by Pillow if installed, else skipped).
  - `NOTES`: `travel/Lisbon places.md` (15 places split outdoor/indoor with neighbourhoods), `travel/Sintra day trip.md`, `clients/Northwind.md`, `clients/Halcyon Labs.md`, `notes/Weekly review.md`, `notes/Q4 goals.md` (deliberately wordy — the Edit-with-AI target), `notes/Home lab.md` (callout `> [!tip]`, `<details>` toggle on separate lines, `<div data-cols="2">` columns with blank-line-separated cells, a table, `![rack|420](uploads/rack.png)`), all cross-linked with `[[wikilinks]]`.

- [ ] **Step 2: Run and verify**

Run: `cd $S && python3 make_seed.py && wc -l seed/finance/expenses-2026.csv && file seed/clients/*`
Expected: `121` lines; PDF and Zip/Microsoft Word document types.

### Task 3: Playwright driver library

**Files:**
- Create: `$S/drive.py`

**Interfaces:**
- Consumes: `RK.export_cookies()`.
- Produces: `browser_ctx(rk, theme="light") -> (pw, browser, ctx, page)`; `design(page, kind, opening, followups: dict[str, str], log) -> dict` (kind `agent`|`skill`; `followups` maps a lowercase substring of a designer question to the answer; returns `{"turns", "plan_ready", "allow_and_build", "outcome", "seconds"}`); `chat(page, chat_id, text, timeout=300)`; `shot(page, name)`.

- [ ] **Step 1: Inspect the designer DOM** — `grep -rn "Approve & build\|Allow and build\|data-testid" web/ui/src/components/designer/*.tsx | head -40` and the composer in `web/ui/src/components/chat/Composer.tsx`; record the real selectors (button text, textarea role) in a comment at the top of `drive.py`.
- [ ] **Step 2: Implement** using those selectors: type into the composer, press send, wait until the typing indicator disappears and a new assistant bubble appears (per-turn timeout 300 s), answer the first follow-up whose key substring appears in the last assistant turn, otherwise click the build button when visible (`Approve & build` or `Allow and build`), then wait for the review card (`Save`) with a 45-minute timeout, screenshot every turn to `$S/log/<name>-<n>.png`, and click **Save**.
- [ ] **Step 3: Dry-check on the old install** — open `/agents/new` with the existing session, type nothing, screenshot, close. Expected: the screenshot shows the designer surface (selectors resolve).

### Task 4: Teardown and fresh deploy  ⛔ needs owner go-ahead

- [ ] **Step 1: Final backup** — `cd ~/rookery && ./bin/rookery backup run 2>&1 | tail -3` (use the subcommand `./bin/rookery backup --help` lists); copy the newest `~/.rookery/backups/*.rkb` to `~/rookery-pre-demo.rkb`; `ls -la ~/rookery-pre-demo.rkb`.
- [ ] **Step 2: Stop** — `make stop && make status`. Expected: no server process.
- [ ] **Step 3: Ask the owner** for the go-ahead *and* the secrets list (spec §Secrets) in one message. Do not proceed without an explicit yes.
- [ ] **Step 4: Wipe** — `rm -rf ~/.rookery`.
- [ ] **Step 5: Deploy from main** — `git -C ~/rookery switch main && git -C ~/rookery pull --ff-only && make deploy`; then `curl -s 127.0.0.1:8899/healthz` and assert `.commit == $(git rev-parse --short HEAD)`.
- [ ] **Step 6: Bootstrap owner** — `./bin/rookery owner bootstrap -u alex -p "$OWNER_PW"` with a generated password stored in `$S/creds.json` (mode 600).
- [ ] **Step 7: Return to the branch** — `git switch docs/demo-workspace-and-screenshots`.

### Task 5: Workspace via the setup wizard (Playwright)

- [ ] **Step 1:** Log in through the SPA, create workspace **Demo** (About: "Alex Morgan's freelance practice and life admin — clients, finances, travel and the home lab."), walk the wizard: master password (generated, saved to `creds.json`); coder `api` / OpenRouter / model per Global Constraints / pasted key; profile (Alex Morgan, alex@alexmorgan.dev, Lisbon, Portugal, `Europe/Lisbon`, concise and friendly, English); Telegram connector with the owner's demo bot token; finish. Screenshot each step to `$S/log/wizard-*.png`.
- [ ] **Step 2:** `PUT /settings/workspace/icon` with the `rookery` preset.
- [ ] **Step 3: Verify** — `rk.get('/settings')` shows coder provider `openrouter`; `POST /settings/coder/test` returns ok.

### Task 6: Secrets, search key, connections

- [ ] **Step 1: Register a Toolshop account** with Playwright at `https://practicesoftwaretesting.com/auth/register` (Alex Morgan, Lisbon address, generated email `alex.morgan.demo+<random>@example.com` if the form accepts it, generated password). Log in once to confirm.
- [ ] **Step 2: Secrets** — `POST /secrets` for `TOOLSHOP_EMAIL`, `TOOLSHOP_PASSWORD`, `CARD_HOLDER=Alex Morgan`, `CARD_NUMBER=4111-1111-1111-1111` (format the Toolshop field expects, checked in step 1), `CARD_EXPIRY` (12/2028 in the field's format), `CVV=123`.
- [ ] **Step 3: Brave** — `PUT /search-keys {"provider":"brave","key":…}`; expect `ok: true`.
- [ ] **Step 4: Keyless connections** — `POST /services/<p>/apikey {}` for `open_meteo`, `wikipedia`, `hackernews`, `frankfurter`, `openstreetmap`.
- [ ] **Step 5: Paste-key connections** — `POST /services/github/apikey {"key": PAT, "label": "alexmorgan-dev"}`, `POST /services/todoist/apikey {"key": token, "label": "personal"}`.
- [ ] **Step 6: Google** — `POST /services/google/creds` with the owner's client id/secret, then ask the owner to click Connect for `google` and `google_calendar` on `/connections` from `http://localhost:8899` (directly or `ssh -L 8899:localhost:8899 rookie@<host>`). ⛔ owner action.
- [ ] **Step 7: Verify** — `GET /services`: every listed connection `ACTIVE`.

### Task 7: Knowledge base, reminders, skills

- [ ] **Step 1: Uploads** — `POST /kb/upload` multipart `file=…`, `dir=finance` / `clients`. Then `GET /kb/note?path=finance/expenses-2026.md` → `kind == "markdown"`, contains `| Vendor |`.
- [ ] **Step 2: Notes** — for each `NOTES` entry: `POST /kb/new {"path": p}` then `PUT /kb/note {"path": p, "content": md}`; upload `rack.png` with `POST /kb/asset` first. Verify `GET /kb/tree` lists them.
- [ ] **Step 3: Open `notes/Home lab` in the SPA** and assert it is NOT read-only (no fidelity banner) — screenshot.
- [ ] **Step 4: Reminders** — `POST /reminders {"text": …}` for the three spec reminders plus "in 3 minutes to stretch" and "in 5 minutes to check the Northwind invoice status". Verify `GET /reminders` has 5 rows with Lisbon-local times.
- [ ] **Step 5: Skills** — `design(page, "skill", S1 prompt, {...})`, then S2. Append to the designer log (Task 11 format). Verify `GET /skills` lists both.

### Task 8: Agents A2–A7 via the designer

For each agent in spec order A2…A7: `design(page, "agent", prompt, followups)` with the spec's prompt/follow-ups; on Save, `GET /agents/:id` and assert schedule and (A2) timezone `Europe/Lisbon`, bindings per spec "Expect"; log to the designer log. One retry on a dead end (Review Focus 3), rephrase logged.

- [ ] A2 · Weekend in Lisbon
- [ ] A3 · Self-hosting digest
- [ ] A4 · Morning brief (requires Task 6 step 6)
- [ ] A5 · Issue → task (requires GitHub repo + Todoist project from the owner)
- [ ] A6 · Framework price watch
- [ ] A7 · Monthly expenses (requires S2)

### Task 9: A1 hero agent — build, refuse, grant

- [ ] **Step 1: Check the sandbox** — Playwright: log into Toolshop, add an item, go to checkout; assert URL path contains `checkout`; assert the payment step offers Credit Card fields.
- [ ] **Step 2: Design A1** — stop before clicking the build button; screenshot the bar showing **Allow and build** (light + dark, saved straight into `$S/hero/`); then click it, wait for Save, save.
- [ ] **Step 3: Withdraw** — `PUT /agents/:id/browser {"irreversible": false}`.
- [ ] **Step 4: Run 1** — `POST /agents/:id/run`; poll `GET /agents/:id` until the run finishes; `GET /agents/:id/runs/:runID`; assert the transcript contains a refusal mentioning the payment/order page. Screenshot the agent page showing the permission card.
- [ ] **Step 5: Grant + Run 2** — `PUT /agents/:id/browser {"irreversible": true}`; run again; assert the transcript shows `${CARD_NUMBER}` placeholders and the final reply carries an invoice number.

### Task 10: Manual runs and chats

- [ ] **Step 1:** `POST /agents/:id/run` for A2–A7 sequentially (scheduler cap is 3; manual runs are not capped, so run one at a time); record exit/silent per run.
- [ ] **Step 2: Chats** — create 3 chats and send the spec's three questions via the SPA (Playwright, so the activity card renders); then open `notes/Q4 goals`, select its body, use **Edit with AI** with "Tighten this into three goals with one measurable outcome each."
- [ ] **Step 3: Verify** — each chat's last assistant message non-empty and contains no `[CHAT]`; the Q4 note content changed.

### Task 11: Designer behaviour log

**Files:** Create `docs/superpowers/specs/2026-10-13-demo-designer-log.md`

- [ ] **Step 1:** One section per S1, S2, A1–A7: prompt as sent, turns (designer questions verbatim, answers sent), plan_ready/Allow-and-build seen, build duration, `build_id` (from `grep build_id logs/server.log`), dry-run outcome, retries/rephrases, defects worth filing.
- [ ] **Step 2: Commit** — `git add docs/superpowers/specs/2026-10-13-demo-designer-log.md docs/superpowers/plans/2026-10-13-demo-workspace.md && git commit -m "docs(specs): demo designer behaviour log"`.

### Task 12: Days 2–3, capture, README, PR

- [ ] **Step 1 (day 2 and day 3):** check `GET /dashboard` and each agent's last runs; note failures in the log.
- [ ] **Step 2: Capture script** `$S/capture.py`: for theme in light/dark (set via the SPA's theme toggle / `localStorage` key found with `grep -rn "localStorage" web/ui/src/lib/theme*`), visit and screenshot: A1 run-2 timeline (expanded run row), A1 design transcript (resume from history or the saved hero shots from Task 9), `/` (Home), `/kb?path=notes/Home lab`, the frame.work chat, `/connections`, `/skills`, A2's agent page.
- [ ] **Step 3: Compress + inspect** — `pngquant --force --ext .png docs/assets/screenshots/*.png` if installed; open every PNG with the Read tool and check for the owner's real identity, tokens or hostnames. Re-shoot anything that leaks.
- [ ] **Step 4: README** — replace the commented hero block with live `<picture>` markup for `run-timeline` and `build-conversation`, alt text describing refuse-then-grant; rewrite the HTML comment accordingly; add a `## Tour` section after "What you get" with the six gallery shots (`<picture>` per shot).
- [ ] **Step 5: docs-sync** — invoke the `docs-sync` skill; `make ci-docs`.
- [ ] **Step 6: Commit, push, PR** — `git add docs/assets/screenshots README.md && git commit -m "docs(readme): demo screenshots and tour"`; `git push -u origin docs/demo-workspace-and-screenshots`; `gh pr create --title "docs(readme): demo screenshots and tour"` with body ending in the attribution line.
