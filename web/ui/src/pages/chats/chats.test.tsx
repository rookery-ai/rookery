import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import ChatsPage from "./ChatsPage";
import type { Chat, ChatMessage } from "@/lib/chats";
import {
  completeTurn,
  latestStream,
  installFakeEventSource,
  turnAcceptedResponse,
} from "./turnTestHarness";

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
        // The server persists the user's message BEFORE running the coder, and
        // keeps it even when the turn fails — so it lands here either way. Only
        // the assistant reply depends on success.
        messages[id] = [...(messages[id] ?? []), { role: "user", content: body.message }];
        if (!result.error) {
          messages[id] = [
            ...messages[id],
            { role: "assistant", content: result.response ?? "" },
          ];
        }
        // 202: the turn is now detached and the reply arrives via the stream.
        return Promise.resolve(turnAcceptedResponse());
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
  // jsdom has no EventSource, and a turn's reply now arrives over one.
  installFakeEventSource();
  vi.setSystemTime(new Date("2026-07-17T07:10:00Z"));
});

afterEach(() => {
  vi.useRealTimers();
});

// The list row shows name + date only. `chat.active` is a chat-platform routing
// pointer and the idle sweeper's reflection gate, not a web lifecycle the user
// acts on — surfacing it meant every chat you clicked visibly flipped to
// "Active" (ChatWindow resumes on open), which read as a bug because it was
// reporting an internal flag as if it were state the user chose. Fixture chats
// One and Two differ on `active`, so a re-introduced chip fails here.
test("lists chat sessions with name and relative time, and no status chip", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Chat One")).toBeInTheDocument();
  expect(screen.getByText("Chat Two")).toBeInTheDocument();

  const rowOne = screen.getByText("Chat One").closest("button")!;
  expect(rowOne.textContent).toContain("10m ago");
  expect(rowOne.textContent).not.toContain("Active");

  const rowTwo = screen.getByText("Chat Two").closest("button")!;
  expect(rowTwo.textContent).toContain("Jul 10");
  expect(rowTwo.textContent).not.toContain("Stopped");
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

  // The optimistic bubble is up before the turn finishes — that is the point
  // of it, and it now also matches a row the server has already persisted.
  expect(await screen.findByText("what's up")).toBeInTheDocument();

  // The reply arrives from the refetch the stream's done event triggers, not
  // from the POST: the turn outlives the request that started it.
  await completeTurn();
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

// A coder failure is no longer a property of the POST — the turn is detached,
// so it fails mid-stream. The tracker pushes "⚠️ <error>" as its last milestone
// and closes with a named error event; the window promotes that to the banner.
// Losing this would leave the owner with a message, no reply, and no reason.
test("a failed turn shows an inline banner, keeps the user bubble, and re-enables the composer", async () => {
  mockFetch(() => ({ error: "coder is unavailable" }));
  wrap("/?chat=c1");
  await screen.findByText("hi");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "ping");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByText("ping")).toBeInTheDocument();

  const es = await vi.waitFor(() => {
    const s = latestStream();
    if (!s) throw new Error("no stream opened");
    return s;
  });
  es.emit("⚠️ coder is unavailable");
  es.dispatchNamedEvent("error");

  expect(await screen.findByText("coder is unavailable")).toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  // The owner's message survives the failure — they typed it, and it is the
  // context for the retry.
  expect(screen.getByText("ping")).toBeInTheDocument();
});

// The chat header carries no Stop/Resume control. Its label flipped on
// `chat.active`, which would have kept reporting an internal flag after the
// chips were dropped. The endpoints it called are still registered and still
// used — by the auto-resume below, and by the chat platforms.
test("the chat header offers no Stop or Resume control", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Resume" })).toBeNull();
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

test("New chat creates a chat and selects it", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Chat One");

  await userEvent.click(screen.getByRole("button", { name: "New chat" }));

  expect(await screen.findByRole("heading", { name: "New chat" })).toBeInTheDocument();
  expect(
    vi.mocked(fetch).mock.calls.some(
      (c) => String(c[0]) === "/api/v1/chats" && (c[1] as RequestInit | undefined)?.method === "POST",
    ),
  ).toBe(true);
});

