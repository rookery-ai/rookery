import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell, useSlideOver } from "@/components/shell/AppShell";
import { ServiceWizard } from "./ServiceWizard";
import type { ServiceProvider } from "@/lib/connections";

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

const OAUTH_NO_CREDS: ServiceProvider = {
  name: "notion",
  label: "Notion",
  category: "Productivity",
  kind: "oauth",
  setup_url: "https://www.notion.so/my-integrations",
  setup_steps: [
    "Create an integration at https://www.notion.so/my-integrations",
    "Copy the client id and secret below",
  ],
  has_creds: false,
  action_count: 0,
  connect_inputs: [], redirect_uri: "", preflight: [],
  connections: [],
};

const OAUTH_WITH_CREDS: ServiceProvider = {
  name: "github",
  label: "GitHub",
  category: "Productivity",
  kind: "oauth",
  setup_url: "",
  setup_steps: [],
  has_creds: true,
  action_count: 3,
  connect_inputs: [], redirect_uri: "", preflight: [],
  connections: [],
};

const OAUTH_NEEDS_REAUTH: ServiceProvider = {
  name: "jira",
  label: "Jira",
  category: "Productivity",
  kind: "oauth",
  setup_url: "",
  setup_steps: [],
  has_creds: true,
  action_count: 0,
  connect_inputs: [], redirect_uri: "", preflight: [],
  connections: [{ id: "c2", label: "team", identity: "me@co.com", status: "NEEDS_REAUTH" }],
};

const OAUTH_ACTIVE_CONN: ServiceProvider = {
  name: "gmail",
  label: "Gmail",
  category: "Productivity",
  kind: "oauth",
  setup_url: "",
  setup_steps: [],
  has_creds: true,
  action_count: 0,
  connect_inputs: [], redirect_uri: "", preflight: [],
  connections: [{ id: "c1", label: "work", identity: "me@gmail.com", status: "ACTIVE" }],
};

const API_KEY_PROVIDER: ServiceProvider = {
  name: "openai",
  label: "OpenAI",
  category: "Productivity",
  kind: "api_key",
  setup_url: "",
  setup_steps: [],
  has_creds: false,
  action_count: 0, redirect_uri: "", preflight: [],
  connect_inputs: [
    { key: "org_id", label: "Organization ID", hint: "Found in your OpenAI settings", required: true },
  ],
  connections: [],
};

type Handlers = {
  creds?: (provider: string, body: { client_id: string; client_secret: string }) => Response;
  connect?: (provider: string, body: { label: string }) => Response;
  apikey?: (
    provider: string,
    body: { key: string; label: string; inputs: Record<string, string> },
  ) => Response;
  del?: (id: string) => Response;
  providersOverride?: () => ServiceProvider[];
  actions?: (provider: string) => Response;
};

