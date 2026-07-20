import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import HomePage, { groupByDay } from "./HomePage";
import type { InboxMessage, Dashboard } from "@/lib/home";

// Redesign of the inbox (spec §5.2): day grouping, two-row cards, status
// badges, unread accent bar, and an expanded view that earns the click. This
// file pins the behaviours the old three-greys inbox didn't have —
// home.test.tsx keeps the read/delete/mark-all-read wiring assertions.

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

function emptyDashboard(): Dashboard {
  return {
    display_name: "Ilija",
    agent_count: 0,
    active_agent_count: 0,
    recent_runs: [],
    upcoming: [],
    has_connector: true,
  };
}

let messages: InboxMessage[];

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/dashboard") return Promise.resolve(jsonResponse(emptyDashboard()));
      if (url === "/api/v1/services") return Promise.resolve(jsonResponse({ providers: [] }));
      if (url === "/api/v1/reminders" && method === "GET") return Promise.resolve(jsonResponse({ reminders: [] }));

      if (url === "/api/v1/inbox" && method === "GET") {
        return Promise.resolve(
          jsonResponse({ messages, unread: messages.filter((m) => !m.read).length }),
        );
      }
      const readMatch = url.match(/^\/api\/v1\/inbox\/([^/]+)\/read$/);
      if (readMatch && method === "POST") {
        const id = readMatch[1];
        messages = messages.map((m) => (m.id === id ? { ...m, read: true } : m));
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<HomePage />} />
            <Route path="/agents/:id" element={<div>agent detail placeholder</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.useRealTimers();
});

// ── groupByDay (pure) ────────────────────────────────────────────────────────

function msg(overrides: Partial<InboxMessage>): InboxMessage {
  return {
    id: "id",
    source: "agent_run",
    agent_id: "",
    agent_name: "",
    trigger: "",
    status: "ok",
    body: "",
    read: false,
    created_at: "2026-07-20T09:00:00Z",
    ...overrides,
  };
}

test("groupByDay buckets messages under Today / Yesterday / a dated label", () => {
  const now = new Date("2026-07-20T12:00:00Z");
  const a = msg({ id: "a", created_at: "2026-07-20T09:00:00Z" }); // today
  const b = msg({ id: "b", created_at: "2026-07-19T09:00:00Z" }); // yesterday
  const c = msg({ id: "c", created_at: "2026-07-14T09:00:00Z" }); // older

  const groups = groupByDay([a, b, c], now);

  expect(groups.map((g) => g.label)).toEqual(["Today", "Yesterday", "Tue, 14 Jul"]);
  expect(groups[0].messages).toEqual([a]);
  expect(groups[1].messages).toEqual([b]);
  expect(groups[2].messages).toEqual([c]);
});

// The day after a DST spring-forward, the previous local day is 23h long, so
// deriving "yesterday" as todayMs - 24h lands mid-day-before-yesterday and the
// label silently degrades to a date. Europe/Skopje springs forward 2026-03-29.
test("groupByDay labels Yesterday correctly across a DST spring-forward", () => {
  const now = new Date("2026-03-30T12:00:00+02:00");
  const y = msg({ id: "y", created_at: "2026-03-29T12:00:00+01:00" });

  const groups = groupByDay([y], now);

  expect(groups.map((g) => g.label)).toEqual(["Yesterday"]);
});

// Bucketing is keyed by day, so out-of-order input can never split a day —
// but the HEADERS would render inverted if their order came from
// first-appearance. Groups are sorted newest-first explicitly.
test("groupByDay orders day groups newest-first regardless of input order", () => {
  const now = new Date("2026-07-20T12:00:00Z");
  const older = msg({ id: "o", created_at: "2026-07-14T09:00:00Z" });
  const yesterday = msg({ id: "y", created_at: "2026-07-19T09:00:00Z" });
  const today = msg({ id: "t", created_at: "2026-07-20T09:00:00Z" });

  const groups = groupByDay([older, yesterday, today], now);

  expect(groups.map((g) => g.label)).toEqual(["Today", "Yesterday", "Tue, 14 Jul"]);
});

test("groupByDay keeps same-day messages in one bucket, in their given order", () => {
  const now = new Date("2026-07-20T12:00:00Z");
  const a = msg({ id: "a", created_at: "2026-07-20T10:00:00Z" });
  const b = msg({ id: "b", created_at: "2026-07-20T09:00:00Z" });

  const groups = groupByDay([a, b], now);

  expect(groups).toHaveLength(1);
  expect(groups[0].label).toBe("Today");
  expect(groups[0].messages.map((m) => m.id)).toEqual(["a", "b"]);
});

// ── Rendering ────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.setSystemTime(new Date("2026-07-20T12:00:00Z"));
});

test("groups messages under Today / Yesterday / date headers", async () => {
  messages = [
    msg({ id: "m1", agent_name: "Digest Bot", created_at: "2026-07-20T09:00:00Z", body: "today's run" }),
    msg({ id: "m2", agent_name: "Digest Bot", created_at: "2026-07-19T09:00:00Z", body: "yesterday's run" }),
    msg({ id: "m3", agent_name: "Digest Bot", created_at: "2026-07-14T09:00:00Z", body: "an older run" }),
  ];
  mockFetch();
  wrap();

  expect(await screen.findByText("Today")).toBeInTheDocument();
  expect(screen.getByText("Yesterday")).toBeInTheDocument();
  expect(screen.getByText(/14 Jul/)).toBeInTheDocument();
});

test("error status renders a badge; ok status does not", async () => {
  messages = [
    msg({ id: "m1", agent_name: "Failing Agent", status: "error", body: "it broke" }),
    msg({ id: "m2", agent_name: "Healthy Agent", status: "ok", body: "all fine" }),
  ];
  mockFetch();
  wrap();

  await screen.findByText("Failing Agent");
  // Exact + case-sensitive: the "Needs attention" card elsewhere on the page
  // has static prose containing the substring "failed" ("no failed runs."),
  // so a loose /failed/i match would false-positive there too.
  expect(screen.getByText("Failed")).toBeInTheDocument();
  expect(screen.getAllByText("Failed")).toHaveLength(1);
});

test("expanding reveals full body and the View agent link", async () => {
  messages = [
    msg({
      id: "m1",
      agent_id: "agent-1",
      agent_name: "Daily Digest",
      trigger: "cron",
      body: "This is the full body of the notification.",
    }),
  ];
  mockFetch();
  wrap();

  // Click the card's own text rather than a role-name regex — "Reminders"
  // section's "Add reminder" button (and similar) can otherwise collide
  // with a loose name match.
  fireEvent.click(await screen.findByText("Daily Digest"));

  const link = await screen.findByRole("link", { name: /view agent/i });
  expect(link).toHaveAttribute("href", "/agents/agent-1");
  expect(screen.getByText(/this is the full body of the notification\./i)).toBeInTheDocument();
  expect(screen.getByText(/cron/i)).toBeInTheDocument();
});

test("a reminder-sourced message offers no View agent link", async () => {
  messages = [
    msg({
      id: "m1",
      source: "reminder",
      agent_id: "",
      agent_name: "",
      trigger: "",
      body: "Call the dentist",
    }),
  ];
  mockFetch();
  wrap();

  fireEvent.click(await screen.findByText("Call the dentist"));

  await waitFor(() => expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument());
  expect(screen.queryByRole("link", { name: /view agent/i })).not.toBeInTheDocument();
});
