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

function resetFixtures() {
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

test("send round-trip: optimistic user bubble appears immediately, assistant bubble after resolution", async () => {
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "what's up");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByText("what's up")).toBeInTheDocument();
  expect(await screen.findByText("echo: what's up")).toBeInTheDocument();

  // Composer re-enabled once the round trip settles.
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
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

test("Resume posts to the resume endpoint", async () => {
  chats = chats.map((c) => (c.id === "c1" ? { ...c, active: false } : c));
  mockFetch();
  wrap("/?chat=c1");
  await screen.findByText("hi");

  await userEvent.click(screen.getByRole("button", { name: "Resume" }));

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
