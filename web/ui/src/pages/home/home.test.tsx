import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
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
      // AgentsAtAGlanceCard reads useAgents(); without this the card renders
      // its empty state and the table assertions below have nothing to find.
      if (url === "/api/v1/agents" && method === "GET") {
        return Promise.resolve(
          jsonResponse({
            agents: [
              { id: "agent-1", name: "Failing Agent", description: "", active: true, created_at: "2026-01-01T00:00:00Z", running: false },
              { id: "agent-2", name: "Backup Bot", description: "", active: true, created_at: "2026-01-01T00:00:00Z", running: false },
            ],
            draft: null,
          }),
        );
      }

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
  const card = heading.parentElement!;
  // Scoped to this card: "Agents at a glance" now also links every agent by
  // name, so an unscoped getByRole("link", { name }) matches twice.
  const link = await within(card).findByRole("link", { name: "Backup Bot" });
  expect(link.getAttribute("href")).toBe("/agents/agent-2");
  expect(card.textContent).toContain("runs");
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

// ── Reminders: completed state, cap-at-3, main-screen card ─────────────────

// Five reminders: two upcoming, three already fired. Enough to exercise the
// pane's 3-row cap AND the pending/done split in one fixture.
function manyReminders() {
  reminders = [
    { id: "r1", message: "Call the dentist", remind_at: "2026-07-17T15:00:00Z", sent: false },
    { id: "r2", message: "Book the flight", remind_at: "2026-07-18T09:00:00Z", sent: false },
    { id: "r3", message: "Water the plants", remind_at: "2026-07-16T08:00:00Z", sent: true },
    { id: "r4", message: "Renew the domain", remind_at: "2026-07-15T08:00:00Z", sent: true },
    { id: "r5", message: "Pay the invoice", remind_at: "2026-07-14T08:00:00Z", sent: true },
  ];
}

test("reminders: a fired reminder is struck through, a pending one is not", async () => {
  mockFetch();
  manyReminders();
  wrap();

  // Upcoming sort first, so both pending rows are within the pane's 3-row cap
  // alongside exactly one completed row.
  const done = await screen.findByText("Water the plants");
  expect(done.className).toMatch(/line-through/);
  // getAllBy: an upcoming reminder shows in BOTH the pane row and the
  // main-screen card. The pane's row is the <p>; assert on that one.
  const pending = screen.getAllByText("Call the dentist").find((el) => el.tagName === "P");
  expect(pending?.className).not.toMatch(/line-through/);
});

test("reminders: the pane caps at 3 and the rest open in the View all modal", async () => {
  mockFetch();
  manyReminders();
  wrap();

  await screen.findByText("Call the dentist");
  // Row 4 and 5 (both completed) are collapsed behind the button.
  expect(screen.queryByText("Renew the domain")).not.toBeInTheDocument();

  const viewAll = screen.getByRole("button", { name: /view all reminders \(5\)/i });
  await userEvent.click(viewAll);

  expect(await screen.findByText("All reminders")).toBeInTheDocument();
  expect(screen.getByText("Renew the domain")).toBeInTheDocument();
  expect(screen.getByText("Pay the invoice")).toBeInTheDocument();
});

test("reminders: upcoming ones also appear in the main-screen card", async () => {
  mockFetch();
  manyReminders();
  wrap();

  const card = await screen.findByRole("region", { name: "Upcoming reminders" });
  // findBy inside the card, not getBy: the region renders (as its empty state)
  // before the reminders query resolves, so a synchronous read races the fetch.
  // The card lists upcoming reminders only — a completed one belongs to the
  // pane, where it can be cleared.
  expect(await within(card).findByText("Call the dentist")).toBeInTheDocument();
  expect(within(card).getByText("Book the flight")).toBeInTheDocument();
  expect(within(card).queryByText("Water the plants")).not.toBeInTheDocument();
});

test("reminders: deleting from the pane also clears the main-screen card", async () => {
  mockFetch();
  manyReminders();
  wrap();

  await screen.findByText("Call the dentist");
  // Two views, one deferred-delete buffer: the row must vanish from BOTH at
  // once, not linger on the dashboard for the 5s undo window.
  const [firstDelete] = screen.getAllByRole("button", { name: /delete reminder/i });
  await userEvent.click(firstDelete);
  await waitFor(() => expect(screen.queryByText("Call the dentist")).not.toBeInTheDocument());
});

// ── The cards that fill what was a half-empty dashboard ─────────────────────
//
// Every one is built from data the SPA already fetches — no new endpoint.

test("quick actions link to the four create surfaces", async () => {
  mockFetch();
  wrap();
  for (const [name, href] of [
    [/new agent/i, "/agents/new"],
    [/new note/i, "/kb"],
    [/start chat/i, "/chats"],
    [/connect a service/i, "/connections"],
  ] as const) {
    const link = await screen.findByRole("link", { name });
    // Links, not buttons: a button with onClick navigate() silently breaks
    // middle-click and "open in new tab".
    expect(link.getAttribute("href")).toBe(href);
  }
});

test("recent activity shows successful runs, not only failures", async () => {
  mockFetch();
  wrap();
  await screen.findByRole("heading", { name: /Ilija/ }); // dashboard resolved
  const card = await screen.findByLabelText("Recent activity");
  // NeedsAttentionCard filters to failures because that IS its job. This card
  // does not: recent_runs was already fetched and its successes were never
  // rendered, so a healthy install looked like it had done nothing.
  expect(within(card).getByRole("link", { name: "Backup Bot" })).toBeInTheDocument();
  expect(within(card).getByRole("link", { name: "Failing Agent" })).toBeInTheDocument();
  expect(card.textContent).toContain("cron");
});

test("agents at a glance scrolls wide content in its own container", async () => {
  mockFetch();
  wrap();
  await screen.findByRole("heading", { name: /Ilija/ });
  const card = await screen.findByLabelText("Agents at a glance");
  await within(card).findByRole("link", { name: "Backup Bot" });
  // The page container is fluid now, so an unbounded table would make the
  // whole document scroll sideways instead of the card.
  expect(card.querySelector(".overflow-x-auto")).toBeTruthy();
});

test("recent notes offers a way into the knowledge base when empty", async () => {
  mockFetch();
  wrap();
  const card = await screen.findByLabelText("Recently edited notes");
  expect(within(card).getByRole("link", { name: /browse the knowledge base/i })).toBeInTheDocument();
});

test("quick actions use the same button treatment as the Agents page", async () => {
  mockFetch();
  wrap();
  // Agents renders its primary action as a default-variant button at default
  // size. Home had all four as outline/sm, so the same kind of action looked
  // like a different control depending on which page you were on.
  const primary = await screen.findByRole("link", { name: /new agent/i });
  expect(primary.className).toContain("bg-primary");
  expect(primary.className).toContain("h-10");

  const secondary = await screen.findByRole("link", { name: /new note/i });
  expect(secondary.className).toContain("h-10");
  expect(secondary.className).not.toContain("bg-primary");
});
