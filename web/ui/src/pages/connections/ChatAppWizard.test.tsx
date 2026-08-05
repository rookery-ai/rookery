import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell, useSlideOver } from "@/components/shell/AppShell";
import { ChatAppWizard } from "./ChatAppWizard";
import type { ConnectorPlatform } from "@/lib/connections";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: {
    id: "w1",
    name: "ws1",
    about: "",
    needs_setup: false,
    created_at: "2026-01-01T00:00:00Z",
  },
  workspaces: [],
};

const NOT_CONNECTED: ConnectorPlatform = {
  platform: "slack",
  label: "Slack",
  blurb: "Slack is where your assistant will message you.",
  setup_steps: [
    "Create a Slack app at https://api.slack.com/apps",
    "Copy the bot token and paste it below",
  ],
  fields: [{ name: "bot_token", label: "Bot token", secret: true }],
  connected: false,
  identity: "",
  linked: false,
  linked_identity: "",
  primary: false,
  dm_url: "",
  invite_url: "",
};

const CONNECTED: ConnectorPlatform = {
  platform: "telegram",
  label: "Telegram",
  blurb: "",
  setup_steps: [],
  fields: [{ name: "bot_token", label: "Bot token", secret: true }],
  connected: true,
  identity: "@rookie_assistant_bot",
  linked: true,
  linked_identity: "123456789",
  primary: true,
  dm_url: "",
  invite_url: "",
};

type Handlers = {
  save?: (body: { platform: string; values: Record<string, string> }) => Response;
  test?: (platform: string) => Response;
  del?: (platform: string) => Response;
  // GET /api/v1/connectors — only needed by tests that reach the link step's
  // polling read (LinkStep, whether via ConnectWizard or Manage's unlinked
  // branch). Every other test's default fallback response ({}) is harmless
  // there because ConnectorPlatform.find falls back to the static prop.
  list?: () => Response;
  // DELETE /api/v1/connectors/:platform/identity — Unlink. Distinct from
  // `del` (Disconnect), which hits the bare platform path.
  unlink?: (platform: string) => Response;
};

function mockFetch(handlers: Handlers = {}) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/connectors" && method === "GET") {
        return Promise.resolve(handlers.list ? handlers.list() : jsonResponse({}));
      }

      if (url === "/api/v1/connectors" && method === "POST") {
        const body = init?.body ? JSON.parse(String(init.body)) : { platform: "", values: {} };
        return Promise.resolve(
          handlers.save ? handlers.save(body) : jsonResponse({ ok: true, identity: "x" }),
        );
      }

      const testMatch = url.match(/^\/api\/v1\/connectors\/([^/]+)\/test$/);
      if (testMatch && method === "POST") {
        return Promise.resolve(
          handlers.test
            ? handlers.test(testMatch[1])
            : jsonResponse({ ok: true, identity: "identity-x" }),
        );
      }

      const unlinkMatch = url.match(/^\/api\/v1\/connectors\/([^/]+)\/identity$/);
      if (unlinkMatch && method === "DELETE") {
        return Promise.resolve(
          handlers.unlink ? handlers.unlink(unlinkMatch[1]) : jsonResponse({ ok: true }),
        );
      }

      const delMatch = url.match(/^\/api\/v1\/connectors\/([^/]+)$/);
      if (delMatch && method === "DELETE") {
        return Promise.resolve(handlers.del ? handlers.del(delMatch[1]) : jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function Opener({ platform }: { platform: ConnectorPlatform }) {
  const { open } = useSlideOver();
  return (
    <button
      onClick={() =>
        open(<ChatAppWizard platform={platform} />, {
          title: `${platform.connected ? "Manage" : "Connect"} ${platform.label}`,
        })
      }
    >
      open wizard
    </button>
  );
}

function wrap(platform: ConnectorPlatform) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Opener platform={platform} />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// Mounts ChatAppWizard directly (not behind an "open wizard" click) inside
// the same AppShell/QueryClientProvider/MemoryRouter wrapper the other tests
// use — the shell still supplies useSlideOver's context, ChatAppWizard just
// isn't behind the Sheet this time. Stubs fetch for the session probe, the
// connectors list (so the link step's poll has something to read), a save
// that authenticates, and a test call that reports the platform's own
// declared identity.
function renderWizard(platform: ConnectorPlatform) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/connectors" && method === "GET") {
        return Promise.resolve(jsonResponse({ platforms: [platform] }));
      }

      if (url === "/api/v1/connectors" && method === "POST") {
        return Promise.resolve(jsonResponse({ ok: true, identity: "rookery_bot" }));
      }

      const testMatch = url.match(/^\/api\/v1\/connectors\/([^/]+)\/test$/);
      if (testMatch && method === "POST") {
        return Promise.resolve(jsonResponse({ ok: true, identity: platform.identity || "rookery_bot" }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<ChatAppWizard platform={platform} />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("setup step renders numbered steps with linkified URLs, chips, and Next advances to credentials", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(NOT_CONNECTED);

  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByText(/Create a Slack app at/)).toBeInTheDocument();
  const link = screen.getByRole("link", { name: "https://api.slack.com/apps" });
  expect(link).toHaveAttribute("href", "https://api.slack.com/apps");
  expect(link).toHaveAttribute("target", "_blank");
  expect(link.getAttribute("rel")).toContain("noreferrer");

  // step chips
  expect(screen.getByText("Setup")).toBeInTheDocument();
  expect(screen.getByText("Credentials")).toBeInTheDocument();
  expect(screen.getByText("Test")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /next/i }));
  expect(await screen.findByLabelText("Bot token")).toBeInTheDocument();
});

