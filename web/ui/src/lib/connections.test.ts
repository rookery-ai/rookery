import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import {
  useConnectors,
  useSaveConnector,
  useDeleteConnector,
  useTestConnector,
  useServices,
  useSaveProviderCreds,
  useConnectService,
  useConnectAPIKey,
  useDeleteServiceConnection,
  groupByCategory,
  CATEGORY_ORDER,
  type ServiceProvider,
} from "./connections";

function providerFixture(
  name: string,
  category: string,
): ServiceProvider {
  return {
    name,
    label: name,
    category,
    kind: "oauth",
    setup_url: "",
    setup_steps: [],
    has_creds: false,
    connect_inputs: [],
    connections: [],
  };
}

describe("groupByCategory", () => {
  it("orders groups by CATEGORY_ORDER, not by input or alphabet", () => {
    const grouped = groupByCategory([
      providerFixture("stripe", "Commerce"),
      providerFixture("gmail", "Google"),
      providerFixture("asana", "Productivity"),
    ]);
    expect(grouped.map(([c]) => c)).toEqual([
      "Google",
      "Productivity",
      "Commerce",
    ]);
  });

  it("preserves incoming order within a group", () => {
    const grouped = groupByCategory([
      providerFixture("google_sheets", "Google"),
      providerFixture("gmail", "Google"),
    ]);
    expect(grouped[0][1].map((p) => p.name)).toEqual([
      "google_sheets",
      "gmail",
    ]);
  });

  // A provider must never vanish from a page whose whole purpose is showing
  // every available integration — an unknown or missing category falls to Other.
  it("routes unknown and empty categories to Other", () => {
    const grouped = groupByCategory([
      providerFixture("mystery", "Nonsense"),
      providerFixture("blank", ""),
    ]);
    expect(grouped).toHaveLength(1);
    expect(grouped[0][0]).toBe("Other");
    expect(grouped[0][1].map((p) => p.name)).toEqual(["mystery", "blank"]);
  });

  it("drops empty categories so no blank heading renders", () => {
    const grouped = groupByCategory([providerFixture("gmail", "Google")]);
    expect(grouped.map(([c]) => c)).toEqual(["Google"]);
    expect(grouped.length).toBeLessThan(CATEGORY_ORDER.length);
  });

  it("loses no providers", () => {
    const input = [
      providerFixture("a", "Google"),
      providerFixture("b", "Commerce"),
      providerFixture("c", "Nonsense"),
      providerFixture("d", ""),
    ];
    const total = groupByCategory(input).reduce((n, [, ps]) => n + ps.length, 0);
    expect(total).toBe(input.length);
  });

  it("returns nothing for an empty list", () => {
    expect(groupByCategory([])).toEqual([]);
  });
});

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

// ── connectors (chat platforms) ─────────────────────────────────────────────

test("useConnectors fetches the platform list", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse({
        platforms: [
          {
            platform: "telegram",
            label: "Telegram",
            blurb: "",
            setup_steps: [],
            fields: [{ name: "bot_token", label: "Bot token", secret: true }],
            connected: true,
            identity: "@my_bot",
          },
        ],
      }),
    ),
  );
  const { result } = renderHook(() => useConnectors(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.platforms).toHaveLength(1));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/connectors");
});

test("useSaveConnector POSTs platform + values and invalidates connectors", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ ok: true, identity: "@my_bot" })),
  );
  const { result } = renderHook(() => useSaveConnector(), { wrapper: wrapper() });
  let response: { ok: boolean; identity?: string } | undefined;
  await act(async () => {
    response = await result.current.mutateAsync({
      platform: "telegram",
      values: { bot_token: "abc123" },
    });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/connectors");
  expect((init as RequestInit).method).toBe("POST");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    platform: "telegram",
    values: { bot_token: "abc123" },
  });
  expect(response?.identity).toBe("@my_bot");
});

test("useDeleteConnector DELETEs /api/v1/connectors/:platform", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useDeleteConnector(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync("telegram");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/connectors/telegram");
  expect((init as RequestInit).method).toBe("DELETE");
});

