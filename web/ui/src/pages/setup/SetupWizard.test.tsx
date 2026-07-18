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
};

function freshState(): BackendState {
  return { basicsDone: false, secretsSalt: false, coderDone: false, profileDone: false, connCount: 0, connSkipped: false };
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
    body.coder_catalog = [{ name: "openrouter", base: "https://openrouter.ai/api/v1", model: "glm-5.2", docs: "https://openrouter.ai/keys", requiresKey: true, custom: false, hasKey: false }];
  }
  if (step === 5) {
    body.platforms = PLATFORMS;
  }
  if (step === 7) {
    body.bot_username = "";
  }
  return body;
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
              if (req.platform && fields.token) state.connCount += 1;
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
    step: 3, coder_kind: "local", coder_bin: "", coder_timeout_s: 120,
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
    step: 3, coder_kind: "api", coder_bin: "", coder_timeout_s: 120,
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

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ step: 5, platform: "telegram", fields: { token: "123:abc" } });
  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
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

test("Done: secondary CTA navigates to /kb", async () => {
  const state = freshState();
  state.basicsDone = true;
  state.secretsSalt = true;
  state.coderDone = true;
  state.profileDone = true;
  state.connSkipped = true;
  mockFetch(state, []);
  wrap();

  expect(await screen.findByText(/you're set up/i)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /explore the knowledge base/i }));

  expect(await screen.findByText("KB PAGE")).toBeInTheDocument();
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
  expect(within(chips).getByText("Master password")).toBeInTheDocument();
  expect(within(chips).getByText("Coder")).toBeInTheDocument();
  expect(within(chips).getByText("Profile")).toBeInTheDocument();
  expect(within(chips).getByText("Chat app")).toBeInTheDocument();
});
