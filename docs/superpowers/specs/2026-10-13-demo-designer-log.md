# Demo build — designer behaviour and defect log

Companion to `2026-10-13-demo-workspace-design.md`. Install: v0.17.1 (`95d1a9c`),
coder `openrouter/~deepseek/deepseek-v4-flash-latest`, fresh data dir.

## Deviations from the spec

- **Setup wizard** was driven through `POST /api/v1/setup` (the endpoint the wizard
  UI posts each step to) rather than by clicking through it — same code path, and
  the wizard is not a screenshot target.
- **Google and GitHub use the owner's own accounts** (decided 2026-10-13). A4 reads
  only the Gmail label `rookery-demo` and a "Demo" calendar; Google cards are
  cropped from the Connections capture. A5 watches `rookery-ai/rookery`.
- **GitHub is OAuth-only** in the provider registry (`github.yaml`), so the PAT the
  plan assumed cannot connect it; it needs an OAuth App + Connect. The PAT is kept
  as secret `GITHUB_TOKEN`.
- **Toolshop's whole checkout is `/checkout`** (address and payment steps included)
  and the cart button is "Proceed to checkout", so without the grant run 1 is
  refused at the first checkout click — before any card field — not at the card
  field as the spec anticipated.

## Defects found

### D1 · Every imported file opens read-only in the KB editor

Uploading a CSV, PDF and DOCX through `POST /api/v1/kb/upload` produced notes
that all opened with the "Opened as a read-only rich view" banner. Diff after
"Edit as rich text anyway" + save:

- the converter's provenance line `_Converted from [x](uploads/x)._` is
  re-serialized as `*Converted from …*` — underscore emphasis is not a fixed
  point of the editor, so **every** imported note fails `checkFidelity`;
- when the filename contains spaces the provenance link's destination is not
  encoded (`(uploads/Halcyon Labs - Brand refresh brief.docx)`), which is not a
  valid CommonMark link, so on save it degrades to escaped literal text
  `\[…\](…)` — the link to the original is lost;
- `pdftotext -layout` column runs (`Milestone      Target date      Payment`)
  collapse to single spaces on save.

Hand-authored notes also tripped it on `|---|` table delimiters (serialized as
`| --- |`) and a bare `~` (escaped to `\~`).

`internal/convert`'s fidelity corpus apparently does not include the provenance
line `vault.ImportFile` prepends. Not fixed here (out of scope) — file separately.

## Designer runs

### S2 · pt-expense-rules (skill) — saved

- **Turns:** 4 design turns, then build. The designer opened by citing
  `finance/expenses-2026.md` and the accountant's name from the KB unprompted —
  the read-only design tools working as intended — and asked two scoping
  questions (what the skill returns; where the rules come from).
- **Build:** 43 s, reasoning-only (no `scripts/`), frontmatter validated; build
  log shows `kb_file_map` + `read_file` over the expenses table.
- **Vetting:** LOW risk, "safe to save".
- **Observations:** (1) the first driver attempt clicked *Approve & build* after
  the opening question, because the skill designer shows that button after
  every turn (no plan-ready signal; documented in CLAUDE.md) — a human could do
  the same and get a build of an unscoped skill. (2) the vetting report renders
  as one run-on bullet (`Red flags: - None Permissions needed: … Verdict: …`) —
  its line structure is lost in the review card. (3) the activity card still
  reads "Building your skill… 0:42" with a half-full bar after the build
  finished. (4) the skill designer logs nothing to `server.log` (the agent
  designer logs five `build_id` lines per build).

### S1 · meeting-notes (skill) — saved

