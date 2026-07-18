import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useAgents, useAgentDetail, useAgentActions } from "./agents";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

test("useAgents fetches the list + draft", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse({
        agents: [{ id: "a1", name: "Agent One", description: "", active: true, created_at: "2026-07-01T00:00:00Z", running: false }],
        draft: null,
      }),
    ),
  );
  const { result } = renderHook(() => useAgents(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.agents).toHaveLength(1));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/agents");
});

test("useAgentDetail is disabled for null id and fetches when set", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ agent: { id: "a1" } })));
  const { result, rerender } = renderHook(({ id }) => useAgentDetail(id), {
    wrapper: wrapper(),
    initialProps: { id: null as string | null },
  });
  expect(result.current.fetchStatus).toBe("idle");
  expect(fetch).not.toHaveBeenCalled();

  rerender({ id: "a1" });
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/agents/a1");
});

test("useAgentActions.del DELETEs /api/v1/agents/:id", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.del("a1");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1");
  expect((init as RequestInit).method).toBe("DELETE");
});

test("useAgentActions.run POSTs /api/v1/agents/:id/run", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ status: "started" }, 202)));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  let response: { status: string } | undefined;
  await act(async () => {
    response = await result.current.run("a1");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1/run");
  expect((init as RequestInit).method).toBe("POST");
  expect(response).toEqual({ status: "started" });
});

test("useAgentActions.saveSchedule PUTs cron_expr", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ cron_expr: "0 * * * *" })));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.saveSchedule("a1", "0 * * * *");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1/schedule");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ cron_expr: "0 * * * *" });
});

test("useAgentActions.deleteSchedule DELETEs the schedule endpoint", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.deleteSchedule("a1");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1/schedule");
  expect((init as RequestInit).method).toBe("DELETE");
});

test("useAgentActions.saveAgentMD PUTs content", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ agent: {} })));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.saveAgentMD("a1", "# hello");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1/agent-md");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ content: "# hello" });
});

test("useAgentActions.saveSkills PUTs skill_names", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ agent: {} })));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.saveSkills("a1", ["pdf", "csv"]);
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1/skills");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ skill_names: ["pdf", "csv"] });
});

test("useAgentActions.saveConnections PUTs connection_ids", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ agent: {} })));
  const { result } = renderHook(() => useAgentActions(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.saveConnections("a1", ["c1", "c2"]);
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/agents/a1/connections");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ connection_ids: ["c1", "c2"] });
});

test("useAgentActions.del invalidates the agents list query", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/v1/agents" && (!init || init.method === undefined || init.method === "GET")) {
        return Promise.resolve(jsonResponse({ agents: [], draft: null }));
      }
      return Promise.resolve(jsonResponse({ ok: true }));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const list = renderHook(() => useAgents(), { wrapper: Wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  const actions = renderHook(() => useAgentActions(), { wrapper: Wrapper });
  await act(async () => {
    await actions.result.current.del("a1");
  });

  await waitFor(() => expect(list.result.current.isFetching).toBe(false));
  const listCalls = vi.mocked(fetch).mock.calls.filter((c) => String(c[0]) === "/api/v1/agents");
  expect(listCalls.length).toBeGreaterThan(1);
});