// ── Composer focus ─────────────────────────────────────────────────────────

test("New chat puts the caret in the composer", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Chat One");

  await userEvent.click(screen.getByRole("button", { name: "New chat" }));
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

// The auto-resume is a per-OPEN gesture, not a standing policy that the chat
// must stay active. Nothing in the web UI stops a chat any more, but two things
// outside it do — the 30-minute idle sweeper in internal/chat, and `/chat stop`
// from a chat platform — and either can land while this window sits mounted.
// The fixture is mutated directly to stand in for that, then a send forces the
// detail refetch that surfaces it. The latch is per-mount and already spent, so
// no second resume fires.
test("a chat stopped elsewhere mid-mount is not re-resumed", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await user.click(await screen.findByText("Chat Two"));
  await waitFor(() => expect(actionCalls).toEqual(["c2/resume"]));

  chats = chats.map((c) => (c.id === "c2" ? { ...c, active: false } : c));

  await user.type(await screen.findByPlaceholderText("Message…"), "ping{Enter}");
  // The reply lands on the refetch the finished turn triggers, and that refetch
  // is what surfaces the externally-stopped chat — which is the condition under
  // test, so the turn has to actually complete.
  await completeTurn();
  await screen.findByText("echo: ping");

  expect(actionCalls).toEqual(["c2/resume"]);
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

// The messages must share the composer's 10% column. Asserted on the RENDERED
// class, because the failure mode here is silent: ChatScroll's base padding was
// the `p-4` shorthand, which tailwind-merge does NOT treat as conflicting with
// `px-[10%]` — both survived and the winner was left to stylesheet ordering, so
// the composer could end up inset while the bubbles were not.
test("the message scroll region carries the same 10% gutter as the composer", async () => {
  mockFetch();
  const { container } = wrap("/?chat=c1");
  await screen.findByText("hello there");

  const scroll = container.querySelector('[data-testid="chat-window"] .overflow-y-auto')!;
  expect(scroll.className).toContain("px-[10%]");
  expect(scroll.className).not.toContain("px-4");
});

// ── ?new=1 — Home's "Start chat" ────────────────────────────────────────────
//
// The Home quick action is named "Start chat" and used to land on the bare
// list, whose empty state reads "Select a chat or start a new one" — so the
// button that says it starts a chat started nothing, and the user had to find
// "New chat" in the context pane themselves.
//
// Auto-creation is keyed on ?new=1 rather than on "no chat is selected"
// because the icon rail's own /chats entry carries no parameter: browsing to
// Chats to READ history must not mint an empty chat on every visit.

function countCreates() {
  const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls;
  return calls.filter(
    ([input, init]) =>
      String(input) === "/api/v1/chats" &&
      ((init as RequestInit | undefined)?.method ?? "GET") === "POST",
  ).length;
}

test("?new=1 creates a chat, opens it, and puts the caret in the composer", async () => {
  mockFetch();
  wrap("/?new=1");

  const box = await screen.findByPlaceholderText("Message…");
  await waitFor(() => expect(document.activeElement).toBe(box));
  expect(screen.queryByText(/select a chat or start a new one/i)).toBeNull();
});

// The effect that creates the chat depends on the chat list, which refetches.
// Without the ref guard every refetch mints another chat — the same shape as
// ChatWindow's streamOpenRef.
test("?new=1 creates exactly one chat even as the list refetches", async () => {
  mockFetch();
  const { rerender } = wrap("/?new=1");
  await screen.findByPlaceholderText("Message…");

  rerender(<div />);
  await waitFor(() => expect(countCreates()).toBe(1));
});

// The rail links to a bare /chats. Creating there would hand a user browsing
// their own history a new empty chat on every visit.
test("plain /chats creates nothing and still shows the empty state", async () => {
  mockFetch();
  wrap();
  await screen.findByText(/select a chat or start a new one/i);

  expect(countCreates()).toBe(0);
});
