import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { ToastProvider } from "@/components/shell/Toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import ChatWindow from "./ChatWindow";
import { completeTurn, installFakeEventSource, turnAcceptedResponse } from "./turnTestHarness";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

let sendFails = false;
// Flipped once the turn has been started, so the chat detail endpoint serves
// the reply from "history" the way the real server does.
let replied = false;

function mockFetch() {
  sendFails = false;
  replied = false;
  // jsdom has no EventSource, and a turn's completion now arrives over one.
  installFakeEventSource();
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/chats/c1" && method === "GET") {
        return Promise.resolve(
          jsonResponse({
            chat: {
              id: "c1",
              name: "Chat",
              platform: "web",
              active: true,
              created_at: "2026-08-14T07:00:00Z",
              updated_at: "2026-08-14T07:00:00Z",
            },
            messages: replied
              ? [
                  { role: "user", content: "rewrite the intro" },
                  { role: "assistant", content: "Done — I updated the note." },
                ]
              : [],
          }),
        );
      }
      if (url === "/api/v1/chats/c1/messages" && method === "POST") {
        if (sendFails) {
          return Promise.resolve(
            jsonResponse({ error: { code: "internal", message: "boom" } }, 500),
          );
        }
        // The turn is detached now: 202 here, and the reply is served from
        // history once the stream reports done.
        replied = true;
        return Promise.resolve(turnAcceptedResponse());
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function renderChat(qc: QueryClient) {
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <ToastProvider>
          <TooltipProvider>
            <ChatWindow chatId="c1" compact />
          </TooltipProvider>
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function send(text: string) {
  const box = await screen.findByPlaceholderText("Message…");
  await userEvent.type(box, text);
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
}

beforeEach(mockFetch);
afterEach(() => vi.unstubAllGlobals());

// A chat turn is the one thing in this browser that can WRITE to the vault —
// the chat coder holds Read/Write/Edit over it, which is what makes "Edit with
// AI" work at all. Nothing used to tell the knowledge base, so a note open in
// the editor showed its old text until a manual reload.
test("a completed chat turn invalidates the knowledge base queries", async () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const spy = vi.spyOn(qc, "invalidateQueries");
  renderChat(qc);

  await send("rewrite the intro");
  // The knowledge base is invalidated when the TURN completes, not when the
  // POST returns — the POST only starts it now.
  await completeTurn();
  await screen.findByText("Done — I updated the note.");

  const keys = spy.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey));
  expect(keys).toContain(JSON.stringify(["kb-note"]));
  // A turn can CREATE a note, and a tree that doesn't show it is the same bug
  // one level up.
  expect(keys).toContain(JSON.stringify(["kb-tree"]));
});

// Nothing was written, so nothing should be refetched — and an editor showing
// unsaved work must not be prompted about a change that never happened.
test("a failed turn does not invalidate them", async () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderChat(qc);
  await screen.findByPlaceholderText("Message…");
  sendFails = true;
  const spy = vi.spyOn(qc, "invalidateQueries");

  await send("rewrite the intro");
  await waitFor(() => expect(screen.getByText(/boom/)).toBeInTheDocument());

  const keys = spy.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey));
  expect(keys).not.toContain(JSON.stringify(["kb-note"]));
});
