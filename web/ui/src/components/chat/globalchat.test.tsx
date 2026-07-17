import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
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
let nextId: number;

function resetFixtures() {
  chats = [
    { id: "c1", name: "Chat One", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-17T07:00:00Z" },
    { id: "c2", name: "Chat Two", platform: "web", active: false, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-16T00:00:00Z" },
  ];
  messages = { c1: [{ role: "user", content: "hi" }], c2: [] };
  nextId = 3;
}

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/chats" && method === "GET") return Promise.resolve(jsonResponse({ chats }));

      if (url === "/api/v1/chats" && method === "POST") {
        const created: Chat = {
          id: `c${nextId++}`, name: "New chat", platform: "web", active: true,
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

function ChatsRoutePlaceholder() {
  return <div>Chats route</div>;
}

function wrap(initialEntry = "/") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<HomePage />} />
            <Route path="/chats" element={<ChatsRoutePlaceholder />} />
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

test("renders the floating button in the shell", async () => {
  mockFetch();
  wrap();
  expect(await screen.findByRole("button", { name: /open chat/i })).toBeInTheDocument();
});

test("hidden on /chats", async () => {
  mockFetch();
  wrap("/chats");
  await screen.findByText("Chats route");
  expect(screen.queryByRole("button", { name: /open chat/i })).not.toBeInTheDocument();
});

test("clicking the button opens the panel with the most recent active chat's ChatWindow", async () => {
  mockFetch();
  wrap();
  const btn = await screen.findByRole("button", { name: /open chat/i });
  await userEvent.click(btn);

  expect(await screen.findByRole("heading", { name: "Chat One" })).toBeInTheDocument();
  expect(screen.getByText("Chat")).toBeInTheDocument(); // sheet title
});

test("picks the most recently updated ACTIVE chat, not just the first active one in the list", async () => {
  // c0 is active and listed first, but c1 (also active) was updated later —
  // the panel must pick c1, proving it sorts by updated_at rather than
  // taking the first active row it finds.
  chats = [
    { id: "c0", name: "Older Active Chat", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-15T00:00:00Z" },
    { id: "c1", name: "Chat One", platform: "web", active: true, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-17T07:00:00Z" },
    { id: "c2", name: "Chat Two", platform: "web", active: false, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-16T00:00:00Z" },
  ];
  messages = { c0: [], c1: [{ role: "user", content: "hi" }], c2: [] };
  mockFetch();
  wrap();

  const btn = await screen.findByRole("button", { name: /open chat/i });
  await userEvent.click(btn);

  expect(await screen.findByRole("heading", { name: "Chat One" })).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Older Active Chat" })).not.toBeInTheDocument();
});

test("Ctrl/Cmd+J opens the panel", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Home");

  fireEvent.keyDown(window, { key: "j", ctrlKey: true });

  expect(await screen.findByRole("heading", { name: "Chat One" })).toBeInTheDocument();
});

test("Ctrl+J while focus is in an input does not open the panel", async () => {
  mockFetch();
  wrap();
  const input = await screen.findByLabelText("some field");
  input.focus();

  fireEvent.keyDown(input, { key: "j", ctrlKey: true });

  expect(screen.queryByRole("heading", { name: "Chat One" })).not.toBeInTheDocument();
});

test("no active chat: creates one on first open and renders it", async () => {
  chats = [{ id: "c2", name: "Chat Two", platform: "web", active: false, created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-16T00:00:00Z" }];
  messages = { c2: [] };
  mockFetch();
  wrap();

  const btn = await screen.findByRole("button", { name: /open chat/i });
  await userEvent.click(btn);

  expect(await screen.findByRole("heading", { name: "New chat" })).toBeInTheDocument();
  expect(
    vi.mocked(fetch).mock.calls.filter(
      (c) => String(c[0]) === "/api/v1/chats" && (c[1] as RequestInit | undefined)?.method === "POST",
    ).length,
  ).toBe(1);
});

test("open full page link navigates to /chats?chat=<id> and closes the panel", async () => {
  mockFetch();
  wrap();
  const btn = await screen.findByRole("button", { name: /open chat/i });
  await userEvent.click(btn);
  await screen.findByRole("heading", { name: "Chat One" });

  await userEvent.click(screen.getByRole("link", { name: /open full page/i }));

  expect(await screen.findByText("Chats route")).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Chat One" })).not.toBeInTheDocument();
});