test("credentials step: secret field is type=password; Back returns to setup", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(NOT_CONNECTED);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /next/i }));

  const input = screen.getByLabelText("Bot token") as HTMLInputElement;
  expect(input.type).toBe("password");

  await user.click(screen.getByRole("button", { name: /back/i }));
  expect(await screen.findByText(/Create a Slack app at/)).toBeInTheDocument();
});

test("save posts {platform, values}; 400 invalid_credentials shows inline error and stays on credentials", async () => {
  let capturedBody: { platform: string; values: Record<string, string> } | null = null;
  mockFetch({
    save: (body) => {
      capturedBody = body;
      return jsonResponse(
        { error: { code: "invalid_credentials", message: "Bot token looks wrong" } },
        400,
      );
    },
  });
  const user = userEvent.setup();
  wrap(NOT_CONNECTED);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /next/i }));
  await user.type(screen.getByLabelText("Bot token"), "xoxb-bad");
  await user.click(screen.getByRole("button", { name: /save/i }));

  expect(await screen.findByText("Bot token looks wrong")).toBeInTheDocument();
  expect(capturedBody).toEqual({ platform: "slack", values: { bot_token: "xoxb-bad" } });
  // stayed on the credentials step
  expect(screen.getByLabelText("Bot token")).toBeInTheDocument();
});

test("save success carries a warning to the test step; test auto-fires ok; Next advances to the link step (no premature Done)", async () => {
  mockFetch({
    save: () => jsonResponse({ ok: true, identity: "@bot", warning: "bot failed to start" }),
    test: () => jsonResponse({ ok: true, identity: "@bot" }),
  });
  const user = userEvent.setup();
  wrap(NOT_CONNECTED);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /next/i }));
  await user.type(screen.getByLabelText("Bot token"), "xoxb-good");
  await user.click(screen.getByRole("button", { name: /save/i }));

  expect(await screen.findByText(/bot failed to start/)).toBeInTheDocument();
  expect(await screen.findByText(/Connected as @bot/)).toBeInTheDocument();

  // A successful token test is not proof the integration works — Next (not
  // Done) is what's offered, and it lands on the link step, not a green
  // completion state.
  expect(screen.queryByRole("button", { name: /^done$/i })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /^next$/i }));
  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
  // The warning carries through to the link step too.
  expect(screen.getByText(/bot failed to start/)).toBeInTheDocument();
});

test("test step failure shows the error and Retry re-fires the test call; Next then lands on the link step", async () => {
  let calls = 0;
  mockFetch({
    save: () => jsonResponse({ ok: true, identity: "@bot" }),
    test: () => {
      calls += 1;
      return calls === 1
        ? jsonResponse({ ok: false, error: "connection refused" })
        : jsonResponse({ ok: true, identity: "@bot" });
    },
  });
  const user = userEvent.setup();
  wrap(NOT_CONNECTED);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /next/i }));
  await user.type(screen.getByLabelText("Bot token"), "xoxb-good");
  await user.click(screen.getByRole("button", { name: /save/i }));

  expect(await screen.findByText("connection refused")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /retry/i }));
  expect(await screen.findByText(/Connected as @bot/)).toBeInTheDocument();
  expect(calls).toBe(2);

  await user.click(screen.getByRole("button", { name: /^next$/i }));
  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
});

