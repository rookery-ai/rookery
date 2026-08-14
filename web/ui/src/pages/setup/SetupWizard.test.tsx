import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import SetupWizard from "./SetupWizard";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: {
    id: "w1", name: "Home Server", about: "", needs_setup: true, created_at: "2026-01-01T00:00:00Z",
  },
  workspaces: [],
};

const PLATFORMS = [
  {
    platform: "telegram", label: "Telegram", blurb: "Message this workspace from Telegram.",
    setup_steps: ["Message @BotFather", "Send /newbot", "Paste the token below"],
    fields: [{ name: "token", label: "Bot token", secret: true }],
    connected: false, identity: "",
  },
  {
    platform: "discord", label: "Discord", blurb: "",
    setup_steps: [],
    fields: [{ name: "token", label: "Bot token", secret: true }],
    connected: false, identity: "",
  },
  {
    platform: "slack", label: "Slack", blurb: "",
    setup_steps: [],
    fields: [
      { name: "token", label: "Bot Token (xoxb-)", secret: true },
      { name: "app_token", label: "App-Level Token (xapp-)", secret: true },
    ],
    connected: false, identity: "",
  },
];

// A minimal in-memory mirror of web/handlers_setup.go's setupStep() so the
// test drives the wizard through a realistic step sequence and can assert
// each POST's exact request body (per web/api_settings.go's apiSetupRequest
// field names) without hand-computing next_step at every call site.
type BackendState = {
  basicsDone: boolean;
  secretsSalt: boolean;
  coderDone: boolean;
  profileDone: boolean;
  connCount: number;
  connSkipped: boolean;
  // Which platform the wizard connected, and whether the operator's /start has
  // landed — the link step polls for exactly this transition.
  connPlatform: string;
  connLinked: boolean;
  // Mirrors web.workspaceCoderReady: whether this workspace has a coder that
  // can actually run, which decides the Done screen's single closing action.
  coderReady: boolean;
};

function freshState(): BackendState {
  return {
    basicsDone: false, secretsSalt: false, coderDone: false, profileDone: false,
    connCount: 0, connSkipped: false, connPlatform: "", connLinked: false,
    coderReady: false,
  };
}

function computeStep(s: BackendState): number {
  if (!s.basicsDone) return 1;
  if (!s.secretsSalt) return 2;
  if (!s.coderDone) return 3;
  if (!s.profileDone) return 4;
  if (s.connCount === 0 && !s.connSkipped) return 5;
  return 7;
}

function setupGetBody(s: BackendState) {
  const step = computeStep(s);
  const body: Record<string, unknown> = { step, needs_setup: true };
  if (step === 3) {
    body.detected_coders = [];
    body.api_providers = [{ name: "openrouter", label: "OpenRouter", schema: "", model_placeholder: "glm-5.2", docs_url: "https://openrouter.ai/keys", requires_key: true, custom: false }];
    body.coder_catalog = [{ name: "openrouter", label: "OpenRouter", base: "https://openrouter.ai/api/v1", model: "glm-5.2", docs: "https://openrouter.ai/keys", requiresKey: true, custom: false, hasKey: false, group: "hosted" }];
  }
  if (step === 5) {
    body.platforms = PLATFORMS;
  }
  if (step === 7) {
    body.coder_ready = s.coderReady;
  }
  if (step === 7 && s.connCount > 0) {
    // Mirrors apiGetSetup's step-7 branch: the platform-keyed summary that
    // replaced the Telegram-only bot_username key.
    body.platform = s.connPlatform;
    body.platform_label = s.connPlatform === "discord" ? "Discord" : "Telegram";
    body.bot_identity = s.connPlatform === "discord" ? "rookery" : "@rookie_bot";
    body.linked = s.connLinked;
    body.linked_identity = s.connLinked ? "operator-1" : "";
    body.dm_url = "https://example.test/dm";
    body.invite_url = "https://example.test/invite";
    body.bot_online = true;
  }
  return body;
}

// The connected platform as /api/v1/setup/platforms reports it — the source
// the shared LinkStep polls during onboarding.
function setupPlatformsBody(s: BackendState) {
  return {
    platforms: PLATFORMS.map((p) => ({
      ...p,
      linked: false,
      linked_identity: "",
      primary: false,
      dm_url: "https://example.test/dm",
      invite_url: "https://example.test/invite",
      bot_online: true,
      ...(p.platform === s.connPlatform && s.connCount > 0
        ? {
            connected: true,
            identity: p.platform === "discord" ? "rookery" : "@rookie_bot",
            linked: s.connLinked,
            linked_identity: s.connLinked ? "operator-1" : "",
          }
        : {}),
    })),
  };
}

