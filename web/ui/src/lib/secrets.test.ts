import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { useSecrets, useAddSecret, useDeleteSecret } from "./secrets";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

test("useSecrets fetches the name-only list", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ secrets: [{ name: "OPENAI_API_KEY" }] })),
  );
  const { result } = renderHook(() => useSecrets(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.secrets).toHaveLength(1));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/secrets");
  expect(result.current.data?.secrets[0]).toEqual({ name: "OPENAI_API_KEY" });
});

test("useAddSecret POSTs name + value and invalidates the list", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true }, 201)));
  const { result } = renderHook(() => useAddSecret(), { wrapper: wrapper() });
  let response: { ok: boolean } | undefined;
  await act(async () => {
    response = await result.current.mutateAsync({ name: "OPENAI_API_KEY", value: "sk-abc123" });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/secrets");
  expect((init as RequestInit).method).toBe("POST");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    name: "OPENAI_API_KEY",
    value: "sk-abc123",
  });
  expect(response?.ok).toBe(true);
});

test("useDeleteSecret DELETEs /api/v1/secrets/:name with master_password body", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useDeleteSecret(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({ name: "OPENAI_API_KEY", masterPassword: "hunter2" });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/secrets/OPENAI_API_KEY");
  expect((init as RequestInit).method).toBe("DELETE");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ master_password: "hunter2" });
});

test("useDeleteSecret URI-encodes the name in the path", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useDeleteSecret(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({ name: "My Secret", masterPassword: "hunter2" });
  });
  const [url] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/secrets/My%20Secret");
});

test("useAddSecret invalidates the secrets list query", async () => {
  let secrets: { name: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/secrets" && method === "GET") {
        return Promise.resolve(jsonResponse({ secrets }));
      }
      if (url === "/api/v1/secrets" && method === "POST") {
        secrets = [{ name: "NEW_SECRET" }];
        return Promise.resolve(jsonResponse({ ok: true }, 201));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const list = renderHook(() => useSecrets(), { wrapper: Wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  const add = renderHook(() => useAddSecret(), { wrapper: Wrapper });
  await act(async () => {
    await add.result.current.mutateAsync({ name: "NEW_SECRET", value: "v" });
  });

  await waitFor(() => expect(list.result.current.isFetching).toBe(false));
  const getCalls = vi.mocked(fetch).mock.calls.filter(
    (c) => String(c[0]) === "/api/v1/secrets" && ((c[1] as RequestInit | undefined)?.method ?? "GET") === "GET",
  );
  expect(getCalls.length).toBeGreaterThan(1);
});

test("useDeleteSecret invalidates the secrets list query", async () => {
  let secrets: { name: string }[] = [{ name: "OPENAI_API_KEY" }];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/secrets" && method === "GET") {
        return Promise.resolve(jsonResponse({ secrets }));
      }
      if (url === "/api/v1/secrets/OPENAI_API_KEY" && method === "DELETE") {
        secrets = [];
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const list = renderHook(() => useSecrets(), { wrapper: Wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  const del = renderHook(() => useDeleteSecret(), { wrapper: Wrapper });
  await act(async () => {
    await del.result.current.mutateAsync({ name: "OPENAI_API_KEY", masterPassword: "hunter2" });
  });

  await waitFor(() => expect(list.result.current.isFetching).toBe(false));
  const listCalls = vi.mocked(fetch).mock.calls.filter((c) => String(c[0]) === "/api/v1/secrets");
  expect(listCalls.length).toBeGreaterThan(1);
});
