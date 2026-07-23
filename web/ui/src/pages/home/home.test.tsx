import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import HomePage from "./HomePage";
import type { InboxMessage, Reminder, Dashboard } from "@/lib/home";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let dashboard: Dashboard;
let messages: InboxMessage[];
let unread: number;
let reminders: Reminder[];

function resetFixtures() {
  dashboard = {
    display_name: "Ilija",
    agent_count: 2,
    active_agent_count: 1,
    recent_runs: [
      {
        id: "run-1",
        agent_id: "agent-1",
        agent_name: "Failing Agent",
        status: "failed",
        trigger: "manual",
        started_at: "2026-07-17T06:00:00Z",
        finished_at: "2026-07-17T06:01:00Z",
      },
      {
        id: "run-2",
        agent_id: "agent-2",
        agent_name: "Backup Bot",
        status: "success",
        trigger: "cron",
        started_at: "2026-07-17T05:00:00Z",
        finished_at: "2026-07-17T05:01:00Z",
      },
    ],
    upcoming: [
      { agent_id: "agent-2", agent_name: "Backup Bot", cron_expr: "0 8 * * *", next_run_at: "2026-07-18T08:00:00Z" },
    ],
    has_connector: true,
  };
  messages = [
    {
      id: "m1",
      source: "agent_run",
      agent_id: "agent-1",
      agent_name: "Digest Bot",
      trigger: "manual",
      status: "ok",
      body: "This is the full body of the first notification.",
      read: false,
      created_at: "2026-07-17T07:00:00Z",
    },
  ];
  unread = 1;
  reminders = [{ id: "r1", message: "Call the dentist", remind_at: "2026-07-17T15:00:00Z", sent: false }];
}

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/dashboard") return Promise.resolve(jsonResponse(dashboard));
      if (url === "/api/v1/services") return Promise.resolve(jsonResponse({ providers: [] }));

      if (url === "/api/v1/inbox" && method === "GET") return Promise.resolve(jsonResponse({ messages, unread }));
      // Generalized to any id (not just "m1") so multi-message fixtures used
      // by the keyboard-nav wiring tests can mark-read/delete any row.
      const inboxReadMatch = url.match(/^\/api\/v1\/inbox\/([^/]+)\/read$/);
      if (inboxReadMatch && method === "POST") {
        const id = inboxReadMatch[1];
        messages = messages.map((m) => (m.id === id ? { ...m, read: true } : m));
        unread = messages.filter((m) => !m.read).length;
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url === "/api/v1/inbox/read-all" && method === "POST") {
        messages = messages.map((m) => ({ ...m, read: true }));
        unread = 0;
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      const inboxDeleteMatch = url.match(/^\/api\/v1\/inbox\/([^/]+)$/);
      if (inboxDeleteMatch && method === "DELETE") {
        const id = inboxDeleteMatch[1];
        messages = messages.filter((m) => m.id !== id);
        unread = messages.filter((m) => !m.read).length;
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      if (url === "/api/v1/reminders" && method === "GET") return Promise.resolve(jsonResponse({ reminders }));
      if (url === "/api/v1/reminders/r1" && method === "DELETE") {
        reminders = reminders.filter((r) => r.id !== "r1");
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url === "/api/v1/reminders" && method === "POST") {
        const body = JSON.parse(String(init?.body));
        const text: string = body.text ?? "";
        if (text.includes("banana")) {
          return Promise.resolve(
            jsonResponse({ error: { code: "unparseable_time", message: "couldn't understand that time" } }, 400),
          );
        }
        const r = { id: "r2", message: "check the oven", remind_at: "2026-07-17T16:00:00Z", sent: false };
        reminders = [...reminders, r];
        return Promise.resolve(jsonResponse(r, 201));
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
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
  vi.setSystemTime(new Date("2026-07-17T09:00:00Z")); // 9am UTC → "Good morning" for a UTC test env
});

afterEach(() => {
  vi.useRealTimers();
});

test("greets by display_name once the dashboard loads", async () => {
  mockFetch();
  wrap();
  expect(await screen.findByRole("heading", { name: /Ilija/ })).toBeInTheDocument();
});

test("renders stat tiles with active agents, recent runs + failed badge, connected services", async () => {
  mockFetch();
  wrap();
  // Wait for the dashboard fetch to resolve — the "active agents" label
  // renders immediately (static text), but its value starts at 0 until
  // useDashboard's data lands, so gate on the greeting (dash-dependent).
  await screen.findByRole("heading", { name: /Ilija/ });
  const activeTile = screen.getByText("active agents").parentElement!;
  expect(activeTile.textContent).toContain("1"); // active_agent_count

  const runsTile = screen.getByText("recent runs").parentElement!;
  expect(runsTile.textContent).toContain("2"); // recent_runs.length
  expect(runsTile.textContent).toContain("1 failed");

  const servicesTile = screen.getByText("connected services").parentElement!;
  expect(servicesTile.textContent).toContain("0");
});

test("Next up lists upcoming schedules with a link to the agent", async () => {
  mockFetch();
  wrap();
  const heading = await screen.findByText("Next up");
  const link = await screen.findByRole("link", { name: "Backup Bot" });
  expect(link.getAttribute("href")).toBe("/agents/agent-2");
  expect(heading.parentElement!.textContent).toContain("runs");
});

test("Needs attention shows failed runs with view-log and ask-the-designer links", async () => {
  mockFetch();
  wrap();
  await screen.findByText(/failed —/);
  const viewLog = screen.getByRole("link", { name: /view log/i });
  expect(viewLog.getAttribute("href")).toBe("/agents/agent-1");
  const askDesigner = screen.getByRole("link", { name: /ask the designer to fix it/i });
  expect(askDesigner.getAttribute("href")).toBe("/agents/agent-1/edit");
});

test("empty needs-attention state when no runs failed", async () => {
  dashboard.recent_runs = dashboard.recent_runs.filter((r) => r.status !== "failed");
  mockFetch();
  wrap();
  expect(await screen.findByText(/all caught up/i)).toBeInTheDocument();
});

test("inbox: click marks as read, expands body, and delete removes it", async () => {
  mockFetch();
  wrap();

  const card = await screen.findByText("Digest Bot");
  expect(screen.getByLabelText(/1 unread/)).toBeInTheDocument();

  await userEvent.click(card);
  await waitFor(() => expect(screen.queryByLabelText(/unread/)).not.toBeInTheDocument());

  const del = screen.getByRole("button", { name: "Delete" });
  await userEvent.click(del);
  await waitFor(() => expect(screen.queryByText("Digest Bot")).not.toBeInTheDocument());
  expect(await screen.findByText(/no notifications yet/i)).toBeInTheDocument();
});

test("inbox: mark all read clears the unread count", async () => {
  mockFetch();
  wrap();
  await screen.findByLabelText(/1 unread/);
  await userEvent.click(screen.getByRole("button", { name: /mark all read/i }));
  await waitFor(() => expect(screen.queryByLabelText(/unread/)).not.toBeInTheDocument());
});

test("reminders: delete removes a reminder", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Call the dentist");
  await userEvent.click(screen.getByRole("button", { name: /delete reminder/i }));
  await waitFor(() => expect(screen.queryByText("Call the dentist")).not.toBeInTheDocument());
});

