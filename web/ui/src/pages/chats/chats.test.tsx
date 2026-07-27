import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import ChatsPage from "./ChatsPage";
import type { Chat, ChatMessage } from "@/lib/chats";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let chats: Chat[];
let messages: Record<string, ChatMessage[]>;
// Every stop/resume POST, in order, as "<chatId>/<action>" — the auto-resume
// tests below assert on the exact sequence, not just that a call happened.
let actionCalls: string[];

function resetFixtures() {
  actionCalls = [];
  chats = [
    { id: "c1", name: "Chat One", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-17T07:00:00Z" },
    { id: "c2", name: "Chat Two", platform: "web", active: false, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-10T00:00:00Z" },
  ];
  messages = {
    c1: [{ role: "user", content: "hi" }, { role: "assistant", content: "hello there" }],
    c2: [],
  };
}

// `onSend` lets an individual test override the legacy message response
// (success shape vs. the 200-with-error shape) without duplicating the
// whole dispatcher.
function mockFetch(onSend?: (id: string, message: string) => { response?: string; error?: string }) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/chats" && method === "GET") return Promise.resolve(jsonResponse({ chats }));

      if (url === "/api/v1/chats" && method === "POST") {
        const created: Chat = {
          id: "c3", name: "New chat", platform: "web", active: true,
          created_at: "2026-07-17T07:10:00Z", updated_at: "2026-07-17T07:10:00Z",
        };
        chats = [...chats, created];
        messages[created.id] = [];
        return Promise.resolve(jsonResponse(created));
      }

      const detail = url.match(/^\/api\/v1\/chats\/([^/]+)$/);
      if (detail && method === "GET") {
        const chat = chats.find((c) => c.id === detail[1]);
        if (!chat) return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "not found" } }, 404));
        return Promise.resolve(jsonResponse({ chat, messages: messages[detail[1]] ?? [] }));
      }
      if (detail && method === "DELETE") {
        chats = chats.filter((c) => c.id !== detail[1]);
        delete messages[detail[1]];
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (detail && method === "PATCH") {
        const body = JSON.parse(String(init?.body)) as { name: string };
        chats = chats.map((c) => (c.id === detail[1] ? { ...c, name: body.name } : c));
        return Promise.resolve(jsonResponse(chats.find((c) => c.id === detail[1])!));
      }

      const send = url.match(/^\/api\/v1\/chats\/([^/]+)\/messages$/);
      if (send && method === "POST") {
        const id = send[1];
        const body = JSON.parse(String(init?.body)) as { message: string };
        const result = onSend ? onSend(id, body.message) : { response: `echo: ${body.message}` };
        if (!result.error) {
          messages[id] = [
            ...(messages[id] ?? []),
            { role: "user", content: body.message },
            { role: "assistant", content: result.response ?? "" },
          ];
        }
        return Promise.resolve(jsonResponse(result));
      }

      const action = url.match(/^\/api\/v1\/chats\/([^/]+)\/(stop|resume)$/);
      if (action && method === "POST") {
        const [, id, kind] = action;
        actionCalls.push(`${id}/${kind}`);
        chats = chats.map((c) => (c.id === id ? { ...c, active: kind === "resume" } : c));
        return Promise.resolve(jsonResponse({ ok: true }));
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
            <Route path="/" element={<ChatsPage />} />
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

test("lists chat sessions with name, Active/Stopped chip, and relative time", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Chat One")).toBeInTheDocument();
  expect(screen.getByText("Chat Two")).toBeInTheDocument();

  const rowOne = screen.getByText("Chat One").closest("button")!;
  expect(rowOne.textContent).toContain("Active");
  expect(rowOne.textContent).toContain("10m ago");

  const rowTwo = screen.getByText("Chat Two").closest("button")!;
  expect(rowTwo.textContent).toContain("Stopped");
  expect(rowTwo.textContent).toContain("Jul 10");
});

test("no chat selected shows the empty state", async () => {
  mockFetch();
  wrap();
  expect(await screen.findByText(/select a chat or start a new one/i)).toBeInTheDocument();
});

test("selecting a session via the search param renders its history", async () => {
  mockFetch();
  wrap("/?chat=c1");

  expect(await screen.findByText("hi")).toBeInTheDocument();
  expect(screen.getByText("hello there")).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Chat One" })).toBeInTheDocument();
});

test("clicking a session row selects it and shows its history", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Chat One");

  await userEvent.click(screen.getByText("Chat Two"));
  expect(await screen.findByRole("heading", { name: "Chat Two" })).toBeInTheDocument();
});

test("editing the chat title renames the chat", async () => {
  mockFetch();
  wrap("/?chat=c1");

  const title = await screen.findByRole("heading", { name: "Chat One" });
  fireEvent.click(title); // enter edit mode
  const input = (await screen.findByLabelText("Chat title")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "Renamed Chat" } });
  fireEvent.keyDown(input, { key: "Enter" });

  expect(await screen.findByRole("heading", { name: "Renamed Chat" })).toBeInTheDocument();
});

