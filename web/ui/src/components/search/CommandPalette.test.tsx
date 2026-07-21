import { render, screen, fireEvent, act, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route, useParams, useSearchParams } from "react-router";
import { AppShell } from "@/components/shell/AppShell";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

const SEARCH_GROUPS = [
  {
    kind: "notes",
    items: [{ title: "notes/trip.md", path: "notes/trip.md", line: 3, snippet: "pack sunscreen for the trip", url: "/kb?path=notes/trip.md" }],
  },
  {
    kind: "agents",
    items: [{ title: "Daily Digest", id: "a1", url: "/agents/a1" }],
  },
  {
    kind: "chats",
    items: [{ title: "Trip planning", id: "c1", url: "/chats/c1" }],
  },
];

let searchCalls: string[];

function mockFetch(opts?: { chats?: Array<Record<string, unknown>> }) {
  const chats = opts?.chats ?? [
    { id: "c1", name: "Chat One", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-17T07:00:00Z" },
  ];
  searchCalls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/inbox/poll") return Promise.resolve(jsonResponse({ unread: 0, recent: [] }));

      if (url.startsWith("/api/v1/search?q=")) {
        searchCalls.push(url);
        return Promise.resolve(jsonResponse({ query: "x", groups: SEARCH_GROUPS }));
      }

      if (url === "/api/v1/chats" && method === "GET") return Promise.resolve(jsonResponse({ chats }));
      const detail = url.match(/^\/api\/v1\/chats\/([^/]+)$/);
      if (detail && method === "GET") {
        const chat = chats.find((c) => c.id === detail[1]);
        return Promise.resolve(jsonResponse({ chat, messages: [] }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function HomePage() {
  return (
    <div>
      <h1>Home</h1>
      <input aria-label="some field" />
    </div>
  );
}

function KBPlaceholder() {
  const [params] = useSearchParams();
  return <div>KB route: {params.get("path")}</div>;
}

function AgentsNewPlaceholder() {
  return <div>New agent route</div>;
}

function AgentDetailPlaceholder() {
  const { id } = useParams();
  return <div>Agent route: {id}</div>;
}

function ChatsPlaceholder() {
  const [params] = useSearchParams();
  return <div>Chats route: {params.get("chat")}</div>;
}

function SettingsPlaceholder() {
  return <div>Settings route</div>;
}

function wrap(initialEntry = "/") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<HomePage />} />
            <Route path="/kb" element={<KBPlaceholder />} />
            <Route path="/agents/new" element={<AgentsNewPlaceholder />} />
            <Route path="/agents/:id" element={<AgentDetailPlaceholder />} />
            <Route path="/chats" element={<ChatsPlaceholder />} />
            <Route path="/settings" element={<SettingsPlaceholder />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.useRealTimers();
});

async function openPalette() {
  await screen.findByText("Home");
  fireEvent.keyDown(window, { key: "k", ctrlKey: true });
  return screen.findByPlaceholderText(/search or run a command/i);
}

test("Ctrl/Cmd+K opens the palette", async () => {
  mockFetch();
  wrap();
  const input = await openPalette();
  expect(input).toBeInTheDocument();
});

test("Ctrl/Cmd+K while focus is in an input does not open the palette", async () => {
  mockFetch();
  wrap();
  const field = await screen.findByLabelText("some field");
  field.focus();

  fireEvent.keyDown(field, { key: "k", ctrlKey: true });

  expect(screen.queryByPlaceholderText(/search or run a command/i)).not.toBeInTheDocument();
});

test("debounces the query before firing the search request", () => {
  // Render + open with real timers first — mixing testing-library's async
  // findBy/waitFor with fake timers deadlocks (its polling itself relies on
  // setTimeout). Only start faking once we're purely in synchronous
  // fireEvent/act territory, matching pages/kb/search.test.tsx's pattern.
  mockFetch();
  wrap();
  fireEvent.keyDown(window, { key: "k", ctrlKey: true });
  const input = screen.getByPlaceholderText(/search or run a command/i);

  vi.useFakeTimers();
  fireEvent.change(input, { target: { value: "trip" } });
  act(() => {
    vi.advanceTimersByTime(100);
  });
  expect(searchCalls).toHaveLength(0);

  act(() => {
    vi.advanceTimersByTime(150);
  });
  expect(searchCalls).toHaveLength(1);
  expect(searchCalls[0]).toBe("/api/v1/search?q=trip");
});

test("renders grouped results from a mocked search, with the match marked", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();
  const input = await openPalette();
  await user.type(input, "trip");

  expect(await screen.findByText("Notes")).toBeInTheDocument();
  expect(screen.getByText("notes/trip.md")).toBeInTheDocument();
  expect(screen.getByText("Agents")).toBeInTheDocument();
  expect(screen.getByText("Daily Digest")).toBeInTheDocument();
  expect(screen.getByText("Chats")).toBeInTheDocument();
  expect(screen.getByText("Trip planning")).toBeInTheDocument();

  // The notes item's snippet ("pack sunscreen for the trip") contains the
  // query — its match should be wrapped in <mark>.
  expect(await screen.findByText("trip", { selector: "mark" })).toBeInTheDocument();
});

test("selecting a notes result navigates to its /kb?path= route", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();
  const input = await openPalette();
  await user.type(input, "trip");

  await user.click(await screen.findByText("notes/trip.md"));

  expect(await screen.findByText("KB route: notes/trip.md")).toBeInTheDocument();
});

test("selecting an agent result navigates to /agents/:id", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();
  const input = await openPalette();
  await user.type(input, "digest");

  await user.click(await screen.findByText("Daily Digest"));

  expect(await screen.findByText("Agent route: a1")).toBeInTheDocument();
});

