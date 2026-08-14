# Onboarding: one closing button, and a chat that can answer for the product

**Status:** design, 2026-08-14

The setup wizard's Done screen currently offers two equal buttons — *Create your
first agent* and *Explore the knowledge base*. Two co-equal calls to action at
the one moment a new owner has no context is a choice they have no basis to
make, and the knowledge base is empty when they arrive at it.

One button. Which one depends on whether the workspace has a coder.

## The branch, and the thing that makes it real

- **Coder configured** → **"Explore what you can do!"** — opens a new chat with a
  seeded first message, and the coder explains the platform conversationally.
- **Coder skipped** → **"Create your first agent"** → `/agents/new`.

The second branch **is currently unreachable**, and that is the first thing this
design has to fix rather than write around. The server already supports skipping
step 3 (`api_settings.go`, `case 3: if req.Skip`), but `CoderStep` renders no
Skip control, so no user can arrive at Done without a coder. The step gains a
Skip alongside its existing Back, wired to the `{step: 3, skip: true}` POST that
already works.

**"Has a coder" is not `CoderKind != ""`.** `coderKindOrDefault` fills that
column on every write, so it is non-empty for a workspace that never configured
anything. The predicate is the `wizard_coder_done` setting *plus* a usable
config: `kind == "api"` with a provider and model, or `kind == "local"` with a
bin. It is computed server-side and returned on the step-7 payload as
`coder_ready`, not inferred in the SPA from fields that mean something else.

## Seeding the chat

The Done button creates a chat (`POST /api/v1/chats`), navigates to it, and the
composer sends the opening message. Sending happens **client-side on arrival**,
not server-side before navigation, for one reason: `handleChatMessage` is a
blocking coder call, and doing it before the redirect would leave the wizard
frozen on a dead button for as long as the model takes. Client-side, the user
lands in a chat that already shows their turn and a typing indicator — the
platform's normal chat behaviour, which is also what we want them to learn.

The seeded text is **UI copy, not a prompt**. It is the message the *user*
appears to have sent, so it belongs with the button that sends it. `internal/
prompts` owns what the model is told about itself and its tools; it does not own
a sentence attributed to the user. Putting it there would also make it
unreachable from the only place that needs it.

## The substantive work is the chat's own knowledge, not the button

`BuildChatSystemPrompt` does **not** inject `platformContextBlock`. Chat gets
`productIdentityBlock(SurfaceChat)`, which names the knowledge base, agents,
skills, reminders and connected accounts — and says nothing about secrets, MCP
servers, providers, coders or chat apps. So a button labelled *"Explore what you
can do!"* would open a conversation that cannot answer the question it invites.
Shipping the button without this is shipping the disappointment.

`platformContextBlock(chatApps, vaultRoot)` is injected into the chat prompt at
both call sites (`web/handlers_misc.go`, `cmd/rookery/main.go`). It already
exists, is already maintained as the single source of platform truth for the
designer and runtime prompts, and already takes the connected chat apps — so
chat inherits future platform changes for free instead of growing a second
description that drifts.

Two constraints on doing this:

- **It must not contradict the surface.** `platformContextBlock` embeds
  `productIdentityBlock(SurfaceAgent)`, whose "right now you are an AGENT run"
  paragraph is wrong in chat and would license the output protocol markers.
  The block takes the surface as a parameter instead of hardcoding it, so chat
  gets the chat paragraph — including its existing, correct statement that chat
  cannot create agents or skills and should point at the app.
- **It costs tokens on every chat turn.** The block is substantial and chat is
  the highest-frequency coder surface. It is injected into the system prompt,
  which is cacheable and identical across turns, rather than per-turn context —
  the same reason the identity block already lives there.

## Scope

Deliberately not included: a scripted tour, a checklist, progress tracking, or
anything that follows the user after the wizard. The claim being made is that a
conversation with a model that knows the product is a better introduction than a
tour we maintain by hand — and a tour would need maintaining against every one
of the surfaces `platformContextBlock` already covers.

## Tests

- `coder_ready` is false for a skipped coder, false for `kind=api` with no model,
  true for a configured one — the predicate, not the button.
- The Done screen renders exactly one primary button, and which one, per branch.
- `CoderStep` renders Skip, and Skip posts `{step: 3, skip: true}`.
- `BuildChatSystemPrompt` contains the platform block and the chat-surface
  paragraph, and does **not** contain the agent-run paragraph — the last
  assertion is the one that catches a careless surface default.