function mockFetch(state: BackendState, posts: { url: string; body: unknown }[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/setup" && method === "GET") {
        return Promise.resolve(jsonResponse(setupGetBody(state)));
      }

      // The setup-scoped mirrors the wizard's test and link phases use. They
      // exist because every /api/v1/connectors route 403s while needs_setup
      // is true.
      if (url === "/api/v1/setup/platforms" && method === "GET") {
        return Promise.resolve(jsonResponse(setupPlatformsBody(state)));
      }
      if (url.startsWith("/api/v1/setup/platforms/") && url.endsWith("/test")) {
        posts.push({ url, body });
        return Promise.resolve(jsonResponse({ ok: true, identity: "@rookie_bot" }));
      }

      if (url === "/api/v1/chats" && method === "POST") {
        posts.push({ url, body });
        return Promise.resolve(jsonResponse({ id: "chat-1", name: "Getting started" }, 201));
      }

      if (url === "/api/v1/setup" && method === "POST") {
        posts.push({ url, body });
        const req = body as Record<string, unknown>;
        switch (req.step) {
          case 1:
            state.basicsDone = true;
            break;
          case 2:
            if (typeof req.master_password !== "string" || req.master_password.length < 8) {
              return Promise.resolve(jsonResponse({ error: { code: "password_too_short", message: "master password must be at least 8 characters" } }, 400));
            }
            if (req.master_password !== req.confirm) {
              return Promise.resolve(jsonResponse({ error: { code: "password_mismatch", message: "passwords do not match" } }, 400));
            }
            state.secretsSalt = true;
            break;
          case 3:
            state.coderDone = true;
            break;
          case 4:
            state.profileDone = true;
            break;
          case 5:
            if (req.skip) {
              state.connSkipped = true;
            } else {
              const fields = (req.fields ?? {}) as Record<string, string>;
              if (req.platform && fields.token) {
                state.connCount += 1;
                state.connPlatform = String(req.platform);
              }
            }
            break;
          case 7:
            break;
          default:
            return Promise.resolve(jsonResponse({ error: { code: "invalid_step", message: "unknown step" } }, 400));
        }
        return Promise.resolve(jsonResponse({ ok: true, next_step: computeStep(state) }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/setup"]}>
        <Routes>
          <Route path="/setup" element={<SetupWizard />} />
          <Route path="/agents/new" element={<div>AGENTS NEW PAGE</div>} />
          <Route path="/kb" element={<div>KB PAGE</div>} />
          <Route path="/chats" element={<div>CHATS PAGE</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("Basics step is prefilled from the session workspace name and posts {step:1,name,about}", async () => {
  const state = freshState();
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  const nameInput = await screen.findByLabelText(/workspace name/i);
  expect((nameInput as HTMLInputElement).value).toBe("Home Server");

  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/what is this workspace about/i), "Testing");
  await user.click(screen.getByRole("button", { name: /continue/i }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ step: 1, name: "Home Server", about: "Testing" });
  expect(await screen.findByLabelText(/^master password$/i)).toBeInTheDocument();
});

test("Master password step: client-side mismatch blocks submit without a network call", async () => {
  const state = freshState();
  state.basicsDone = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(/^master password$/i), "longenoughpw1");
  await user.type(screen.getByLabelText(/confirm master password/i), "different-pw");
  await user.click(screen.getByRole("button", { name: /set master password/i }));

  expect(await screen.findByText(/passwords do not match/i)).toBeInTheDocument();
  expect(posts).toHaveLength(0);
});

test("Master password step posts {step:2,master_password,confirm} and advances to Coder", async () => {
  const state = freshState();
  state.basicsDone = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(/^master password$/i), "wizard-pw-123");
  await user.type(screen.getByLabelText(/confirm master password/i), "wizard-pw-123");
  await user.click(screen.getByRole("button", { name: /set master password/i }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ step: 2, master_password: "wizard-pw-123", confirm: "wizard-pw-123" });
  expect(await screen.findByText(/choose a coder/i)).toBeInTheDocument();
});

test("Coder step: CoderSection's Save posts step:3 coder_* fields and advances to Profile", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  expect(await screen.findByText(/choose a coder/i)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /^save coder$/i }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toMatchObject({
    // 0 = follow the server default. The wizard no longer shows a timeout
    // field at all: a brand-new owner has no basis to choose one, and the
    // number they were shown was the coder form's hardcoded 120, which the
    // wizard then wrote to the database for every workspace ever created.
    step: 3, coder_kind: "local", coder_bin: "", coder_timeout_s: 0,
    coder_provider: "", coder_model: "", coder_base_url: "", coder_api_key: "",
  });
  expect(await screen.findByText(/workspace profile/i)).toBeInTheDocument();
});

