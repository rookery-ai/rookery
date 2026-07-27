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
  connect_inputs: [],
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
  connect_inputs: [],
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
  connect_inputs: [],
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
  connect_inputs: [],
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

  expect(captured).toEqual({ label: "work" });
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