function mockFetch(handlers: Handlers = {}) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/services" && method === "GET") {
        const providers = handlers.providersOverride
          ? handlers.providersOverride()
          : [OAUTH_NO_CREDS, OAUTH_WITH_CREDS, OAUTH_NEEDS_REAUTH, OAUTH_ACTIVE_CONN, API_KEY_PROVIDER];
        return Promise.resolve(jsonResponse({ providers }));
      }

      const credsMatch = url.match(/^\/api\/v1\/services\/([^/]+)\/creds$/);
      if (credsMatch && method === "POST") {
        const body = init?.body ? JSON.parse(String(init.body)) : { client_id: "", client_secret: "" };
        return Promise.resolve(
          handlers.creds ? handlers.creds(credsMatch[1], body) : jsonResponse({ ok: true }),
        );
      }

      const connectMatch = url.match(/^\/api\/v1\/services\/([^/]+)\/connect$/);
      if (connectMatch && method === "POST") {
        const body = init?.body ? JSON.parse(String(init.body)) : { label: "" };
        return Promise.resolve(
          handlers.connect
            ? handlers.connect(connectMatch[1], body)
            : jsonResponse({ redirect_url: "https://provider.example/oauth/authorize" }),
        );
      }

      const apikeyMatch = url.match(/^\/api\/v1\/services\/([^/]+)\/apikey$/);
      if (apikeyMatch && method === "POST") {
        const body = init?.body
          ? JSON.parse(String(init.body))
          : { key: "", label: "", inputs: {} };
        return Promise.resolve(
          handlers.apikey ? handlers.apikey(apikeyMatch[1], body) : jsonResponse({ ok: true }),
        );
      }

      const actionsMatch = url.match(/^\/api\/v1\/services\/([^/]+)\/actions$/);
      if (actionsMatch && method === "GET") {
        return Promise.resolve(
          handlers.actions
            ? handlers.actions(actionsMatch[1])
            : jsonResponse({
                actions: [
                  {
                    name: "github_search_issues",
                    description: "Search issues across your repos",
                    mutating: false,
                    public_write: false,
                    params: { properties: { query: { type: "string" } }, required: ["query"] },
                  },
                ],
              }),
        );
      }

      const delMatch = url.match(/^\/api\/v1\/services\/([^/]+)$/);
      if (delMatch && method === "DELETE") {
        return Promise.resolve(handlers.del ? handlers.del(delMatch[1]) : jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function Opener({ provider }: { provider: ServiceProvider }) {
  const { open } = useSlideOver();
  return (
    <button
      onClick={() =>
        open(<ServiceWizard provider={provider} />, {
          title: `${provider.connections.length > 0 ? "Manage" : "Connect"} ${provider.label}`,
        })
      }
    >
      open wizard
    </button>
  );
}

function wrap(provider: ServiceProvider) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Opener provider={provider} />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("oauth provider with no saved creds shows the creds form and posts {client_id, client_secret}", async () => {
  let captured: { client_id: string; client_secret: string } | null = null;
  mockFetch({
    creds: (_provider, body) => {
      captured = body;
      return jsonResponse({ ok: true });
    },
  });
  const user = userEvent.setup();
  wrap(OAUTH_NO_CREDS);

  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByText("Client ID")).toBeInTheDocument();
  // The prominent setup_url link AND the linkified setup_steps copy both
  // reference the same URL — both must resolve to a real https:// href.
  const links = screen.getAllByRole("link", { name: "https://www.notion.so/my-integrations" });
  expect(links.length).toBeGreaterThanOrEqual(2);
  for (const link of links) {
    expect(link).toHaveAttribute("href", "https://www.notion.so/my-integrations");
  }

  await user.type(screen.getByLabelText("Client ID"), "cid-123");
  await user.type(screen.getByLabelText("Client secret"), "csecret-456");
  await user.click(screen.getByRole("button", { name: /save & continue/i }));

  expect(captured).toEqual({ client_id: "cid-123", client_secret: "csecret-456" });
  // advances to the connect step
  expect(await screen.findByRole("button", { name: /connect notion/i })).toBeInTheDocument();
});

test("oauth provider with saved creds: connect posts {label} and navigates to redirect_url", async () => {
  let captured: { label: string } | null = null;
  mockFetch({
    connect: (_provider, body) => {
      captured = body;
      return jsonResponse({ redirect_url: "https://github.com/login/oauth/authorize?client_id=x" });
    },
  });
  // jsdom's window.location.assign isn't configurable directly (spyOn throws
  // "Cannot redefine property"); stub the whole `location` getter instead,
  // the same workaround WorkspaceMenu.test.tsx uses for `location.href`.
  const assignSpy = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ assign: assignSpy } as unknown as Location);
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS);

  await user.click(screen.getByText("open wizard"));
  await user.type(await screen.findByLabelText(/label/i), "work");
  await user.click(screen.getByRole("button", { name: /connect github/i }));

  expect(captured).toEqual({ label: "work", inputs: {} });
  expect(assignSpy).toHaveBeenCalledWith("https://github.com/login/oauth/authorize?client_id=x");
});

test("api_key provider posts {key, inputs} and closes the panel on success", async () => {
  let captured: { key: string; label: string; inputs: Record<string, string> } | null = null;
  mockFetch({
    apikey: (_provider, body) => {
      captured = body;
      return jsonResponse({ ok: true });
    },
  });
  const user = userEvent.setup();
  wrap(API_KEY_PROVIDER);

  await user.click(screen.getByText("open wizard"));
  await user.type(await screen.findByLabelText(/openai api key/i), "sk-test-key");
  await user.type(screen.getByLabelText(/organization id/i), "org-1");
  await user.click(screen.getByRole("button", { name: /^connect$/i }));

  expect(captured).toEqual({ key: "sk-test-key", label: "", inputs: { org_id: "org-1" } });
  await screen.findByText("open wizard"); // panel closed, opener button visible again
  expect(screen.queryByLabelText(/openai api key/i)).not.toBeInTheDocument();
});

test("a needs-reconnect account shows a Reconnect button that jumps to the connect flow", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_NEEDS_REAUTH);

  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByText("needs reconnect")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /reconnect/i }));

  expect(await screen.findByRole("button", { name: /connect jira/i })).toBeInTheDocument();
  expect(screen.getByLabelText(/label/i)).toHaveValue("team");
});

test("disconnect asks for confirmation, then DELETEs the connection", async () => {
  let deletedID: string | null = null;
  mockFetch({
    del: (id) => {
      deletedID = id;
      return jsonResponse({ ok: true });
    },
  });
  const user = userEvent.setup();
  wrap(OAUTH_ACTIVE_CONN);

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText("me@gmail.com")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /^disconnect$/i }));
  expect(screen.getByText(/disconnect work\?/i)).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /yes, disconnect/i }));

  expect(deletedID).toBe("c1");
});