test("Coder step never renders a Test button (dropped in-wizard — the settings/coder/test endpoint is blocked while needs_setup is true)", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  mockFetch(state, []);
  wrap();

  await screen.findByText(/choose a coder/i);
  expect(screen.queryByRole("button", { name: /^test$/i })).not.toBeInTheDocument();
});

// Regression test: during setup no provider has a saved key yet (the
// step-3 coder_catalog fixture below has hasKey:false for openrouter, just
// like the real backend on a fresh workspace). The provider <select> must
// stay usable regardless — the inline API-key field supplies the key
// instead of ProviderCards (which is unreachable during setup: its
// /api/v1/secrets endpoint is also blocked by requireSetupCompleteAPI).
test("Coder step (API engine, no keys saved yet): selecting a provider via the real <select>, typing model + inline API key, and saving posts step:3 coder_provider/coder_model/coder_api_key", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  expect(await screen.findByText(/choose a coder/i)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getByRole("radio", { name: /^api$/i }));

  const openrouterOption = screen.getByRole("option", { name: /openrouter/i });
  expect(openrouterOption).not.toBeDisabled();

  await user.selectOptions(screen.getByLabelText(/^provider$/i), "openrouter");
  await user.type(screen.getByLabelText(/^model$/i), "glm-5.2");
  await waitFor(() => expect(screen.getByLabelText(/api key/i)).toBeInTheDocument());
  await user.type(screen.getByLabelText(/api key/i), "sk-or-live-test");
  await user.click(screen.getByRole("button", { name: /^save coder$/i }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({
    step: 3, coder_kind: "api", coder_bin: "", coder_timeout_s: 0,
    coder_provider: "openrouter", coder_model: "glm-5.2", coder_base_url: "",
    coder_api_key: "sk-or-live-test",
  });
  expect(await screen.findByText(/workspace profile/i)).toBeInTheDocument();
});

test("Profile step: Skip posts {step:4,skip:true} and advances to Chat app", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /skip for now/i }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ step: 4, skip: true });
  expect(await screen.findByText(/connect a chat app/i)).toBeInTheDocument();
});

test("Chat app step: picking a platform reveals its credential fields; Save is disabled until all fields are filled; posts {step:5,platform,fields}", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  expect(await screen.findByText(/connect a chat app/i)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /telegram/i }));

  const connect = await screen.findByRole("button", { name: /^connect$/i });
  expect(connect).toBeDisabled();

  await user.type(screen.getByLabelText(/bot token/i), "123:abc");
  expect(connect).not.toBeDisabled();
  await user.click(connect);

  await waitFor(() => expect(posts.length).toBeGreaterThanOrEqual(1));
  expect(posts[0].body).toEqual({ step: 5, platform: "telegram", fields: { token: "123:abc" } });

  // Saving credentials must NOT finish the wizard. setupStep() flips 5 → 7 the
  // instant a connection row exists, and onboarding used to navigate straight
  // there on next_step — which is exactly how a chat app was left connected
  // but never linked, with the operator never shown a /start instruction.
  expect(await screen.findByText(/connected as/i)).toBeInTheDocument();
  expect(screen.queryByText(/you're set up/i)).not.toBeInTheDocument();
});

test("Chat app step: a passing test advances to the link step, which waits for /start and offers no Done", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /telegram/i }));
  await user.type(await screen.findByLabelText(/bot token/i), "123:abc");
  await user.click(screen.getByRole("button", { name: /^connect$/i }));

  await user.click(await screen.findByRole("button", { name: /^next$/i }));

  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
  // The invariant the whole flow exists to hold: no completion signalled
  // before the identity row proves the inbound path works.
  expect(screen.queryByRole("button", { name: /^done$/i })).not.toBeInTheDocument();
});

test("Done screen names the real platform — never Telegram for a Discord install", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  state.connCount = 1;
  state.connPlatform = "discord";
  state.connLinked = true;
  mockFetch(state, []);
  wrap();

  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
  expect(screen.getByText(/discord linked as operator-1/i)).toBeInTheDocument();
  // The old Done screen read a Telegram-only setting key and printed "Open
  // Telegram…" regardless of the platform actually connected.
  expect(screen.queryByText(/telegram/i)).not.toBeInTheDocument();
});

