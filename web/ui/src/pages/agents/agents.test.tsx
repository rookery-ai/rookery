import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import AgentsPage from "./AgentsPage";
import AgentNewPage from "./AgentNewPage";
import type { Agent, AgentDraft } from "@/lib/agents";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let agents: Agent[];
let draft: AgentDraft;
let dismissCalled: boolean;

function resetFixtures() {
  agents = [
    {
      id: "a1", name: "Inbox Triager", description: "Reads new mail every morning and files it into folders based on sender and subject line heuristics.",
      active: true, created_at: "2026-07-01T00:00:00Z", running: false,
    },
    {
      id: "a2", name: "Nightly Backup", description: "Backs up the vault.",
      active: false, created_at: "2026-06-01T00:00:00Z", running: false,
    },
    {
      id: "a3", name: "Live Watcher", description: "Watches a feed.",
      active: true, created_at: "2026-07-10T00:00:00Z", running: true,
    },
  ];
  draft = null;
  dismissCalled = false;
}

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/agents" && method === "GET") return Promise.resolve(jsonResponse({ agents, draft }));
      if (url === "/api/v1/agents/design/dismiss" && method === "POST") {
        dismissCalled = true;
        draft = null;
        return Promise.resolve(jsonResponse({ status: "ok" }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap(initialEntry = "/") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<AgentsPage />} />
            <Route path="/agents/new" element={<AgentNewPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
  vi.setSystemTime(new Date("2026-07-17T07:10:00Z"));
});

afterEach(() => {
  vi.useRealTimers();
});

test("renders a card grid with name, chips, and created date", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Inbox Triager")).toBeInTheDocument();
  expect(screen.getByText("Nightly Backup")).toBeInTheDocument();
  expect(screen.getByText("Live Watcher")).toBeInTheDocument();

  const activeCard = screen.getByText("Inbox Triager").closest("a")!;
  expect(activeCard.getAttribute("href")).toBe("/agents/a1");
  expect(activeCard.textContent).toContain("Active");

  const pausedCard = screen.getByText("Nightly Backup").closest("a")!;
  expect(pausedCard.textContent).toContain("Paused");

  const runningCard = screen.getByText("Live Watcher").closest("a")!;
  expect(runningCard.textContent).toContain("Running");
});

// ── Recency ─────────────────────────────────────────────────────────────────
//
// The list is ordered newest-first by the SERVER (apiListAgents), not here —
// db.ListAgents keeps its name ordering for five other callers. So the page
// must render the order it is given rather than imposing one of its own: a
// client-side re-sort would look identical on this fixture and quietly
// override whatever the server decided.
test("renders agents in the order the server sent them", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Inbox Triager");

  const names = screen.getAllByRole("link")
    .map((el) => el.querySelector("h3")?.textContent)
    .filter(Boolean);
  expect(names).toEqual(agents.map((a) => a.name));
});

// "Last run" is the question you open this page with. It REPLACES the created
// date rather than joining it — one date line per card.
test("a card that has run shows its last run instead of its created date", async () => {
  agents = [
    { ...agents[0]!, last_run_at: "2026-07-17T06:10:00Z" },
  ];
  mockFetch();
  wrap();

  const card = (await screen.findByText("Inbox Triager")).closest("a")!;
  expect(card.textContent).toContain("Last run 1h ago");
  expect(card.textContent).not.toContain("Created");
});

// An agent that has never run has nothing else its date line could usefully
// say, so it falls back. null is the server's real answer here; undefined only
// ever means a response from an older build, and both must take this branch.
test.each([
  ["null", null],
  ["absent", undefined],
] as const)("a card with a %s last run falls back to its created date", async (_label, value) => {
  agents = [{ ...agents[0]!, last_run_at: value }];
  mockFetch();
  wrap();

  const card = (await screen.findByText("Inbox Triager")).closest("a")!;
  expect(card.textContent).toContain("Created");
  expect(card.textContent).not.toContain("Last run");
});

test("search filters the grid client-side", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Inbox Triager");

  // SearchInput renders type="search", whose implicit role is searchbox.
  const search = screen.getByRole("searchbox", { name: /search/i });
  await userEvent.type(search, "backup");

  expect(screen.queryByText("Inbox Triager")).not.toBeInTheDocument();
  expect(screen.getByText("Nightly Backup")).toBeInTheDocument();
});

