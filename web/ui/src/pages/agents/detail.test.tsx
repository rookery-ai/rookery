import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import AgentDetailPage from "./AgentDetailPage";
import type { AgentDetail } from "@/lib/agents";

// Minimal EventSource stub — mirrors designer.test.tsx / sse.test.ts. We
// never assert on connection internals directly, just drive
// onopen/onmessage/dispatchNamedEvent("done") from the test.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  url: string;
  readyState = FakeEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners: Record<string, Array<() => void>> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  dispatchNamedEvent(type: string) {
    this.listeners[type]?.forEach((l) => l());
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

function baseDetail(overrides: Partial<AgentDetail> = {}): AgentDetail {
  return {
    agent: {
      id: "a1",
      name: "Inbox Triager",
      description: "Reads new mail every morning.",
      active: true,
      created_at: "2026-07-01T00:00:00Z",
      running: false,
    },
    schedule: { cron_expr: "0 8 * * *", next_run_at: "2026-07-18T08:00:00Z", last_run_at: null, enabled: true },
    runs: [
      {
        id: "r1",
        trigger: "manual",
        status: "success",
        exit_code: 0,
        stdout: "Filed 3 emails.",
        stderr: "",
        prompt_tokens: null,
        completion_tokens: null,
        total_tokens: null,
        started_at: "2026-07-16T08:00:00Z",
        finished_at: "2026-07-16T08:00:12Z",
      },
    ],
    agent_md: "# Agent\n\nDo the thing.",
    state: "{}",
    logs: [],
    last_log: "",
    attached_skills: [],
    core_skills: [],
    all_skills: [],
    workspace_connections: [],
    attached_connection_ids: [],
    missing_secrets: [],
    running: false,
    live_run: false,
    ...overrides,
  };
}

let detail: AgentDetail;
let runCalled = false;
let deleteAgentCalled = false;

function mockFetch(handlers: Record<string, (body: unknown) => Response | Promise<Response>> = {}) {
  const calls: Array<{ url: string; method: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/agents/a1" && method === "GET") return Promise.resolve(jsonResponse(detail));
      if (url === "/api/v1/agents/a1/run" && method === "POST") {
        runCalled = true;
        detail = { ...detail, running: true, live_run: true };
        return Promise.resolve(jsonResponse({ status: "started" }, 202));
      }
      if (url === "/api/v1/agents/a1" && method === "DELETE") {
        deleteAgentCalled = true;
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      const key = `${method} ${url}`;
      if (handlers[key]) return Promise.resolve(handlers[key](body));

      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/agents/a1"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/agents/:id" element={<AgentDetailPage />} />
            <Route path="/agents" element={<div data-testid="agents-list-page">Agents list</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  detail = baseDetail();
  runCalled = false;
  deleteAgentCalled = false;
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test("Run now fires the run POST and streams SSE lines into the activity card", async () => {
  mockFetch();
  wrap();

  const runBtn = await screen.findByRole("button", { name: /run now/i });
  await userEvent.click(runBtn);

  await waitFor(() => expect(runCalled).toBe(true));
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

  const es = FakeEventSource.instances[0]!;
  expect(es.url).toBe("/api/v1/agents/a1/run/progress");
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.onmessage?.({ data: "✓ Sent 2 messages" });

  expect(await screen.findByText("✓ Sent 2 messages")).toBeInTheDocument();
  expect(screen.getByText(/Running Inbox Triager/)).toBeInTheDocument();
});

test("live_run on the detail DTO auto-attaches the activity card on mount, with no click", async () => {
  detail = baseDetail({ live_run: true, running: true });
  mockFetch();
  wrap();

  await screen.findByText("Inbox Triager");
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  expect(FakeEventSource.instances[0]!.url).toBe("/api/v1/agents/a1/run/progress");
  expect(runCalled).toBe(false);
});

test("AGENT.md save shows the ethics_blocked message inline and does not exit edit mode", async () => {
  mockFetch({
    "PUT /api/v1/agents/a1/agent-md": () =>
      jsonResponse({ error: { code: "ethics_blocked", message: "AGENT.md failed safety check: blocked phrase" } }, 400),
  });
  wrap();

  await userEvent.click(await screen.findByRole("button", { name: /^edit$/i }));
  const textarea = screen.getByRole("textbox", { name: "AGENT.md" });
  await userEvent.type(textarea, " more");

  await userEvent.click(screen.getByRole("button", { name: /save agent\.md/i }));

  expect(await screen.findByText(/failed safety check/i)).toBeInTheDocument();
  // Still in edit mode — the textarea is still present.
  expect(screen.getByRole("textbox", { name: "AGENT.md" })).toBeInTheDocument();
});

test("AGENT.md Save is disabled until the content actually changes", async () => {
  mockFetch();
  wrap();

  await userEvent.click(await screen.findByRole("button", { name: /^edit$/i }));
  expect(screen.getByRole("button", { name: /save agent\.md/i })).toBeDisabled();

  await userEvent.type(screen.getByRole("textbox", { name: "AGENT.md" }), "!");
  expect(screen.getByRole("button", { name: /save agent\.md/i })).toBeEnabled();
});

test("missing_secrets renders a warning strip naming the missing secrets", async () => {
  detail = baseDetail({ missing_secrets: ["OPENAI_API_KEY", "SMTP_PASSWORD"] });
  mockFetch();
  wrap();

  expect(await screen.findByText(/expects secrets/i)).toBeInTheDocument();
  expect(screen.getByText(/OPENAI_API_KEY, SMTP_PASSWORD/)).toBeInTheDocument();
});

test("missing_secrets strip is absent when there are none", async () => {
  mockFetch();
  wrap();

  await screen.findByText("Inbox Triager");
  expect(screen.queryByText(/expects secrets/i)).not.toBeInTheDocument();
});

test("schedule save posts the cron expression", async () => {
  const calls = mockFetch({
    "PUT /api/v1/agents/a1/schedule": (body) => {
      detail = { ...detail, schedule: { cron_expr: (body as { cron_expr: string }).cron_expr, next_run_at: null, last_run_at: null, enabled: true } };
      return jsonResponse(detail.schedule);
    },
  });
  wrap();

  const cronInput = await screen.findByLabelText(/cron expression/i);
  await userEvent.clear(cronInput);
  await userEvent.type(cronInput, "*/15 * * * *");
  await userEvent.click(screen.getByRole("button", { name: /save schedule/i }));

  await waitFor(() => {
    const put = calls.find((c) => c.url === "/api/v1/agents/a1/schedule" && c.method === "PUT");
    expect(put).toBeTruthy();
    expect(put!.body).toEqual({ cron_expr: "*/15 * * * *" });
  });
});

test("schedule save shows invalid_cron message inline on 400", async () => {
  mockFetch({
    "PUT /api/v1/agents/a1/schedule": () =>
      jsonResponse({ error: { code: "invalid_cron", message: "invalid cron expression: bad" } }, 400),
  });
  wrap();

  const cronInput = await screen.findByLabelText(/cron expression/i);
  await userEvent.clear(cronInput);
  await userEvent.type(cronInput, "not a cron");

  await userEvent.click(screen.getByRole("button", { name: /save schedule/i }));

  expect(await screen.findByText(/invalid cron expression/i)).toBeInTheDocument();
});

test("schedule Remove deletes the schedule", async () => {
  const calls = mockFetch({
    "DELETE /api/v1/agents/a1/schedule": () => jsonResponse({ ok: true }),
  });
  wrap();

  await userEvent.click(await screen.findByRole("button", { name: /remove/i }));

  await waitFor(() => {
    const del = calls.find((c) => c.url === "/api/v1/agents/a1/schedule" && c.method === "DELETE");
    expect(del).toBeTruthy();
  });
});

test("delete agent: confirm dialog fires DELETE and navigates to /agents", async () => {
  mockFetch();
  wrap();

  await userEvent.click(await screen.findByLabelText(/agent actions/i));
  await userEvent.click(await screen.findByText("Delete…"));

  const heading = await screen.findByRole("heading", { name: /^Delete\s/ });
  expect(heading.textContent).toContain("Inbox Triager");

  await userEvent.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() => expect(deleteAgentCalled).toBe(true));
  expect(await screen.findByTestId("agents-list-page")).toBeInTheDocument();
});

test("Skills card Save PUTs the checked skill_names", async () => {
  detail = baseDetail({
    core_skills: [{ name: "pdf", description: "PDF handling" }],
    all_skills: [{ id: "s1", name: "csv", description: "CSV handling", installed_at: "2026-07-01T00:00:00Z" }],
    attached_skills: ["pdf"],
  });
  const calls = mockFetch({
    "PUT /api/v1/agents/a1/skills": (body) => {
      detail = { ...detail, attached_skills: (body as { skill_names: string[] }).skill_names };
      return jsonResponse(detail);
    },
  });
  wrap();

  await screen.findByText("Skills (1)");
  await userEvent.click(screen.getByRole("checkbox", { name: "csv" }));
  expect(screen.getByText("Skills (2)")).toBeInTheDocument();

  const skillsSave = screen.getByRole("button", { name: "Save skills" });
  await userEvent.click(skillsSave);

  await waitFor(() => {
    const put = calls.find((c) => c.url === "/api/v1/agents/a1/skills" && c.method === "PUT");
    expect(put).toBeTruthy();
    expect(put!.body).toEqual({ skill_names: ["pdf", "csv"] });
  });
});

test("Connections card Save PUTs the checked connection_ids", async () => {
  detail = baseDetail({
    workspace_connections: [
      {
        id: "c1",
        provider: "gmail",
        account_label: "work@example.com",
        account_identity: "work@example.com",
        status: "active",
        created_at: "2026-07-01T00:00:00Z",
      },
    ],
    attached_connection_ids: [],
  });
  const calls = mockFetch({
    "PUT /api/v1/agents/a1/connections": (body) => {
      detail = { ...detail, attached_connection_ids: (body as { connection_ids: string[] }).connection_ids };
      return jsonResponse(detail);
    },
  });
  wrap();

  await screen.findByText("Connections (0)");
  await userEvent.click(screen.getByRole("checkbox", { name: /gmail/i }));

  const connectionsSave = await screen.findByRole("button", { name: "Save connections" });
  await userEvent.click(connectionsSave);

  await waitFor(() => {
    const put = calls.find((c) => c.url === "/api/v1/agents/a1/connections" && c.method === "PUT");
    expect(put).toBeTruthy();
    expect(put!.body).toEqual({ connection_ids: ["c1"] });
  });
});

test("Connections card with an empty pool shows a Connect-services-first note instead of Save", async () => {
  detail = baseDetail({ workspace_connections: [], attached_connection_ids: [] });
  mockFetch();
  wrap();

  expect(await screen.findByText(/connect services first/i)).toBeInTheDocument();
  const link = screen.getByRole("link", { name: /connect services first/i });
  expect(link.getAttribute("href")).toBe("/connections");
});

test("run history renders status, trigger, and expandable output", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("manual")).toBeInTheDocument();
  expect(screen.getByText("OK")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: /show output/i }));
  expect(screen.getByText("Filed 3 emails.")).toBeInTheDocument();
});