test("send round-trip: optimistic user bubble appears immediately, assistant bubble after resolution", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  const chatsCallsBefore = vi.mocked(fetch).mock.calls.filter(
    (c) => String(c[0]) === "/api/v1/chats" && (c[1] as RequestInit | undefined)?.method === "GET",
  ).length;

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "what's up");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByText("what's up")).toBeInTheDocument();
  expect(await screen.findByText("echo: what's up")).toBeInTheDocument();

  // Composer re-enabled once the round trip settles.
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());

  // The pending bubbles are deduped against the freshly-fetched history
  // rather than blindly cleared — still exactly one of each after settling.
  expect(screen.getAllByText("what's up")).toHaveLength(1);
  expect(screen.getAllByText("echo: what's up")).toHaveLength(1);

  // A send also invalidates the ["chats"] list query so the session list's
  // timestamp/order refreshes (the list is mounted alongside ChatWindow via
  // ChatsPage's ContextPane in this harness).
  await waitFor(() => {
    const chatsCallsAfter = vi.mocked(fetch).mock.calls.filter(
      (c) => String(c[0]) === "/api/v1/chats" && (c[1] as RequestInit | undefined)?.method === "GET",
    ).length;
    expect(chatsCallsAfter).toBeGreaterThan(chatsCallsBefore);
  });
});

test("a 200-with-error response shows an inline banner, keeps the user bubble, and re-enables the composer", async () => {
  mockFetch(() => ({ error: "coder is unavailable" }));
  wrap("/?chat=c1");
  await screen.findByText("hi");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "ping");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByText("ping")).toBeInTheDocument();
  expect(await screen.findByText("coder is unavailable")).toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  // The failed send must not be treated as a settled round trip.
  expect(screen.getByText("ping")).toBeInTheDocument();
});

test("Stop posts to the stop endpoint and flips the chip/button to Resume", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  await userEvent.click(screen.getByRole("button", { name: "Stop" }));

  await waitFor(() =>
    expect(
      vi.mocked(fetch).mock.calls.some(
        (c) => String(c[0]) === "/api/v1/chats/c1/stop" && (c[1] as RequestInit | undefined)?.method === "POST",
      ),
    ).toBe(true),
  );
  expect(await screen.findByRole("button", { name: "Resume" })).toBeInTheDocument();
});

// Reaching the manual Resume button now takes a Stop first: opening a chat
// that is ALREADY stopped auto-resumes it (see the auto-resume tests below), so
// the only way the control is on screen is after the user stopped it here.
test("Resume posts to the resume endpoint", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  await userEvent.click(screen.getByRole("button", { name: "Stop" }));
  await userEvent.click(await screen.findByRole("button", { name: "Resume" }));

  await waitFor(() =>
    expect(
      vi.mocked(fetch).mock.calls.some(
        (c) => String(c[0]) === "/api/v1/chats/c1/resume" && (c[1] as RequestInit | undefined)?.method === "POST",
      ),
    ).toBe(true),
  );
});

test("Delete confirms, DELETEs the chat, and clears the selection", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  await userEvent.click(screen.getByLabelText("Chat actions"));
  await userEvent.click(await screen.findByText("Delete…"));
  expect(await screen.findByRole("heading", { name: /^Delete\s/ })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() =>
    expect(
      vi.mocked(fetch).mock.calls.some(
        (c) => String(c[0]) === "/api/v1/chats/c1" && (c[1] as RequestInit | undefined)?.method === "DELETE",
      ),
    ).toBe(true),
  );
  expect(await screen.findByText(/select a chat or start a new one/i)).toBeInTheDocument();
  expect(screen.queryByText("Chat One")).not.toBeInTheDocument();
});

test("shows the creation timestamp as secondary text", async () => {
  mockFetch();
  wrap();

  await screen.findByText("Chat One");
  const rowOne = screen.getByText("Chat One").closest("button")!;
  expect(rowOne.textContent).toMatch(/Jul 1/);
});