test("connected platform opens the Manage view: identity shown, Test connection ok/fail branches", async () => {
  let ok = true;
  mockFetch({
    test: () =>
      ok
        ? jsonResponse({ ok: true, identity: "@rookie_assistant_bot" })
        : jsonResponse({ ok: false, error: "token revoked" }),
  });
  const user = userEvent.setup();
  wrap(CONNECTED);

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText(/@rookie_assistant_bot/)).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /test connection/i }));
  expect(await screen.findByText(/Connected as @rookie_assistant_bot/)).toBeInTheDocument();

  ok = false;
  await user.click(screen.getByRole("button", { name: /test connection/i }));
  expect(await screen.findByText("token revoked")).toBeInTheDocument();
});

test("Manage: Disconnect asks for confirmation; confirming deletes and closes the panel", async () => {
  let deletedPlatform: string | null = null;
  mockFetch({
    del: (platform) => {
      deletedPlatform = platform;
      return jsonResponse({ ok: true });
    },
  });
  const user = userEvent.setup();
  wrap(CONNECTED);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /disconnect/i }));
  expect(screen.getByText(/reconnect to use it again/i)).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /^cancel$/i }));
  expect(screen.queryByText(/reconnect to use it again/i)).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /disconnect/i }));
  await user.click(screen.getByRole("button", { name: /yes, disconnect/i }));

  expect(deletedPlatform).toBe("telegram");
  expect(screen.queryByText(/@rookie_assistant_bot/)).not.toBeInTheDocument();
});

// ── Unlink: self-serviceable re-link, distinct from Disconnect ─────────────
//
// Unlink drops the operator's /start handshake but keeps the saved bot
// credentials — the router otherwise answers a re-link attempt with "contact
// your administrator", a dead end in a single-owner product.

test("Manage (linked): Unlink calls the identity-removal endpoint, leaving Disconnect untouched", async () => {
  let unlinkedPlatform: string | null = null;
  mockFetch({
    unlink: (platform) => {
      unlinkedPlatform = platform;
      return jsonResponse({ ok: true });
    },
  });
  const user = userEvent.setup();
  wrap(CONNECTED);

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText("@rookie_assistant_bot")).toBeInTheDocument();

  const unlinkButton = screen.getByRole("button", { name: /unlink this account/i });
  await user.click(unlinkButton);

  await waitFor(() => expect(unlinkedPlatform).toBe("telegram"));
  // Disconnect (which does remove credentials) is a separate control and
  // stays reachable.
  expect(screen.getByRole("button", { name: /disconnect/i })).toBeInTheDocument();
});

// `AppShell`'s slide-over is a `useState<{node: ReactNode}>`, so the element
// ConnectionsPage passes to `open()` is created once and never re-created —
// ManageWizard's `platform` prop is a frozen snapshot from the moment the
// panel opened. `useUnlinkConnector` invalidates the `["connectors"]` query
// and the page refetches, but a stale prop can't observe that on its own.
// This test's `list` handler tracks the DELETE and reports the platform as
// unlinked afterwards — the fake server, not the frozen prop — the way the
// real API does.
test("Manage (linked): after Unlink succeeds, the green header and Done button disappear", async () => {
  let linked = true;
  mockFetch({
    unlink: () => {
      linked = false;
      return jsonResponse({ ok: true });
    },
    list: () =>
      jsonResponse({
        platforms: [
          { ...CONNECTED, linked, linked_identity: linked ? CONNECTED.linked_identity : "" },
        ],
      }),
  });
  const user = userEvent.setup();
  wrap(CONNECTED);

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText(/linked as 123456789/i)).toBeInTheDocument();
  expect(screen.queryByText(/^connected$/i)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^done$/i })).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /unlink this account/i }));

  await waitFor(() =>
    expect(screen.queryByText(/linked as 123456789/i)).not.toBeInTheDocument(),
  );
  expect(screen.queryByText(/^connected$/i)).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^done$/i })).not.toBeInTheDocument();
  // Falls through to the link step, proving the panel re-read live state
  // rather than just hiding the old one.
  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
});

test("Manage (linked): a failed Unlink surfaces an inline error instead of silently no-opping", async () => {
  mockFetch({
    unlink: () => jsonResponse({ error: { code: "server_error", message: "unlink failed" } }, 500),
  });
  const user = userEvent.setup();
  wrap(CONNECTED);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /unlink this account/i }));

  expect(await screen.findByText("unlink failed")).toBeInTheDocument();
  // The green header is untouched — the unlink never actually took effect.
  expect(screen.getByText(/linked as 123456789/i)).toBeInTheDocument();
});

