import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { OwnerSections } from "./OwnerSections";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "Home Server", about: "Personal assistant", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [
    { id: "w1", name: "Home Server", about: "Personal assistant", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
    { id: "w2", name: "Side Project", about: "", needs_setup: false, created_at: "2026-01-02T00:00:00Z" },
  ],
};

const ADMIN_SETTINGS = {
  claude_bin: "/usr/bin/claude",
  coder_timeout: "120",
  agent_timeout: "300",
  memory_mb: "512",
  sandbox_on: true,
  landlock_ready: true,
};

const AUDIT_LOGS = {
  logs: [
    {
      workspace_id: "w1",
      action: "configure_coder",
      target: "workspace:w1",
      detail: "api:openrouter/glm-5.2",
      ip: "127.0.0.1",
      created_at: "2026-07-16T12:00:00Z",
    },
    {
      workspace_id: "",
      action: "delete_workspace",
      target: "workspace:w9",
      detail: "",
      ip: "127.0.0.1",
      created_at: "2026-07-15T09:00:00Z",
    },
  ],
};

const PERMISSIONS = {
  permissions: [
    { name: "bash", granted: true },
    { name: "web-browser", granted: false },
    { name: "system-tools", granted: false },
    { name: "mcp-servers", granted: false },
  ],
};

type Overrides = Record<string, (body: unknown) => Response | Promise<Response>>;