test("opens a newly created chat on the first click", async () => {
  // The GET /api/v1/chats list is deliberately slow to "refetch" (a real
  // 300ms delay) so the invalidateQueries triggered by chat creation cannot
  // possibly land before the assertion below runs — reproducing the window
  // where the ["chats"] cache hasn't caught up with a just-created chat by
  // the time ChatsPage's dead-selection guard runs. Without an optimistic
  // cache insert in useCreateChat.onSuccess, the guard sees the newly-set
  // selection missing from the still-stale cached list and clears it.
  let localChats: Chat[] = [
    { id: "c1", name: "Chat One", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-17T07:00:00Z" },
  ];
  const created: Chat = {
    id: "new1", name: "New chat", platform: "web", active: true,
    created_at: "2026-07-17T07:10:00Z", updated_at: "2026-07-17T07:10:00Z",
  };

  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/chats" && method === "GET") {
        return new Promise((resolve) => setTimeout(() => resolve(jsonResponse({ chats: localChats })), 300));
      }
      if (url === "/api/v1/chats" && method === "POST") {
        localChats = [...localChats, created];
        return Promise.resolve(jsonResponse(created));
      }
      if (url === "/api/v1/chats/new1" && method === "GET") {
        return Promise.resolve(jsonResponse({ chat: created, messages: [] }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  wrap();
  await screen.findByText("Chat One", {}, { timeout: 2000 });

  await userEvent.click(screen.getByRole("button", { name: /new chat/i }));

  // Give the dead-selection guard's effect a tick to run before asserting —
  // well under the 300ms list "refetch" delay above, so the cached list can
  // only already contain the new chat via the optimistic insert.
  await new Promise((r) => setTimeout(r, 100));

  // The chat window for new1 must be shown — not cleared by the
  // dead-selection guard — before any list refetch lands.
  expect(await screen.findByTestId("chat-window")).toBeInTheDocument();
});

test("+ New chat creates a chat and selects it", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Chat One");

  await userEvent.click(screen.getByRole("button", { name: "+ New chat" }));

  expect(await screen.findByRole("heading", { name: "New chat" })).toBeInTheDocument();
  expect(
    vi.mocked(fetch).mock.calls.some(
      (c) => String(c[0]) === "/api/v1/chats" && (c[1] as RequestInit | undefined)?.method === "POST",
    ),
  ).toBe(true);
});

// ── Composer focus ─────────────────────────────────────────────────────────

test("+ New chat puts the caret in the composer", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Chat One");

  await userEvent.click(screen.getByRole("button", { name: "+ New chat" }));
  await screen.findByRole("heading", { name: "New chat" });

  // Starting a chat is a "let me type now" gesture — having to click into the
  // box first is a wasted step.
  const composer = await screen.findByPlaceholderText("Message…");
  await waitFor(() => expect(document.activeElement).toBe(composer));
});

// Reversal of an earlier deliberate choice (this test used to assert the
// opposite): on the chats page, every way of arriving at a chat is now treated
// as "I came here to type", so the caret goes into the composer on selection —
// not only for a chat this page just created.
test("selecting an EXISTING chat focuses the composer", async () => {
  mockFetch();
  wrap();
  const row = await screen.findByText("Chat One");

  await userEvent.click(row);
  await screen.findByRole("heading", { name: "Chat One" });

  const composer = await screen.findByPlaceholderText("Message…");
  await waitFor(() => expect(document.activeElement).toBe(composer));
});

// ── Opening a chat: auto-resume + composer focus ─────────────────────────────

test("opening a stopped chat resumes it once and focuses the composer", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await user.click(await screen.findByText("Chat Two")); // c2, active: false

  await waitFor(() => expect(actionCalls).toEqual(["c2/resume"]));
  await waitFor(() => expect(screen.getByPlaceholderText("Message…")).toHaveFocus());
});

test("opening an already-active chat resumes nothing", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await user.click(await screen.findByText("Chat One")); // c1, active: true
  await screen.findByText("hello there");

  expect(actionCalls).toEqual([]);
});

// The auto-resume is a per-open gesture, not a policy that a chat must be
// active: pressing Stop afterwards has to stick.
test("stopping a chat after an auto-resume does not re-resume it", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await user.click(await screen.findByText("Chat Two"));
  await waitFor(() => expect(actionCalls).toEqual(["c2/resume"]));

  await user.click(await screen.findByRole("button", { name: "Stop" }));
  await waitFor(() => expect(actionCalls).toEqual(["c2/resume", "c2/stop"]));
  await new Promise((r) => setTimeout(r, 50));
  expect(actionCalls).toEqual(["c2/resume", "c2/stop"]);
});

// ── FAB clearance ───────────────────────────────────────────────────────────

// The reported bug: the floating action buttons sit in the bottom-right corner
// directly on top of the composer's Send button. The 10% gutter clears them on
// a wide window but not below ~1100px, so a page-level composer also pushes the
// FAB stack up. Asserted through the real AppShell, not a stubbed context.
function fabStack() {
  return screen.getByLabelText("Search everything").parentElement!;
}

test("an open chat lifts the FAB stack clear of the composer", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByPlaceholderText("Message…");

  await waitFor(() => expect(fabStack().className).toContain("md:bottom-24"));
});

test("with no chat open (no composer) the FAB stack sits in its normal corner", async () => {
  mockFetch();
  wrap();
  await screen.findByText(/select a chat or start a new one/i);

  expect(fabStack().className).toContain("md:bottom-6");
  expect(fabStack().className).not.toContain("md:bottom-24");
});