test("useTestConnector POSTs to /api/v1/connectors/:platform/test", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ ok: true, identity: "@my_bot" })),
  );
  const { result } = renderHook(() => useTestConnector(), { wrapper: wrapper() });
  let response: { ok: boolean } | undefined;
  await act(async () => {
    response = await result.current.mutateAsync("telegram");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/connectors/telegram/test");
  expect((init as RequestInit).method).toBe("POST");
  expect(response?.ok).toBe(true);
});

test("useDeleteConnector invalidates the connectors list query", async () => {
  let platforms = [
    { platform: "telegram", label: "Telegram", blurb: "", setup_steps: [], fields: [], connected: true, identity: "" },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/connectors" && method === "GET") {
        return Promise.resolve(jsonResponse({ platforms }));
      }
      if (url === "/api/v1/connectors/telegram" && method === "DELETE") {
        platforms = [];
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const list = renderHook(() => useConnectors(), { wrapper: Wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  const del = renderHook(() => useDeleteConnector(), { wrapper: Wrapper });
  await act(async () => {
    await del.result.current.mutateAsync("telegram");
  });

  await waitFor(() => expect(list.result.current.isFetching).toBe(false));
  const listCalls = vi.mocked(fetch).mock.calls.filter((c) => String(c[0]) === "/api/v1/connectors");
  expect(listCalls.length).toBeGreaterThan(1);
});

// ── services (self-managed OAuth / API-key connectors) ──────────────────────

test("useServices fetches the provider list", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse({
        providers: [
          {
            name: "github",
            label: "GitHub",
            kind: "oauth",
            setup_url: "",
            setup_steps: [],
            has_creds: true,
            connect_inputs: [],
            connections: [],
          },
        ],
      }),
    ),
  );
  const { result } = renderHook(() => useServices(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.providers).toHaveLength(1));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/services");
});

test("useSaveProviderCreds POSTs client_id + client_secret", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useSaveProviderCreds(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({
      provider: "github",
      clientId: "id123",
      clientSecret: "secret456",
    });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/services/github/creds");
  expect((init as RequestInit).method).toBe("POST");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    client_id: "id123",
    client_secret: "secret456",
  });
});

test("useConnectService POSTs label and resolves redirect_url without navigating", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ redirect_url: "https://github.com/login/oauth/authorize?x=1" })),
  );
  const originalLocation = window.location.href;
  const { result } = renderHook(() => useConnectService(), { wrapper: wrapper() });
  let response: { redirect_url: string } | undefined;
  await act(async () => {
    response = await result.current.mutateAsync({ provider: "github", label: "work" });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/services/github/connect");
  expect((init as RequestInit).method).toBe("POST");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ label: "work" });
  expect(response?.redirect_url).toBe("https://github.com/login/oauth/authorize?x=1");
  // The hook must not navigate itself — only return the URL for the caller.
  expect(window.location.href).toBe(originalLocation);
});

test("useConnectAPIKey POSTs key, label, and inputs", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useConnectAPIKey(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({
      provider: "shopify",
      key: "shpat_abc",
      label: "store",
      inputs: { shop: "my-store" },
    });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/services/shopify/apikey");
  expect((init as RequestInit).method).toBe("POST");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    key: "shpat_abc",
    label: "store",
    inputs: { shop: "my-store" },
  });
});

test("useDeleteServiceConnection DELETEs /api/v1/services/:id", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useDeleteServiceConnection(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync("conn-1");
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/services/conn-1");
  expect((init as RequestInit).method).toBe("DELETE");
});

test("useSaveProviderCreds invalidates the services list query", async () => {
  let providers = [
    { name: "github", label: "GitHub", kind: "oauth", setup_url: "", setup_steps: [], has_creds: false, connect_inputs: [], connections: [] },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/services" && method === "GET") {
        return Promise.resolve(jsonResponse({ providers }));
      }
      if (url === "/api/v1/services/github/creds" && method === "POST") {
        providers = [{ ...providers[0], has_creds: true }];
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const list = renderHook(() => useServices(), { wrapper: Wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  const creds = renderHook(() => useSaveProviderCreds(), { wrapper: Wrapper });
  await act(async () => {
    await creds.result.current.mutateAsync({ provider: "github", clientId: "a", clientSecret: "b" });
  });

  await waitFor(() => expect(list.result.current.isFetching).toBe(false));
  const listCalls = vi.mocked(fetch).mock.calls.filter((c) => String(c[0]) === "/api/v1/services");
  expect(listCalls.length).toBeGreaterThan(1);
});