test("reminders: adding an unparseable sentence shows the error inline and keeps the text", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Call the dentist");

  await userEvent.type(screen.getByLabelText(/^reminder$/i), "check the oven banana");
  await userEvent.click(screen.getByRole("button", { name: /add reminder/i }));

  expect(await screen.findByText(/couldn't understand that time/i)).toBeInTheDocument();
  // Text is preserved on failure — no premature reset.
  expect(screen.getByLabelText(/^reminder$/i)).toHaveValue("check the oven banana");
});

test("reminders: adding a valid sentence clears the field and toasts the resolved time", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Call the dentist");

  await userEvent.type(screen.getByLabelText(/^reminder$/i), "in 10 minutes to check the oven");
  await userEvent.click(screen.getByRole("button", { name: /add reminder/i }));

  await waitFor(() => expect(screen.getByLabelText(/^reminder$/i)).toHaveValue(""));
  // The trust surface: the app echoes back the time it figured out. The toast
  // message renders in both the visible toast and the sr-only aria-live region,
  // so match all occurrences.
  expect((await screen.findAllByText(/reminder set for/i)).length).toBeGreaterThan(0);
});

// ── Keyboard nav wiring (useListNav) ─────────────────────────────────────────

function threeDayGroupMessages(): InboxMessage[] {
  // Two messages "Today" (2026-07-17, per the fixed system time above) and
  // one "Yesterday" — exercises groupByDay's day-boundary grouping plus the
  // index-offset accumulator that maps a flat useListNav index onto a
  // specific day group + row.
  return [
    {
      id: "m1",
      source: "agent_run",
      agent_id: "agent-1",
      agent_name: "Digest Bot",
      trigger: "manual",
      status: "ok",
      body: "Alpha message today.",
      read: true,
      created_at: "2026-07-17T07:00:00Z",
    },
    {
      id: "m2",
      source: "agent_run",
      agent_id: "agent-1",
      agent_name: "Digest Bot",
      trigger: "manual",
      status: "ok",
      body: "Bravo message today.",
      read: true,
      created_at: "2026-07-17T06:00:00Z",
    },
    {
      id: "m3",
      source: "reminder",
      agent_id: "",
      agent_name: "",
      trigger: "",
      status: "ok",
      body: "Charlie message yesterday.",
      read: true,
      created_at: "2026-07-16T07:00:00Z",
    },
  ];
}