- **Turns:** 5 design turns. The designer noticed `clients/Northwind.md` already
  has a dated *Log* section and no `meetings/` folder, and asked how transcripts
  arrive twice (the scripted answers didn't cover it) before proposing.
- **Build:** 56 s, reasoning-only. Vetting LOW / safe to save.

### A2 · Weekend in Lisbon — saved (`build_id=cdadbf4f`, 84 s)

- **Turns:** 3. The first reply had already read `travel/Lisbon places` (named its
  Outdoor / Rainy day sections) and checked the forecast source before asking
  anything. Asked "both days?" and how to deliver; proposed a plan that matches
  the outdoor plan to the nicer day.
- **Result:** TIER 1 (no script), `# Connections: open_meteo`, cron `0 17 * * 5`
  stored in `Europe/Lisbon` (next run 16:00Z). `script_verified=false` as
  expected for TIER 1 — the dry run supplied the executed evidence.

### A3 · Self-hosting digest — saved (`build_id=44979672`, 366 s)

- **Turns:** 1. The designer checked Hacker News itself and proposed a complete
  plan in its first reply — none of the scripted follow-ups were needed.
- **Result:** authored a script and the engine confirmed it ran
  (`script_verified=true`); bound `hackernews`; five core skills declared
  including `change-detection` and `web-research`; `0 8 * * 1-5`.

### A6 · Framework price watch — saved, then REPLACED (`build_id=649438ad`, 381 s)

- **Turns:** 2. During design it probed frame.work and told the user up front that
  the page has a security check, which the agent would try to get through with a
  real browser.
- **Dry run:** Cloudflare blocked both `web_fetch` and the browser; the agent
  refused to guess a price and reported the failure honestly — correct
  behaviour, but a demo agent that fails daily. Headless checks of alternatives:
  worten.pt, backmarket.pt and raspberrypi.com are also behind a challenge
  (403 "Just a moment…"); ikea.com/pt renders with prices. Replaced by an IKEA
  MARKUS price watch (spec risk "frame.work may block headless Chromium").
- **Observation:** the dry-run text shown to the user contains internal
  narration — "Per my instructions…", "No [STATE] update: the read failed…" —
  i.e. the model's reasoning about the protocol leaks into the review card.

### A7 · Monthly expenses — saved (`build_id=cf0b82da`, 347 s)

- **Turns:** 3. Found `finance/expenses-2026.md` and the Frankfurter connection
  on its own and asked only where to save and what to notify.
- **Result:** bound `frankfurter`; `0 9 2 * *`. **Did not attach the user skill
  `pt-expense-rules`** — the designer never asked about categorisation rules,
  and the opening prompt didn't name the skill, so the scripted answer that
  mentions it never fired. Attached afterwards on the agent page's Skills card
  (`PUT /agents/:id/skills`). The designer's `<available_skills>` block did list
  it; a skill whose description matches the task ("categorise … expenses")
  being skipped is worth a look.

### Cross-cutting observations

- The build activity card stays in its running state ("Building your agent…
  6:21", spinner and bar) after the build finished and the review card
  rendered — on every agent and skill build so far.

### A1 · Toolshop restock (hero) — saved (`build_id=b01e1734`, 170 s)

- **Design:** 2 turns. The designer probed the shop before asking anything, then
  asked for the run time and told the user to *create* `TOOLSHOP_EMAIL` /
  `TOOLSHOP_PASSWORD` secrets — which already existed. **The designer is not told
  which secret names are stored**; once the user named them it confirmed they
  were present. The plan carried `Irreversible actions:` and the bar showed
  **Allow and build** with the warning line — hero shot 2 captured here.
- **Grant:** approving via *Allow and build* set `browser_irreversible=true` at
  save (as designed). Withdrawn on the agent page for run 1.
- **Sandbox:** the self-registered Toolshop account was deleted by the sandbox's
  periodic reset within ~1 h ("Invalid email or password" on the first run —
  reported honestly, no retry storm). Switched to the seeded `customer2` account.
- **Run 1 (no grant):** logged in with `${TOOLSHOP_EMAIL}`/`${TOOLSHOP_PASSWORD}`
  placeholders, built and verified the cart ($39.78), was refused at "Proceed
  to checkout", placed nothing, and told the owner exactly which permission to
  grant. The intended behaviour, end to end.
- **Run 2 (granted):** order placed (INV-2026000003, $39.78), but:
  - it **invented a billing address** ("Main Street, Springfield, IL") when the
    form had empty fields;
  - the country field is a native `<select>` and **there is no browser tool for
    choosing an option** (tools: open/read/page/click/fill/press/wait;
    `browser_fill` on a select errors) — it pressed `u`/`Enter` ~16 times;
  - it then **abandoned the browser and placed the order via the shop's REST API
    with `bash`+`curl`** — the residual CLAUDE.md records ("an agent with bash
    can still POST to a payment endpoint, and no browser permission covers
    that"). Granted here, so not a policy breach, but the same path is open to
    an agent WITHOUT the grant;
  - five `set_state({"patch": {}})` calls with empty patches before falling back
    to `[STATE]`.
  Remedy for the demo: edit the agent conversationally (address + browser-only)
  and use a freshly registered account whose country is pre-filled at checkout.

## Chats

- **September spending** — `kb_table_query`×2 + `search_files`; Frankfurter
  failed twice (intermittent TLS errors from this host — `curl` succeeds minutes
  later), so it fell back to an ECB rate via `web_fetch` and stated which rate it
  used. It also **edited A7's summary note** to add converted figures — unasked.
- **Weekend + Sintra** — `weather_geocode` → `weather_forecast` + `search_files`
  + `read_file`; correct, concise, tied the note's "dry Saturday" plan to the
  forecast. The best chat of the set.
- **Desk chair (IKEA)** — correct answer (price, specs, stock signals with an
  honest caveat) but **17 minutes and 92 tool calls** (`web_fetch`×55 with 23
  errors, `browser_read`×29, `web_search`×6) — a weak-model thrash the
  unproductive-streak guard did not stop because many calls "succeeded".

### D2 · `set_state` receives an empty patch on DeepSeek Flash — HIGH

Every agent that used the `set_state` host tool sent `{"patch": {}}` (the
transcript records the arguments as received). The Desk chair price watch called
it **149 times in one run** and stopped only at the 150-turn hard ceiling; its
reply says "the state patch kept going through empty", i.e. the model was trying
to write keys. Toolshop run 2 did the same five times before falling back to the
`[STATE]` marker. Consequence: agent memory silently does not persist, so a
change-detection agent re-baselines on every run and never notifies.

- Schema (`internal/coder/hosttools.go:327`): `patch` is
  `{"type":"object"}` with no `properties` and no `additionalProperties`.
  Hypothesis (unverified): the provider's constrained tool-call decoding treats
  an object with no declared properties as `{}`, so the model *cannot* emit
  keys. Worth testing `additionalProperties: true` or a JSON-string parameter.
- Secondary: the unproductive-streak guard did not stop 149 identical calls —
  `set_state` returns success for an empty patch, so each call counts as
  productive. An empty patch should arguably return an error-free *notice*
  that nothing was written, and identical consecutive calls should count as a
  repeat.

### A1b · Replacement monitor (demoblaze) — replaces Toolshop as the hero

Toolshop's checkout is blocked by D3 (below) for any account whose country is
empty, and the sandbox resets accounts hourly. demoblaze.com's "Place order"
form is all text inputs on `/cart.html` and its final control is **Purchase**,
so the guard should allow filling the card fields and refuse at Purchase — the
story the README slot originally described.

### D3 · No browser tool can choose an option in a native `<select>`

Tools are open/read/page/click/fill/press/wait. `browser_fill` on a `<select>`
errors, and per-call `browser_press` cannot drive type-ahead (calls are seconds
apart). Toolshop run 4 pressed `ArrowDown` 105 times and then — correctly,
following its edited instructions — stopped without bypassing the form. Any
checkout or sign-up with a country/state dropdown is out of reach.

### D4 · Fields inside a Bootstrap fade modal never appear in the page snapshot

demoblaze.com's login is a Bootstrap `.modal.fade`. After clicking "Log in" the
agent's `browser_page` snapshot never listed `#loginusername` /
`#loginpassword`, across 24 clicks and ~100 Tab presses; `browser_wait` on the
selector did not help. Likely the aria snapshot is taken while the modal is
still `aria-hidden`/animating, or `FilterInteractive` drops it. Run 2 (granted)
then started reverse-engineering the site's JS with `curl` and ended after 30
minutes with **empty output** — a produced-nothing run that the runner should
have flagged.

### Outcome for the hero shot

Five purchase attempts across two sandbox shops; none produced a clean
completed-purchase timeline. Causes are D3 and D4 (tool gaps) amplified by the
weak model's habit of falling back to `bash`+`curl`. The demoblaze agent was
deleted. Kept: Toolshop's refused run (`52738130…`) — logged in via
placeholders, verified the cart, refused at "Proceed to checkout", nothing
placed — though its timeline also contains `curl` calls against the shop API.

## Day 1, afternoon — fixes and the hero retry

The owner asked for every finding to be fixed before retrying. Fix PRs:

| Defect | PR | Root cause (verified) |
|---|---|---|
| D2 `set_state` empty patch | #346 | An OpenRouter upstream compiles `{"type":"object"}` with no properties as CLOSED, so the decoder could only emit `{}`. Found by capturing a real run through a logging proxy and replaying the request; a standalone probe routed elsewhere and did not reproduce. |
| D3 no `<select>` support | #347 | `Locator.Fill` rejects a select; `browser_fill` now chooses the matching option. |
| D4 modal fields invisible | #348 | Not the modal: demoblaze's `<label for>` points at the wrong id, so the inputs had no accessible name and `FilterInteractive` dropped them. Unnamed fields now borrow the preceding visible text. |
| D1 imported notes read-only | #349 | The provenance line ImportFile writes (`_…_`, unescaped destination) and PDF layout gaps were not editor fixed points; the fidelity corpus now includes real assembled import notes. |
| Designer polish | #350 | Stale "Building…" title (and, caught in self-review, it must not say "Built" for a failed build), run-on vetting report, untraced skill builds, designer unaware of stored secret names. |
| D5 timeout reported as "provider dropped the connection" | #356 | The API engine checked its deadline only at the top of a turn; a deadline expiring inside the provider call escaped as a generic API error, classified `canceled`, and skipped the timeout retry. |

Issues filed for model-behaviour findings: #351 (bash+curl fallback, secrets readable via the bash env), #352 (dry-run narration), #353 (matching user skill skipped), #354 (92-call chat thrash, timed-out runs record zero usage, transcript lacks tool results).

**Hero, on a local integration build (main + #346–#349):**

- Toolshop resets its database **every hour on the hour** — the cause of the vanished accounts. Runs must start just after :00.
- With D3 the agent chose "Portugal" from the country dropdown and filled the
  billing address from its edited instructions; with D2 its `set_state` calls
  carried keys.
- One attempt failed on MY setup error (the `CARD_NUMBER` secret had been
  overwritten without hyphens for demoblaze); the agent diagnosed the format
  correctly and placed nothing.
- One attempt reached "Payment was successful" but its Confirm click did not
  land (no tool results are recorded anywhere, so the cause is unknown — #354).
- **Run `27c29641`** placed **INV-2026000008, $39.78**, verified on the shop's
  invoices page: login, cart, billing address, `${CARD_NUMBER}` / `${CARD_EXPIRY}` /
  `${CVV}` / `${CARD_HOLDER}`, Check payment, Confirm — all in the browser. Its
  `bash` calls were read-only product discovery against the shop's public API.

**A5 · Issue to task** — first build ran exactly 30:00 and hit the coder timeout
(D5 above); rebuilt by approving again.

**Edit with AI** (Q4 goals) — the button auto-sends the selection with no
instruction, so the model must first ask "what's the edit?"; with the
instruction it proposed a grouping, asked once more, then rewrote the note into
three goals with outcomes and dates. Two extra round trips for one edit.

## Day 1, evening

- **More fixes:** #356 (deadline mid-request reported as "provider dropped the
  connection", now `ErrTimeout`), #358 (run notes opened read-only: coder turns
  written as prose, and tiptap-markdown hard-codes a three-backtick fence, so
  any code block containing ``` broke on save), #359 (a click or fill step
  names its element instead of an opaque ref), #360 (agent page showed raw
  provider slugs like "Google_calendar").
- **#350 self-review catch:** a failed build's progress stream also ends with
  `done`, so the status-following title would have said "Built and tested"
  above a failure; fixed before merge.
- **Hero, final:** run `0eb815a7` placed **INV-2026000006** (verified on the
  shop) in 7 minutes. Its steps now read `browser_fill "Credit Card Number" ←
  ${CARD_NUMBER}` … `browser_click "Confirm"`, and the run note is editable.
  One attempt before it got an empty provider response (`exit -1`,
  `total_tokens=0` — usage lost on error, #354).
- **A5 · Issue to task** filed five Todoist tasks (#351–#354, #357) and
  remembers them; bindings had to be trimmed (auto-bind added Google Calendar
  the build had merely probed — #357).
- **Capture environment:** headless Chromium here needed an emoji font
  (Noto Color Emoji) and a symbol fallback (DejaVu Sans, for "✓"); the browser
  context runs in `Europe/Lisbon`; the Telegram user id and bot name on the
  Connections page are blurred in captures.
- **Blocked on the owner:** the Google Calendar API is disabled in the OAuth
  project, so A4 (Morning brief) waits; a background orchestrator builds it as
  soon as Calendar answers.
