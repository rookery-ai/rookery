import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import {
  useDashboard,
  useReminders,
  useCreateReminder,
  useDeleteReminder,
  useInbox,
  useMarkInboxRead,
  useMarkAllInboxRead,
  useDeleteInboxMessage,
  useInboxPoll,
  greeting,
} from "./home";
import { ApiError } from "./api";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// ── greeting ─────────────────────────────────────────────────────────────────

test("greeting is 'Good morning' before noon", () => {
  expect(greeting(0)).toBe("Good morning");
  expect(greeting(6)).toBe("Good morning");
  expect(greeting(11)).toBe("Good morning");
});

test("greeting is 'Good afternoon' from noon to before 6pm", () => {
  expect(greeting(12)).toBe("Good afternoon");
  expect(greeting(14)).toBe("Good afternoon");
  expect(greeting(17)).toBe("Good afternoon");
});

test("greeting is 'Good evening' from 6pm onward", () => {
  expect(greeting(18)).toBe("Good evening");
  expect(greeting(23)).toBe("Good evening");
});

// ── useDashboard ─────────────────────────────────────────────────────────────

test("useDashboard fetches from the right URL and normalizes nil arrays", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse({
        display_name: "Ilija",
        agent_count: 0,
        active_agent_count: 0,
        recent_runs: null,
        upcoming: null,
        has_connector: false,
      }),
    ),
  );
  const { result } = renderHook(() => useDashboard(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.display_name).toBe("Ilija"));
  expect(result.current.data?.recent_runs).toEqual([]);
  expect(result.current.data?.upcoming).toEqual([]);
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/dashboard");
});

// ── Reminders ────────────────────────────────────────────────────────────────

test("useReminders fetches the reminders list and normalizes nil", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ reminders: null })));
  const { result } = renderHook(() => useReminders(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.reminders).toEqual([]));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/reminders");
});

test("useCreateReminder posts message+when and invalidates reminders", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ id: "r1", message: "hi", remind_at: "t1", sent: false }, 201)),
  );
  const { result } = renderHook(() => useCreateReminder(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({ message: "hi", when: "in 10 minutes" });
  });
  const call = vi.mocked(fetch).mock.calls[0];
  expect(call[0]).toBe("/api/v1/reminders");
  expect(JSON.parse(String((call[1] as RequestInit).body))).toEqual({ message: "hi", when: "in 10 minutes" });
});

test("useCreateReminder rejects with ApiError(400,'unparseable_time',...) on bad input", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse({ error: { code: "unparseable_time", message: "couldn't understand that time" } }, 400),
    ),
  );
  const { result } = renderHook(() => useCreateReminder(), { wrapper: wrapper() });
  let caught: unknown;
  await act(async () => {
    try {
      await result.current.mutateAsync({ message: "hi", when: "banana" });
    } catch (e) {
      caught = e;
    }
  });
  expect(caught).toBeInstanceOf(ApiError);
  expect((caught as ApiError).code).toBe("unparseable_time");
});

test("useDeleteReminder DELETEs by id", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useDeleteReminder(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync("r1");
  });
  const call = vi.mocked(fetch).mock.calls[0];
  expect(call[0]).toBe("/api/v1/reminders/r1");
  expect((call[1] as RequestInit).method).toBe("DELETE");
});

// ── Inbox ────────────────────────────────────────────────────────────────────

test("useInbox fetches messages+unread and normalizes nil messages", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ messages: null, unread: 0 })));
  const { result } = renderHook(() => useInbox(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.messages).toEqual([]));
  expect(result.current.data?.unread).toBe(0);
});

test("useMarkInboxRead posts to /:id/read", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useMarkInboxRead(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync("m1");
  });
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/inbox/m1/read");
});

test("useMarkAllInboxRead posts to /read-all", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useMarkAllInboxRead(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync();
  });
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/inbox/read-all");
});

test("useDeleteInboxMessage DELETEs by id", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useDeleteInboxMessage(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync("m1");
  });
  const call = vi.mocked(fetch).mock.calls[0];
  expect(call[0]).toBe("/api/v1/inbox/m1");
  expect((call[1] as RequestInit).method).toBe("DELETE");
});

test("useInboxPoll fetches the poll endpoint and normalizes nil recent", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ unread: 3, recent: null })));
  const { result } = renderHook(() => useInboxPoll(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.unread).toBe(3));
  expect(result.current.data?.recent).toEqual([]);
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/inbox/poll");
});
