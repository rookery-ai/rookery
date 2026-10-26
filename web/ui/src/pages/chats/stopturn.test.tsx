import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { ToastProvider } from "@/components/shell/Toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import ChatWindow from "./ChatWindow";
import { installFakeEventSource } from "./turnTestHarness";

const CHAT = {
  id: "c1",
  name: "Chat",
  platform: "web",
  active: true,
  created_at: "2026-10-26T10:00:00Z",
  updated_at: "2026-10-26T10:00:00Z",
};

let cancelStatus = 204;
let fetchMock: ReturnType<typeof vi.fn>;

function json(body: unknown, status = 200) {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  installFakeEventSource();
  cancelStatus = 204;
  fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/chats/c1" && method === "GET") {
      return Promise.resolve(
        json({
          chat: CHAT,
          messages: [{ role: "user", content: "what is the code?" }],
          in_flight: true,
          turn_lines: ["🔧 web_search(code)"],
        }),
      );
    }
    if (url === "/api/v1/chats/c1/turn/cancel" && method === "POST") {
      return Promise.resolve(json({ error: "no active turn" }, cancelStatus));
    }
    return Promise.resolve(json({}));
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

function renderChat() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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

function cancelCalls() {
  return fetchMock.mock.calls.filter(
    ([u, init]) => String(u) === "/api/v1/chats/c1/turn/cancel" && init?.method === "POST",
  );
}

test("Stop on a running turn asks the server to cancel it", async () => {
  renderChat();
  fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
  await vi.waitFor(() => expect(cancelCalls()).toHaveLength(1));
});

// The turn finished between the click and the request: nothing went wrong, so
// nothing is reported.
test("a Stop that arrives after the turn ended shows no error", async () => {
  cancelStatus = 404;
  renderChat();
  fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
  await vi.waitFor(() => expect(cancelCalls()).toHaveLength(1));
  expect(screen.queryByText(/no active turn/i)).not.toBeInTheDocument();
});