test("selecting a chat result navigates to /chats?chat=<id>, not the backend's per-chat url", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();
  const input = await openPalette();
  await user.type(input, "trip");

  await user.click(await screen.findByText("Trip planning"));

  expect(await screen.findByText("Chats route: c1")).toBeInTheDocument();
});

test("the Actions group is always present and 'New agent' navigates to /agents/new", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();
  await openPalette();

  expect(screen.getByText("Actions")).toBeInTheDocument();
  await user.click(screen.getByText("New agent"));

  expect(await screen.findByText("New agent route")).toBeInTheDocument();
});

test("'Ask assistant' only appears once the query is non-empty, and opens the chat panel with the query prefilled", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();
  const input = await openPalette();

  expect(screen.queryByText(/ask assistant about/i)).not.toBeInTheDocument();

  await user.type(input, "trip");
  const askItem = await screen.findByText("Ask assistant about 'trip'");
  await user.click(askItem);

  expect(await screen.findByRole("heading", { name: "Chat One" })).toBeInTheDocument();
  const composer = await screen.findByPlaceholderText(/message/i);
  await waitFor(() => expect((composer as HTMLTextAreaElement).value).toBe("trip"));
});

test("'Ask assistant' with a new query does NOT clobber an in-progress draft in an already-open chat panel", async () => {
  // GlobalChatPanel is NOT remounted when the slide-over's content is
  // swapped for another <GlobalChatPanel initialText=.../> — AppShell just
  // re-renders panel.node in place, same component type, new props. If the
  // Composer's initialText effect unconditionally overwrote `value`, a
  // second "Ask assistant" invocation while the chat panel is already open
  // would silently wipe out whatever the user had been typing.
  mockFetch();
  const user = userEvent.setup();
  wrap();

  // Open the chat panel the normal way (floating button's Ctrl+J) and type
  // a draft that was never sent.
  fireEvent.keyDown(window, { key: "j", ctrlKey: true });
  const composer = await screen.findByPlaceholderText(/message/i);
  await user.type(composer, "my unsent draft");
  expect((composer as HTMLTextAreaElement).value).toBe("my unsent draft");

  // Now invoke the palette's "Ask assistant" with a DIFFERENT query while
  // that draft is still sitting in the composer.
  fireEvent.keyDown(window, { key: "k", ctrlKey: true });
  const paletteInput = await screen.findByPlaceholderText(/search or run a command/i);
  await user.type(paletteInput, "differentquery");
  const askItem = await screen.findByText("Ask assistant about 'differentquery'");
  await user.click(askItem);

  // The chat panel is still showing the same chat (proving no remount)...
  expect(await screen.findByRole("heading", { name: "Chat One" })).toBeInTheDocument();
  // ...and the draft must have survived, not been replaced by the new query.
  const composerAfter = await screen.findByPlaceholderText(/message/i);
  await waitFor(() => expect((composerAfter as HTMLTextAreaElement).value).toBe("my unsent draft"));
});

// The palette has existed since the shell landed, but ⌘K was its only trigger
// — with no visible affordance it read as "this app has no global search".
test("the search button opens the palette, and ⌘K still works alongside it", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Home");

  await userEvent.click(screen.getByRole("button", { name: /search everything/i }));
  expect(await screen.findByPlaceholderText(/search or run a command/i)).toBeInTheDocument();

  // Close, then reopen with the keyboard — the button and the shortcut drive
  // the same state, so neither can strand the other.
  await userEvent.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByPlaceholderText(/search or run a command/i)).not.toBeInTheDocument(),
  );
  fireEvent.keyDown(window, { key: "k", ctrlKey: true });
  expect(await screen.findByPlaceholderText(/search or run a command/i)).toBeInTheDocument();
});