test("inbox: j/k move a visibly-highlighted row across day-group boundaries", async () => {
  messages = threeDayGroupMessages();
  unread = 0;
  mockFetch();
  wrap();

  await screen.findByText("Alpha message today.");
  const row = (text: string) => screen.getByText(text).closest("[data-highlighted]")!;

  // Starts on the first row, with a visible (not just DOM-only) signal.
  expect(row("Alpha message today.")).toHaveAttribute("data-highlighted", "true");
  expect(row("Alpha message today.")).toHaveClass("ring-2");

  fireEvent.keyDown(document.body, { key: "j" });
  expect(row("Bravo message today.")).toHaveAttribute("data-highlighted", "true");
  expect(row("Alpha message today.")).toHaveAttribute("data-highlighted", "false");

  // Crossing from the "Today" group into "Yesterday" — this only works if
  // the index-offset accumulator carries across group boundaries.
  fireEvent.keyDown(document.body, { key: "j" });
  expect(row("Charlie message yesterday.")).toHaveAttribute("data-highlighted", "true");
});

test("inbox: Enter activates the highlighted row — expands it and marks it read, same as a click", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Digest Bot");
  expect(screen.getByLabelText(/1 unread/)).toBeInTheDocument();

  fireEvent.keyDown(document.body, { key: "Enter" });

  await waitFor(() => expect(screen.queryByLabelText(/unread/)).not.toBeInTheDocument());
  expect(await screen.findByRole("button", { name: "Delete" })).toBeInTheDocument();
});

test("inbox: deleting the highlighted row moves the highlight to the next row", async () => {
  messages = threeDayGroupMessages();
  unread = 0;
  mockFetch();
  wrap();
  await screen.findByText("Alpha message today.");

  // Highlight Bravo (index 1), then activate it via Enter to reveal its
  // Delete button (mirrors how a real user would delete the highlighted
  // row: highlight it, open it, delete it).
  fireEvent.keyDown(document.body, { key: "j" });
  expect(screen.getByText("Bravo message today.").closest("[data-highlighted]")).toHaveAttribute(
    "data-highlighted",
    "true",
  );
  fireEvent.keyDown(document.body, { key: "Enter" });
  fireEvent.click(await screen.findByRole("button", { name: "Delete" }));

  // Bravo is hidden immediately (useDeferredDelete's optimistic `pending`
  // filter, before the 5s undo window even starts) — the highlighted INDEX
  // (1) is left as-is by useListNav's clamp effect, so it now names
  // whatever shifted up into that slot: Charlie. This is the deliberate
  // "moves to the next row" policy documented in useKeyboardNav.ts, not an
  // accidental drift.
  await waitFor(() => expect(screen.queryByText("Bravo message today.")).not.toBeInTheDocument());
  expect(screen.getByText("Charlie message yesterday.").closest("[data-highlighted]")).toHaveAttribute(
    "data-highlighted",
    "true",
  );
});

test("inbox: Enter on 'Mark all read' does not also activate the highlighted row (no double-fire)", async () => {
  mockFetch();
  wrap();
  await screen.findByLabelText(/1 unread/);

  const markAllBtn = screen.getByRole("button", { name: /mark all read/i });
  markAllBtn.focus();
  fireEvent.keyDown(markAllBtn, { key: "Enter" });

  // If this Enter had ALSO reached useListNav's onActivate, the highlighted
  // row (the only message, index 0) would have expanded and shown its
  // Delete button. It must not.
  expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
});

test("inbox: j/Enter do nothing to the list while the shortcuts overlay is open over it", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Digest Bot");

  fireEvent.keyDown(document.body, { key: "?" });
  expect(await screen.findByRole("dialog", { name: /shortcuts/i })).toBeInTheDocument();

  // With the guard bypassed this Enter would expand the background row
  // (the sole message starts highlighted) and reveal its Delete button.
  fireEvent.keyDown(document.body, { key: "j" });
  fireEvent.keyDown(document.body, { key: "Enter" });
  expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
});
