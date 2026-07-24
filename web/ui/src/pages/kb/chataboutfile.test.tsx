import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import ChatAboutFileButton, { chatPrompt } from "./ChatAboutFileButton";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

// No ACTIVE chat in the list: "chat about this file" must create one anyway
// (forceNew), so this fixture also proves the create fires.
let created = 0;

function mockFetch() {
  created = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/chats" && method === "GET") return Promise.resolve(jsonResponse({ chats: [] }));
      if (url === "/api/v1/chats" && method === "POST") {
        created++;
        return Promise.resolve(
          jsonResponse({
            id: "c-new", name: "New chat", platform: "web", active: true,
            created_at: "2026-07-24T07:00:00Z", updated_at: "2026-07-24T07:00:00Z",
          }),
        );
      }
      const detail = url.match(/^\/api\/v1\/chats\/([^/]+)$/);
      if (detail && method === "GET") {
        return Promise.resolve(
          jsonResponse({
            chat: {
              id: detail[1], name: "New chat", platform: "web", active: true,
              created_at: "2026-07-24T07:00:00Z", updated_at: "2026-07-24T07:00:00Z",
            },
            messages: [],
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/kb"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/kb" element={<ChatAboutFileButton path={path} />} />
            <Route path="/chats" element={<div>Chats route</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// The prompt NAMES the file rather than inlining it: the chat coder is already
// rooted at the vault with file tools, so a path is enough — and an inlined
// snapshot would both blow the context on a large note and go stale the moment
// either side edits the file.
test("chatPrompt names the path and does not inline content", () => {
  const p = chatPrompt("notes/trip.md");
  expect(p).toContain("notes/trip.md");
  expect(p).toMatch(/`notes\/trip\.md`/);
});

test("the button opens the chat panel on a NEW chat with the path prefilled", async () => {
  mockFetch();
  wrap("notes/trip.md");

  await userEvent.click(await screen.findByRole("button", { name: /chat about this/i }));

  // forceNew: even with no active chat to resume, a fresh one is created —
  // and a question about a note never lands mid-thread in an unrelated chat.
  const composer = await screen.findByPlaceholderText("Message…");
  expect(composer).toHaveValue(chatPrompt("notes/trip.md"));
  expect(created).toBe(1);
});