test("Done screen tells an unlinked chat app how to link, and says a server message is ignored", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  state.connCount = 1;
  state.connPlatform = "discord";
  state.connLinked = false;
  mockFetch(state, []);
  wrap();

  expect(await screen.findByText(/connected but not linked yet/i)).toBeInTheDocument();
  expect(
    screen.getByText(/message posted in a server channel is ignored/i),
  ).toBeInTheDocument();
});

test("Chat app step: Skip posts {step:5,skip:true} and lands on Done", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  await screen.findByText(/connect a chat app/i);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /skip for now/i }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ step: 5, skip: true });
  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
});

test("Done: primary CTA posts {step:7}, invalidates session, and navigates to /agents/new", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  state.connSkipped = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  wrap();

  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /create your first agent/i }));

  await waitFor(() => expect(posts.some((p) => p.body && (p.body as { step: number }).step === 7)).toBe(true));
  expect(await screen.findByText("AGENTS NEW PAGE")).toBeInTheDocument();
});

// ONE closing action, never two.
//
// Two co-equal buttons asked a brand-new owner to choose between things they
// have no basis to compare, and the "Explore the knowledge base" half led to a
// knowledge base that is empty at exactly that moment.
test("Done: exactly one closing action is offered", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  state.connSkipped = true;
  mockFetch(state, []);
  wrap();

  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /explore the knowledge base/i })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /create your first agent/i })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /explore what you can do/i })).not.toBeInTheDocument();
});

// With a coder configured, the ending is a conversation: the chat can read and
// write the knowledge base, reach connected accounts, and — since its prompt
// carries the platform primer — actually answer what the platform is.
test("Done: a workspace with a coder is offered the guided chat", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  state.connSkipped = true;
  state.coderReady = true;
  const posts: { url: string; body: unknown }[] = [];
  mockFetch(state, posts);
  // jsdom's location.assign is not implemented and logs "Not implemented:
  // navigation"; stubbing it also makes the handoff assertable.
  const assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, assign });
  wrap();

  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /create your first agent/i })).not.toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /explore what you can do/i }));

  // Setup is completed AND a chat is created, then the wizard hands off to it
  // with ?intro=1 — the message itself is sent by the chat window after the
  // navigation, so the wizard never blocks on a coder call.
  await waitFor(() => expect(posts.some((p) => p.url === "/api/v1/chats")).toBe(true));
  expect(posts.some((p) => p.body && (p.body as { step?: number }).step === 7)).toBe(true);

  // Asserted as a FULL page load, not as a rendered route.
  //
  // /chats is the first wizard destination behind RequireAuth, which redirects
  // to /setup while the CACHED session still says needs_setup — which it does
  // at the moment of handoff. A client-side nav() therefore bounces the owner
  // back into the wizard they just finished. This test cannot see that
  // directly: the MemoryRouter here mounts a bare element for /chats with no
  // guard above it, which is exactly why the bug survived the first
  // implementation. Pinning the page load is the part that IS observable.
  await waitFor(() =>
    expect(assign).toHaveBeenCalledWith(expect.stringContaining("/chats?chat=chat-1&intro=1")),
  );
});

test("Back navigation: from Master password back to Basics re-shows the Basics form", async () => {
  const state = freshState();
  state.basicsDone = true;
  mockFetch(state, []);
  wrap();

  await screen.findByLabelText(/^master password$/i);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /back/i }));

  expect(await screen.findByLabelText(/workspace name/i)).toBeInTheDocument();
});

test("step chips show Basics through Chat app, and hide on the Done screen", async () => {
  const state = freshState();
  mockFetch(state, []);
  wrap();

  await screen.findByLabelText(/workspace name/i);
  const chips = screen.getByRole("list");
  expect(within(chips).getByText("Basics")).toBeInTheDocument();
  expect(within(chips).getByText("Password")).toBeInTheDocument();
  expect(within(chips).getByText("Coder")).toBeInTheDocument();
  expect(within(chips).getByText("Profile")).toBeInTheDocument();
  expect(within(chips).getByText("Chat app")).toBeInTheDocument();
});

// The wizard must not offer a timeout at all. It is not merely noise for a new
// owner: the field's value came from the coder form's hardcoded initial state,
// so every workspace created through the wizard was written with a hard
// two-minute cap that nobody chose and nothing surfaced again.
test("Coder step: no timeout field is offered during setup", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  mockFetch(state, []);
  wrap();

  expect(await screen.findByText(/choose a coder/i)).toBeInTheDocument();
  expect(screen.queryByLabelText(/timeout/i)).not.toBeInTheDocument();
});