test("New agent button links to /agents/new", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Inbox Triager");
  const link = screen.getByRole("link", { name: /new agent/i });
  expect(link.getAttribute("href")).toBe("/agents/new");
});

test("empty state shows create-first-agent CTA when there are no agents", async () => {
  agents = [];
  mockFetch();
  wrap();

  expect(await screen.findByText(/no agents yet/i)).toBeInTheDocument();
  const cta = screen.getByRole("link", { name: /create.*first agent/i });
  expect(cta.getAttribute("href")).toBe("/agents/new");
});

test("draft card shows Resume link and Discard posts dismiss + refreshes the list", async () => {
  agents = [];
  draft = { agent_id: undefined, agent_name: "Draft Agent", is_edit: false, state: "designing", updated_at: "2026-07-16T00:00:00Z" };
  mockFetch();
  wrap();

  expect(await screen.findByText("Draft Agent")).toBeInTheDocument();
  expect(screen.getByText("Draft")).toBeInTheDocument();

  const resume = screen.getByRole("link", { name: /resume/i });
  expect(resume.getAttribute("href")).toBe("/agents/new?resume=1");

  await userEvent.click(screen.getByRole("button", { name: /discard/i }));

  await waitFor(() => expect(dismissCalled).toBe(true));
  await waitFor(() => {
    const listCalls = vi.mocked(fetch).mock.calls.filter(
      (c) => String(c[0]) === "/api/v1/agents" && (c[1] as RequestInit).method === "GET",
    );
    expect(listCalls.length).toBeGreaterThan(1);
  });
  await waitFor(() => expect(screen.queryByText("Draft Agent")).not.toBeInTheDocument());
});

// SP4 final review fix: AgentNewPage must not mount DesignerSurface with a
// still-null draft on a cold query cache. DesignerSurface decides whether to
// show its resume banner / auto-resume ONCE, on its own mount effect — it
// never re-checks a `draft` prop that arrives later (see SkillNewPage's
// identical waitingForDraft gate and skills.test.tsx). A direct load of
// /agents/new?resume=1 before useAgents() has settled would otherwise mount
// DesignerSurface with draft=null and silently skip the resume.
test("AgentNewPage on a cold cache shows loading, then resumes once the draft query resolves", async () => {
  draft = { agent_id: undefined, agent_name: "Draft Agent", is_edit: false, state: "designing", updated_at: "2026-07-16T00:00:00Z" };

  let resolveAgentsGet!: (res: Response) => void;
  const agentsGetPromise = new Promise<Response>((resolve) => {
    resolveAgentsGet = resolve;
  });

  const calls: Array<{ url: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ url, method });

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      // The agents list (and its embedded `draft`) never resolves until the
      // test explicitly does so below — simulating a cold query cache.
      if (url === "/api/v1/agents" && method === "GET") return agentsGetPromise;
      if (url === "/api/v1/agents/design/state" && method === "GET") {
        return Promise.resolve(jsonResponse({ active: false }));
      }
      if (url === "/api/v1/agents/design/resume" && method === "POST") {
        return Promise.resolve(
          jsonResponse({
            response: "Resuming your draft for **Draft Agent**. Continue, or approve.",
            state: "designing",
            history: [
              { role: "user", content: "watch my inbox" },
              { role: "assistant", content: "got it, anything else?" },
            ],
            agent_id: "",
            agent_name: "Draft Agent",
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  wrap("/agents/new?resume=1");

  // Cold cache: the loading gate is shown, and nothing has tried to auto-resume yet.
  expect(await screen.findByText(/loading/i)).toBeInTheDocument();
  expect(calls.some((c) => c.url === "/api/v1/agents/design/resume")).toBe(false);

  // The draft query resolves...
  resolveAgentsGet(jsonResponse({ agents: [], draft }));

  // ...and DesignerSurface now mounts with the real draft and auto-resumes.
  await waitFor(() => expect(calls.some((c) => c.url === "/api/v1/agents/design/resume")).toBe(true));
  expect(await screen.findByText("watch my inbox")).toBeInTheDocument();
  expect(screen.getByText(/Resuming your draft for/)).toBeInTheDocument();
});
