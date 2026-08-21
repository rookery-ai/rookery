import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useChats, startChatTurn, chatTurnProgressURL } from "./chats";
import { ApiError } from "./api";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

test("useChats fetches the chat list from the right URL", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          chats: [
            { id: "c1", name: "Chat 1", platform: "web", active: true, created_at: "t1", updated_at: "t1" },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  const { result } = renderHook(() => useChats(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.chats[0].id).toBe("c1"));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/chats");
});

// The endpoint STARTS a turn rather than completing one, so it answers 202 with
// a turn id. The old {"response": …} / {"error": …} 200-shape is gone, and with
// it the legacy parsing: a refused turn is now a real status code.
test("startChatTurn posts the message and returns the turn id", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ turn_id: "t1" }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  await expect(startChatTurn("c1", "hello")).resolves.toEqual({ turn_id: "t1" });

  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/chats/c1/messages");
  expect(JSON.parse(String(init?.body))).toEqual({ message: "hello" });
});

// A chat already working on a turn refuses the second, so a double-send cannot
// point two coders at one conversation. It is a 409, which api.post raises
// through the same ApiError path every other failure uses.
test("startChatTurn surfaces a refused turn as an ApiError", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ error: "turn_in_flight", message: "This chat is already working on a turn." }),
        { status: 409, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  let caught: unknown;
  try {
    await startChatTurn("c1", "hello");
  } catch (e) {
    caught = e;
  }
  expect(caught).toBeInstanceOf(ApiError);
  expect(caught).toMatchObject({ status: 409 });
});

// The progress URL is defined beside the starter so the two cannot drift.
test("chatTurnProgressURL points at the turn's SSE endpoint", () => {
  expect(chatTurnProgressURL("c1")).toBe("/api/v1/chats/c1/turn/progress");
});