test("shows an actions entry button carrying the provider's action count", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS);
  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByRole("button", { name: /What can it do/ })).toBeInTheDocument();
  expect(screen.getByText(/3 actions/)).toBeInTheDocument();
});

test("a provider with no actions shows no entry button", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_NO_CREDS); // action_count: 0
  await user.click(screen.getByText("open wizard"));

  await screen.findByLabelText("Client ID");
  expect(screen.queryByRole("button", { name: /What can it do/ })).not.toBeInTheDocument();
});

test("opening the actions view replaces the connect body, and Back restores it", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS); // has_creds: true → opens on the connect view
  await user.click(screen.getByText("open wizard"));

  await screen.findByLabelText("Label (optional)");
  await user.click(await screen.findByRole("button", { name: /What can it do/ }));

  expect(await screen.findByText("Search issues across your repos")).toBeInTheDocument();
  expect(screen.queryByLabelText("Label (optional)")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /Back/ }));
  expect(await screen.findByLabelText("Label (optional)")).toBeInTheDocument();
});

// The regression that ruled out opening a second slide-over panel: the shell's
// slide-over is a single slot, so a real second panel would unmount the wizard
// and silently discard whatever the user had typed.
test("half-typed OAuth credentials survive a trip through the actions view", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(OAUTH_WITH_CREDS);
  await user.click(screen.getByText("open wizard"));

  // Jump to the creds view, where the sensitive fields live.
  await user.click(await screen.findByText("edit app credentials"));
  await user.type(await screen.findByLabelText("Client ID"), "typed-client-id");

  await user.click(screen.getByRole("button", { name: /What can it do/ }));
  await screen.findByText("Search issues across your repos");
  await user.click(screen.getByRole("button", { name: /Back/ }));

  expect(await screen.findByLabelText("Client ID")).toHaveValue("typed-client-id");
});

// ServiceWizard prefers the provider from the ["services"] query over the prop
// snapshot, so these fixtures must be served by the mocked fetch — passing them
// only to wrap() would be silently discarded on the first refetch.
function wrapLive(provider: ServiceProvider) {
  mockFetch({ providersOverride: () => [provider] });
  return wrap(provider);
}

test("shows the redirect URI so the user can register it", async () => {
  const user = userEvent.setup();
  const uri = "https://agents.example.com/dashboard/connectors/services/callback/google";
  wrapLive({ ...OAUTH_NO_CREDS, redirect_uri: uri });

  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByText("Redirect URI to register")).toBeInTheDocument();
  expect(screen.getAllByText(uri).length).toBeGreaterThan(0);
});

test("substitutes the real callback URL into the setup steps, as copyable text not a link", async () => {
  const user = userEvent.setup();
  const uri = "https://agents.example.com/dashboard/connectors/services/callback/google";
  wrapLive({
    ...OAUTH_NO_CREDS,
    redirect_uri: uri,
    setup_steps: ["Under Authorized redirect URIs, add {{redirect_uri}} exactly, then click Create."],
  });

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText(/then click Create/)).toBeInTheDocument();

  // The placeholder must never reach the user.
  expect(screen.queryByText(/\{\{redirect_uri\}\}/)).not.toBeInTheDocument();

  // The URI is NOT an anchor: following it would hit our own callback route with
  // no state parameter, which only ever renders an error.
  for (const node of screen.getAllByText(uri)) {
    expect(node.closest("a")).toBeNull();
  }
});

test("disables Connect and explains when preflight finds a hard problem", async () => {
  const user = userEvent.setup();
  wrapLive({
    ...OAUTH_WITH_CREDS,
    redirect_uri: "http://192.168.1.194:8080/dashboard/connectors/services/callback/github",
    preflight: [
      {
        severity: "hard",
        code: "raw_ip",
        message: "This provider does not accept an IP address as the redirect host.",
        fix: "Use a hostname instead.",
      },
    ],
  });

  await user.click(screen.getByText("open wizard"));

  // The reason must be visible next to the button it disables.
  expect(await screen.findByText(/does not accept an IP address/)).toBeInTheDocument();
  expect(screen.getByText("Use a hostname instead.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /connect github/i })).toBeDisabled();
});

test("warns but still allows Connect on a soft problem", async () => {
  const user = userEvent.setup();
  wrapLive({
    ...OAUTH_WITH_CREDS,
    preflight: [
      { severity: "soft", code: "unverified_host", message: "Unconfirmed suffix.", fix: "Try it." },
    ],
  });

  await user.click(screen.getByText("open wizard"));
  expect(await screen.findByText("Unconfirmed suffix.")).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: /connect github/i })).toBeEnabled();
});
