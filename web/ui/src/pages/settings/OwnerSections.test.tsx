import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import {
  AuditLogSection, InstanceURLSection, SystemStatusSection, WorkspacesSection,
} from "./OwnerSections";
import { BackupSection } from "./BackupSection";
import { OwnerGate } from "./OwnerGate";

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

// OwnerSections mounts BackupSection, so its endpoints must be mocked here too.
const BACKUP_CONFIG = {
  enabled: false,
  destination: "local",
  schedule: "daily",
  hour: 3,
  weekday: 0,
  retention: 7,
  passphrase_set: false,
  local_dir: "",
  s3: { endpoint: "", region: "", bucket: "", prefix: "", access_key: "", secret_key_set: false, path_style: false },
  last_run_at: "0001-01-01T00:00:00Z",
  last_status: "",
  last_error: "",
  last_size: 0,
  next_run_at: "0001-01-01T00:00:00Z",
  pending_restore: false,
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
      if (url === "/api/v1/backup/config") return Promise.resolve(jsonResponse(BACKUP_CONFIG));
      if (url.startsWith("/api/v1/backup/snapshots")) return Promise.resolve(jsonResponse([]));
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
        {/* The OwnerSections wrapper is gone — each section is now its own
            settings page. This renders all five together so the existing
            assertions keep exercising the same components. */}
        <WorkspacesSection />
        <InstanceURLSection />
        <SystemStatusSection />
        <BackupSection />
        <AuditLogSection />
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
  // Landlock reports its ABI alongside readiness — the page used to show two
  // bare booleans while /healthz already knew the version, commit, ABI, coder
  // mode and host-tool presence, and showed none of it to the operator.
  expect(screen.getByText(/^ready/)).toBeInTheDocument();
});

test("System status reports the host tools and build identity", async () => {
  mockFetch();
  wrap();
  await screen.findByText("on");
  // Labels, not values: the fixture may report a tool either way, and what
  // this pins is that the page ASKS about each one at all.
  for (const label of ["Version", "Commit", "Coder mode", "Python 3", "ripgrep", "pdftotext", "tesseract"]) {
    expect(screen.getByText(label)).toBeInTheDocument();
  }
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
  // Scoped to the System status section: this guard was page-wide only because
  // that section used to be the only one with a Save button. Backup and the
  // Instance URL both now have legitimate ones — each backed by a value that is
  // actually read back — and a page-wide assertion would forbid them.
  const systemStatus = screen.getByText("System status").parentElement!;
  expect(within(systemStatus).queryByRole("button", { name: /^save$/i })).toBeNull();
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

// ── OwnerGate ────────────────────────────────────────────────────────────

function wrapGate() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <OwnerGate>
          <div>owner body</div>
        </OwnerGate>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const GATE_403 = () =>
  jsonResponse(
    { error: { code: "owner_verification_required", message: "confirm your owner password" } },
    403,
  );

test("OwnerGate prompts for the password when an install-level route 403s", async () => {
  mockFetch({ "GET /api/v1/admin/overview": GATE_403 });
  wrapGate();

  expect(await screen.findByLabelText(/owner password/i)).toBeInTheDocument();
  expect(screen.queryByText("owner body")).not.toBeInTheDocument();
});

test("OwnerGate renders the body after a successful verify", async () => {
  let verified = false;
  mockFetch({
    "GET /api/v1/admin/overview": () =>
      verified ? jsonResponse({ workspaces: 1 }) : GATE_403(),
    "POST /api/v1/auth/owner-verify": () => {
      verified = true;
      return jsonResponse({ ok: true, verified_until: "2099-01-01T00:00:00Z" });
    },
  });
  wrapGate();

  await userEvent.type(await screen.findByLabelText(/owner password/i), "hunter2");
  await userEvent.click(screen.getByRole("button", { name: /unlock/i }));

  expect(await screen.findByText("owner body")).toBeInTheDocument();
});

test("OwnerGate keeps the prompt and shows the error on a wrong password", async () => {
  mockFetch({
    "GET /api/v1/admin/overview": GATE_403,
    "POST /api/v1/auth/owner-verify": () =>
      jsonResponse({ error: { code: "invalid_password", message: "wrong owner password" } }, 401),
  });
  wrapGate();

  await userEvent.type(await screen.findByLabelText(/owner password/i), "nope");
  await userEvent.click(screen.getByRole("button", { name: /unlock/i }));

  expect(await screen.findByText(/wrong owner password/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/owner password/i)).toBeInTheDocument();
});

// An unrelated 403 is a real permission error, not a verification gate — showing
// a password prompt for it would be a dead end.
test("OwnerGate does not prompt for an unrelated 403", async () => {
  mockFetch({
    "GET /api/v1/admin/overview": () =>
      jsonResponse({ error: { code: "forbidden", message: "nope" } }, 403),
  });
  wrapGate();

  await waitFor(() => expect(screen.getByText("owner body")).toBeInTheDocument());
  expect(screen.queryByLabelText(/owner password/i)).not.toBeInTheDocument();
});

// The gate is transparent when the probe succeeds, so an already-verified
// session sees no prompt at all.
test("OwnerGate is transparent when already verified", async () => {
  mockFetch({ "GET /api/v1/admin/overview": () => jsonResponse({ workspaces: 1 }) });
  wrapGate();

  expect(await screen.findByText("owner body")).toBeInTheDocument();
  expect(screen.queryByLabelText(/owner password/i)).not.toBeInTheDocument();
});

// This page listed workspaces as names and buttons while every other surface
// showed their chosen artwork. The fixture's workspaces set no icon, which is
// the unset case WorkspaceAvatar renders as the Rookery mark.
test("each workspace card shows its image, not just its name", async () => {
  mockFetch();
  wrap();

  await waitFor(() => expect(workspaceCardNames().length).toBeGreaterThan(0));

  const nameEl = document.querySelector(".truncate.font-semibold");
  const card = nameEl?.closest("div.rounded-lg");
  // Assert on WorkspaceAvatar's own gradient id, not on "some svg": the card's
  // Enter and Delete buttons carry lucide icons, so a bare querySelector("svg")
  // passes with no avatar present at all.
  expect(card?.querySelector('linearGradient[id^="wsicon-"]')).toBeTruthy();
});
