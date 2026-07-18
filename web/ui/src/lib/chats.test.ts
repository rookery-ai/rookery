import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useChats, sendChatMessage } from "./chats";
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

test("sendChatMessage resolves the response string for the legacy success shape", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ response: "hi" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  await expect(sendChatMessage("c1", "hello")).resolves.toBe("hi");
});

test("sendChatMessage rejects with ApiError(200,\"chat_error\",...) for the legacy error shape", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "Couldn't reach X" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  let caught: unknown;
  try {
    await sendChatMessage("c1", "hello");
  } catch (e) {
    caught = e;
  }
  expect(caught).toBeInstanceOf(ApiError);
  expect(caught).toMatchObject({ status: 200, code: "chat_error", message: "Couldn't reach X" });
});
