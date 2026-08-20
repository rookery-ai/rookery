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
// Every message POSTed to a chat, so the test can assert the citation was SENT
// rather than parked in the composer.
let sentMessages: string[] = [];

function mockFetch() {
  created = 0;
  sentMessages = [];
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
      const send = url.match(/^\/api\/v1\/chats\/([^/]+)\/messages$/);
      if (send && method === "POST") {
        sentMessages.push(JSON.parse(String(init?.body ?? "{}")).message);
        return Promise.resolve(jsonResponse({ response: "Here's a summary." }));
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

// The property that makes it safe to auto-send. A citation ending in a dangling
// separator is a sentence waiting to be finished, and sending it alone asks the
// model nothing — the failure selectionChatPrompt already records.
test("chatPrompt carries an instruction rather than trailing off", () => {
  const p = chatPrompt("notes/trip.md").trimEnd();
  expect(p).not.toMatch(/[—:-]$/);
  expect(p.length).toBeGreaterThan("notes/trip.md".length + 20);
});

test("the button opens a NEW chat and SENDS the citation as a message", async () => {
  mockFetch();
  wrap("notes/trip.md");

  await userEvent.click(await screen.findByRole("button", { name: /chat about this/i }));

  // forceNew: even with no active chat to resume, a fresh one is created —
  // and a question about a note never lands mid-thread in an unrelated chat.
  await vi.waitFor(() => expect(created).toBe(1));

  // Sent, not parked. A composer prefill lives only in component state, so it
  // was lost on the remount at /chats and left that chat genuinely empty.
  await vi.waitFor(() => {
    expect(sentMessages).toEqual([chatPrompt("notes/trip.md")]);
  });

  // And the composer is NOT holding a copy of it.
  const composer = await screen.findByPlaceholderText("Message…");
  expect(composer).toHaveValue("");
});