function mockFetch(overrides: Overrides = {}) {
  const calls: { url: string; method: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      const key = `${method} ${url}`;
      if (overrides[key]) return Promise.resolve(overrides[key](body));

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/admin/settings" && method === "GET") return Promise.resolve(jsonResponse(ADMIN_SETTINGS));
      if (url === "/api/v1/admin/audit?limit=100" && method === "GET") return Promise.resolve(jsonResponse(AUDIT_LOGS));
      if (/^\/api\/v1\/workspaces\/.+\/permissions$/.test(url) && method === "GET") {
        return Promise.resolve(jsonResponse(PERMISSIONS));
      }
      if (/^\/api\/v1\/workspaces\/.+\/permissions$/.test(url) && method === "PUT") {
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (/^\/api\/v1\/workspaces\/.+$/.test(url) && method === "DELETE") {
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <OwnerSections />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// Scoped to the workspace-card name element specifically (not text-only
// getByText) — the audit log's Workspace column now also resolves w1 to
// "Home Server", so a plain getByText would match twice.
function workspaceCardNames() {
  return Array.from(document.querySelectorAll(".truncate.font-semibold")).map((el) => el.textContent);
}

test("renders workspace cards from the session", async () => {
  mockFetch();
  wrap();
  await waitFor(() => expect(workspaceCardNames().length).toBeGreaterThan(0));
  const names = workspaceCardNames();
  expect(names.some((t) => t?.includes("Home Server"))).toBe(true);
  expect(names.some((t) => t?.includes("Side Project"))).toBe(true);
});

test("System settings form prefills from GET and renders sandbox/landlock indicators", async () => {
  mockFetch();
  wrap();
  const bin = (await screen.findByLabelText(/claude binary path/i)) as HTMLInputElement;
  await waitFor(() => expect(bin.value).toBe("/usr/bin/claude"));
  expect(screen.getByText("on")).toBeInTheDocument();
  expect(screen.getByText("ready")).toBeInTheDocument();
});

test("System settings save PUTs claude_bin/coder_timeout/agent_timeout/memory_mb", async () => {
  const calls = mockFetch();
  wrap();

  const bin = (await screen.findByLabelText(/claude binary path/i)) as HTMLInputElement;
  await waitFor(() => expect(bin.value).toBe("/usr/bin/claude"));

  const user = userEvent.setup();
  await user.clear(bin);
  await user.type(bin, "/opt/claude/bin/claude");
  await user.click(screen.getByRole("button", { name: /^save$/i }));

  await waitFor(() => {
    const put = calls.find((c) => c.url === "/api/v1/admin/settings" && c.method === "PUT");
    expect(put).toBeDefined();
  });
  const put = calls.find((c) => c.url === "/api/v1/admin/settings" && c.method === "PUT")!;
  expect(put.body).toEqual({
    claude_bin: "/opt/claude/bin/claude",
    coder_timeout: "120",
    agent_timeout: "300",
    memory_mb: "512",
  });
});

test("Audit log renders rows from the last-100 GET", async () => {
  mockFetch();
  wrap();
  expect(await screen.findByText("configure_coder")).toBeInTheDocument();
  expect(screen.getByText("delete_workspace")).toBeInTheDocument();
  expect(screen.getByText("workspace:w1")).toBeInTheDocument();
});

test("Audit log Workspace column resolves a known workspace_id to its name via the session, and falls back to a short uuid for a deleted workspace", async () => {
  mockFetch({
    "GET /api/v1/admin/audit?limit=100": () =>
      jsonResponse({
        logs: [
          { workspace_id: "w1", action: "configure_coder", target: "workspace:w1", detail: "", ip: "127.0.0.1", created_at: "2026-07-16T12:00:00Z" },
          { workspace_id: "deadbeef-0000-0000-0000-000000000000", action: "delete_workspace", target: "workspace:x", detail: "", ip: "127.0.0.1", created_at: "2026-07-15T09:00:00Z" },
          { workspace_id: "", action: "system_event", target: "system", detail: "", ip: "127.0.0.1", created_at: "2026-07-14T09:00:00Z" },
        ],
      }),
  });
  wrap();

  await screen.findByText("configure_coder");
  const table = screen.getByRole("table");

  // Known workspace_id (present in session.workspaces) resolves to its name
  // in the Workspace column (scoped to the table — "Home Server" also
  // appears in the Workspaces card above).
  expect(within(table).getByText("Home Server")).toBeInTheDocument();
  // Unknown/deleted workspace_id falls back to a short uuid.
  expect(within(table).getByText("deadbeef")).toBeInTheDocument();
  // Empty workspace_id (owner-level event) still shows the placeholder.
  expect(within(table).getByText("—")).toBeInTheDocument();
});

test("Workspace permissions: expanding loads checkboxes and Save PUTs grant/revoke", async () => {
  const calls = mockFetch();
  wrap();

  await screen.findAllByText("Home Server");
  const user = userEvent.setup();
  const permButtons = screen.getAllByRole("button", { name: /permissions/i });
  await user.click(permButtons[0]);

  await screen.findByText("web-browser");
  const bashBox = screen.getByRole("checkbox", { name: "bash" }) as HTMLInputElement;
  expect(bashBox.checked).toBe(true);
  const webBox = screen.getByRole("checkbox", { name: "web-browser" }) as HTMLInputElement;
  await user.click(webBox);
  await user.click(bashBox);

  await user.click(screen.getByRole("button", { name: /save permissions/i }));

  await waitFor(() => {
    const put = calls.find((c) => c.url === "/api/v1/workspaces/w1/permissions" && c.method === "PUT");
    expect(put).toBeDefined();
  });
  const put = calls.find((c) => c.url === "/api/v1/workspaces/w1/permissions" && c.method === "PUT")!;
  const body = put.body as { grant: string[]; revoke: string[] };
  expect(body.grant.sort()).toEqual(["web-browser"]);
  expect(body.revoke.sort()).toEqual(["bash", "mcp-servers", "system-tools"]);
});

test("Delete workspace: confirm flow warns extra for the ACTIVE workspace and calls DELETE", async () => {
  const calls = mockFetch();
  wrap();

  await screen.findAllByText("Home Server");
  const user = userEvent.setup();
  const deleteButtons = screen.getAllByRole("button", { name: /^delete$/i });
  // First card is Home Server (the active workspace, w1).
  await user.click(deleteButtons[0]);

  expect(screen.getByText(/active workspace/i)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /yes, delete/i }));

  await waitFor(() => {
    const del = calls.find((c) => c.url === "/api/v1/workspaces/w1" && c.method === "DELETE");
    expect(del).toBeDefined();
  });
});

test("Delete workspace: non-active workspace has no extra warning", async () => {
  mockFetch();
  wrap();

  await screen.findByText("Side Project");
  const user = userEvent.setup();
  const deleteButtons = screen.getAllByRole("button", { name: /^delete$/i });
  await user.click(deleteButtons[1]);

  expect(screen.getByText(/delete “side project”\?/i)).toBeInTheDocument();
  expect(screen.queryByText(/active workspace/i)).not.toBeInTheDocument();
});
