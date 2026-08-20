import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { ToastProvider } from "@/components/shell/Toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import ChatWindow, { turnFailureMessage } from "./ChatWindow";
import {
  installFakeEventSource,
  latestStream,
  turnAcceptedResponse,
} from "./turnTestHarness";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const CHAT = {
  id: "c1",
  name: "Chat",
  platform: "web",
  active: true,
  created_at: "2026-08-20T07:00:00Z",
  updated_at: "2026-08-20T07:00:00Z",
};

// detail is what GET /api/v1/chats/c1 answers; a test sets it to describe the
// server-side state it wants to land in.
let detail: Record<string, unknown>;

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/chats/c1" && method === "GET") {
        return Promise.resolve(jsonResponse(detail));
      }
      if (url === "/api/v1/chats/c1/messages" && method === "POST") {
        return Promise.resolve(turnAcceptedResponse());
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

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

beforeEach(() => {
  installFakeEventSource();
  detail = { chat: CHAT, messages: [], in_flight: false, turn_lines: [] };
  mockFetch();
});

afterEach(() => vi.unstubAllGlobals());

// THE REPORTED BUG. Leave the page mid-turn, come back, and the chat was empty:
// the message was persisted only after the coder returned, so for the whole
// turn it existed nowhere but this component's state. Mounting fresh against a
// running turn is exactly that "come back" case.
test("a chat mounted mid-turn shows the message and a live progress card", async () => {
  detail = {
    chat: CHAT,
    messages: [{ role: "user", content: "how much did I spend?" }],
    in_flight: true,
    turn_lines: ["🔧 search_files(transactions)"],
  };
  renderChat();

  expect(await screen.findByText("how much did I spend?")).toBeInTheDocument();

  const card = await screen.findByTestId("activity-card");
  expect(card).toHaveTextContent("search_files(transactions)");
  expect(screen.getByTestId("activity-status-dot")).toHaveClass("animate-pulse");
});

// A returning client attaches to the stream rather than waiting for the next
// milestone, which on a slow turn would leave it watching an empty card.
test("mounting mid-turn attaches to the progress stream", async () => {
  detail = { chat: CHAT, messages: [], in_flight: true, turn_lines: [] };
  renderChat();

  await vi.waitFor(() => expect(latestStream()).toBeDefined());
  expect(latestStream()!.url).toBe("/api/v1/chats/c1/turn/progress");
});

// Collapsed shows what the coder is doing NOW; expanding shows the whole
// history of actions.
test("the progress card collapses to the current action and expands to all of them", async () => {
  detail = {
    chat: CHAT,
    messages: [],
    in_flight: true,
    turn_lines: ["🔧 read_file(a.md)", "🔧 read_file(b.md)"],
  };
  renderChat();

  // Collapsed by default: the header says what the coder is doing NOW, and the
  // earlier steps are behind the toggle rather than pushing the reply off screen.
  const card = await screen.findByTestId("activity-card");
  expect(card).toHaveTextContent("read_file(b.md)");
  expect(card).not.toHaveTextContent("read_file(a.md)");

  fireEvent.click(within(card).getByTestId("activity-toggle"));
  await waitFor(() => {
    expect(card).toHaveTextContent("read_file(a.md)");
    expect(card).toHaveTextContent("read_file(b.md)");
  });
});

// Live milestones arriving on the stream reach the card.
test("a milestone arriving mid-turn is appended to the card", async () => {
  detail = { chat: CHAT, messages: [], in_flight: true, turn_lines: [] };
  renderChat();

  const es = await vi.waitFor(() => {
    const s = latestStream();
    if (!s) throw new Error("no stream");
    return s;
  });
  es.emit("🔧 web_search(weather skopje)");

  expect(await screen.findByTestId("activity-card")).toHaveTextContent(
    "web_search(weather skopje)",
  );
});

// The optimistic bubble and the row the server persisted DURING the turn are
// the same message. reconcilePending keys on role::content, so it collapses
// them — without it the sender sees their own message twice.
test("the optimistic bubble does not duplicate the persisted message", async () => {
  renderChat();
  const box = await screen.findByPlaceholderText("Message…");
  await userEvent.type(box, "hello");

  // From here the server has the message, as it does the moment the turn starts.
  detail = {
    chat: CHAT,
    messages: [
      { role: "user", content: "hello" },
      { role: "assistant", content: "hi back" },
    ],
    in_flight: false,
    turn_lines: [],
  };
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  const es = await vi.waitFor(() => {
    const s = latestStream();
    if (!s) throw new Error("no stream");
    return s;
  });
  es.dispatchNamedEvent("done");

  expect(await screen.findByText("hi back")).toBeInTheDocument();
  await waitFor(() => expect(screen.getAllByText("hello")).toHaveLength(1));
});

// Closing the stream must not read as a turn failure — a proxy can drop it, and
// EventSource fires onerror during its own transparent reconnect. Only the
// SERVER's named error event means the turn itself died.
test("a transport drop is not reported as a turn failure", async () => {
  renderChat();
  const box = await screen.findByPlaceholderText("Message…");
  await userEvent.type(box, "hello");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  const es = await vi.waitFor(() => {
    const s = latestStream();
    if (!s) throw new Error("no stream");
    return s;
  });
  es.onerror?.();

  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  expect(screen.queryByText(/turn failed/i)).not.toBeInTheDocument();
});

// The reason a failed turn gives is the stream's last milestone; the fallback
// exists because a turn can die before emitting anything, and an empty banner
// reads as nothing having happened.
test("turnFailureMessage unwraps the warning marker and falls back", () => {
  expect(turnFailureMessage(["🔧 read_file(a.md)", "⚠️ provider exploded"])).toBe(
    "provider exploded",
  );
  expect(turnFailureMessage([])).toBe("The chat turn failed.");
});