test("Manage (not yet linked): no Unlink button — there is no link to remove", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap({ ...CONNECTED, linked: false, linked_identity: "" });

  await user.click(screen.getByText("open wizard"));
  await screen.findByRole("button", { name: /disconnect/i });

  expect(
    screen.queryByRole("button", { name: /unlink this account/i }),
  ).not.toBeInTheDocument();
});

// ── Step 4: Link your account ───────────────────────────────────────────────

const LINK_STEP_PLATFORM: ConnectorPlatform = {
  platform: "discord",
  label: "Discord",
  blurb: "",
  setup_steps: ["Open the Discord Developer Portal and click New Application"],
  fields: [{ name: "token", label: "Bot Token", secret: true }],
  connected: true,
  identity: "rookery_bot",
  linked: false,
  linked_identity: "",
  primary: false,
  dm_url: "https://discord.com/users/42",
  invite_url:
    "https://discord.com/api/oauth2/authorize?client_id=42&scope=bot&permissions=0",
};

test("shows no Done button and no success state while unlinked", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, connected: false });

  await userEvent.click(screen.getByRole("button", { name: /next/i }));
  await userEvent.type(screen.getByLabelText(/bot token/i), "tok");
  await userEvent.click(screen.getByRole("button", { name: /save & continue/i }));
  // The token test succeeds — that's not proof of a real link, so it offers
  // Next (not Done) into the step that waits for the actual handshake.
  await userEvent.click(await screen.findByRole("button", { name: /^next$/i }));

  // Step 4 is reached but unlinked: the wizard must not offer completion.
  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^done$/i })).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: /invite/i })).toHaveAttribute(
    "href",
    LINK_STEP_PLATFORM.invite_url,
  );
});

test("offers Done once the identity row appears", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, linked: true, linked_identity: "ilija#4821" });

  expect(await screen.findByText(/ilija#4821/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^done$/i })).toBeInTheDocument();
  expect(screen.queryByText(/waiting for you to send/i)).not.toBeInTheDocument();
});

test("the escape hatch never reads as success", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, connected: false });

  await userEvent.click(screen.getByRole("button", { name: /next/i }));
  await userEvent.type(screen.getByLabelText(/bot token/i), "tok");
  await userEvent.click(screen.getByRole("button", { name: /save & continue/i }));
  await userEvent.click(await screen.findByRole("button", { name: /^next$/i }));

  const escape = await screen.findByRole("button", { name: /finish later/i });
  expect(escape).toHaveTextContent(/not linked/i);
});

// ── connected-but-unlinked routing: Manage must not skip the link gate ─────
//
// A platform can be `connected` (the saved token authenticates) without
// being `linked` (the operator never sent /start, or unlinked). That state
// must open the Manage entry point — Disconnect has to stay reachable — but
// must show the link step in place of the green "Connected" header, never
// both a green header AND an unlinked state.

test("a connected-but-unlinked platform opens on the link step, not the green Connected header, and keeps Disconnect reachable", async () => {
  renderWizard(LINK_STEP_PLATFORM); // connected: true, linked: false

  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^done$/i })).not.toBeInTheDocument();
  expect(screen.queryByText(/^connected$/i)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /disconnect/i })).toBeInTheDocument();
});

test("a connected and linked platform reaches the normal Manage panel with its identity header", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, linked: true, linked_identity: "ilija#4821" });

  expect(await screen.findByText("rookery_bot")).toBeInTheDocument();
  expect(screen.getByText(/ilija#4821/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /test connection/i })).toBeInTheDocument();
  expect(screen.queryByText(/waiting for you to send/i)).not.toBeInTheDocument();
});

// ── Done actually closes the panel ──────────────────────────────────────────
//
// renderWizard mounts ChatAppWizard directly, so close() is a no-op there —
// these assert through wrap()/Opener, which opens the real slide-over.

test("Manage: Done closes the panel", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(CONNECTED); // linked: true

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText("@rookie_assistant_bot")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /^done$/i }));
  expect(screen.queryByText("@rookie_assistant_bot")).not.toBeInTheDocument();
});

test("Manage's link step: Done closes the panel once the identity row appears", async () => {
  const unlinked: ConnectorPlatform = { ...CONNECTED, linked: false, linked_identity: "" };
  mockFetch({
    list: () =>
      jsonResponse({
        platforms: [{ ...unlinked, linked: true, linked_identity: "@ilija" }],
      }),
  });
  const user = userEvent.setup();
  wrap(unlinked);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /^done$/i }));
  expect(screen.queryByText(/@ilija/)).not.toBeInTheDocument();
});
