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
  actions: ["configure_coder", "delete_workspace"],
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
      if (url.startsWith("/api/v1/admin/audit") && method === "GET") return Promise.resolve(jsonResponse(AUDIT_LOGS));
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
// An action name now appears both as an <option> in the filter picker and as
// a cell in the table, so audit-row assertions have to be scoped to the table.
async function findAuditRow(text: string) {
  const table = await screen.findByRole("table");
  return within(table).findByText(text);
}

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

test("System status renders the sandbox/landlock indicators", async () => {
  mockFetch();
  wrap();
  expect(await screen.findByText("on")).toBeInTheDocument();
  expect(screen.getByText("ready")).toBeInTheDocument();
});

test("System status offers no editable settings", async () => {
  // Regression guard for the removal: claude_bin / coder_timeout /
  // agent_timeout / memory_mb were written to system_settings and never read
  // back by anything, so the form was removed rather than wired up. If an
  // input reappears here it is once again configuring nothing.
  mockFetch();
  wrap();
  await screen.findByText("on");
  expect(screen.queryByLabelText(/claude binary path/i)).toBeNull();
  expect(screen.queryByLabelText(/coder timeout/i)).toBeNull();
  expect(screen.queryByLabelText(/agent timeout/i)).toBeNull();
  expect(screen.queryByLabelText(/memory limit/i)).toBeNull();
  // NOTE: a bare "Save" button is no longer a valid proxy for "nothing here is
  // editable" — the Instance URL section owns one, and that value IS read back
  // (publicurl.Resolve consults it on every consent URL and every preflight).
  // The guard that matters is the absence of the inert fields above.
  expect(screen.queryByLabelText(/instance url/i)).toBeInTheDocument();
});

test("Audit log renders rows from the last-100 GET", async () => {
  mockFetch();
  wrap();
  expect(await findAuditRow("configure_coder")).toBeInTheDocument();
  const table = screen.getByRole("table");
  expect(within(table).getByText("delete_workspace")).toBeInTheDocument();
  expect(within(table).getByText("workspace:w1")).toBeInTheDocument();
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

  await findAuditRow("configure_coder");
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

test("Workspace cards offer no permissions editor", async () => {
  // Regression guard: workspace_permissions had exactly one reader
  // (rbac.CanPerform) and that function had no callers at all, so the
  // checkboxes gated nothing. The whole surface was removed.
  mockFetch();
  wrap();
  await screen.findAllByText("Home Server");
  expect(screen.queryByRole("button", { name: /permissions/i })).toBeNull();
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

  // "Side Project" now also appears as an <option> in the audit log's
  // workspace filter, so scope the wait to the workspace cards.
  await waitFor(() => expect(workspaceCardNames()).toContain("Side Project"));
  const user = userEvent.setup();
  const deleteButtons = screen.getAllByRole("button", { name: /^delete$/i });
  await user.click(deleteButtons[1]);

  expect(screen.getByText(/delete “side project”\?/i)).toBeInTheDocument();
  expect(screen.queryByText(/active workspace/i)).not.toBeInTheDocument();
});

test("Audit log filters are sent to the server, not applied to the returned page", async () => {
  // Narrowing an already-truncated page of the most recent 100 events would
  // report "no matches" for something that merely happened 101 events ago, so
  // every filter has to reach the query.
  const calls = mockFetch();
  wrap();
  await findAuditRow("configure_coder");

  const user = userEvent.setup();
  await user.selectOptions(screen.getByLabelText(/filter by action/i), "configure_coder");
  await waitFor(() => {
    expect(calls.some((c) => c.url.includes("action=configure_coder"))).toBe(true);
  });

  await user.selectOptions(screen.getByLabelText(/filter by time/i), "7");
  await waitFor(() => {
    expect(calls.some((c) => c.url.includes("since_days=7"))).toBe(true);
  });

  await user.selectOptions(screen.getByLabelText(/filter by workspace/i), "w1");
  await waitFor(() => {
    expect(calls.some((c) => c.url.includes("workspace_id=w1"))).toBe(true);
  });
});

test("Audit log search is debounced into a single server query", async () => {
  const calls = mockFetch();
  wrap();
  await findAuditRow("configure_coder");

  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/search audit log/i), "invoice");

  await waitFor(() => {
    expect(calls.some((c) => c.url.includes("q=invoice"))).toBe(true);
  });
  // Seven keystrokes must not mean seven requests.
  const searchCalls = calls.filter((c) => c.url.includes("q="));
  expect(searchCalls.length).toBeLessThan(4);
});
