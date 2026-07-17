import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import {
  useSettings,
  useSaveProfile,
  useSaveWorkspaceMeta,
  useSaveCoder,
  useTestCoder,
  useChangeMasterPassword,
} from "./settings";
import { useSession } from "./session";

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

const SETTINGS_FIXTURE = {
  profile: {
    display_name: "Ilija",
    email: "ilija@example.com",
    location: "Skopje",
    timezone: "Europe/Skopje",
    tone: "direct",
    language: "English",
    notes: "",
  },
  workspace: { name: "Home Server", about: "Personal assistant" },
  coder: {
    kind: "api",
    bin: "",
    timeout_s: 120,
    provider: "openrouter",
    model: "glm-5.2",
    base_url: "",
    api_key_secret: "CODER_KEY_OPENROUTER",
  },
  detected_coders: [{ name: "claude", bin: "claude", backend_type: "claude" }],
  api_providers: [
    {
      name: "openrouter",
      label: "OpenRouter",
      schema: "openai",
      model_placeholder: "glm-5.2",
      docs_url: "https://openrouter.ai/docs",
      requires_key: true,
      custom: false,
    },
  ],
  coder_catalog: [
    {
      name: "openrouter",
      base: "https://openrouter.ai/api/v1",
      model: "glm-5.2",
      docs: "https://openrouter.ai/docs",
      requiresKey: true,
      custom: false,
      hasKey: true,
    },
  ],
  secret_names: ["CODER_KEY_OPENROUTER"],
};

test("useSettings fetches /api/v1/settings", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(SETTINGS_FIXTURE)));
  const { result } = renderHook(() => useSettings(), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/v1/settings");
  expect(result.current.data?.profile.display_name).toBe("Ilija");
  expect(result.current.data?.coder_catalog[0].hasKey).toBe(true);
});

test("useSaveProfile PUTs the full profile body to /api/v1/settings/profile", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useSaveProfile(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync(SETTINGS_FIXTURE.profile);
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/settings/profile");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual(SETTINGS_FIXTURE.profile);
});

test("useSaveWorkspaceMeta PUTs {name,about} and invalidates settings + session", async () => {
  let settingsFetches = 0;
  let sessionFetches = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/settings" && method === "GET") {
        settingsFetches++;
        return Promise.resolve(jsonResponse(SETTINGS_FIXTURE));
      }
      if (url === "/api/v1/auth/session" && method === "GET") {
        sessionFetches++;
        return Promise.resolve(jsonResponse({ authenticated: true }));
      }
      if (url === "/api/v1/settings/workspace" && method === "PUT") {
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);

  const settingsHook = renderHook(() => useSettings(), { wrapper: Wrapper });
  await waitFor(() => expect(settingsHook.result.current.isSuccess).toBe(true));
  // Mount an active session observer so invalidation has something to refetch.
  const sessionHook = renderHook(() => useSession(), { wrapper: Wrapper });
  await waitFor(() => expect(sessionHook.result.current.isSuccess).toBe(true));

  const saveHook = renderHook(() => useSaveWorkspaceMeta(), { wrapper: Wrapper });
  await act(async () => {
    await saveHook.result.current.mutateAsync({ name: "New Name", about: "New about" });
  });

  const [url, init] = vi.mocked(fetch).mock.calls.find(
    (c) => String(c[0]) === "/api/v1/settings/workspace",
  )!;
  expect(url).toBe("/api/v1/settings/workspace");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    name: "New Name",
    about: "New about",
  });

  await waitFor(() => expect(settingsFetches).toBeGreaterThan(1));
  await waitFor(() => expect(sessionFetches).toBeGreaterThan(1));
});

test("useSaveCoder PUTs kind/bin/timeout_s/provider/model/base_url/api_key", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useSaveCoder(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({
      kind: "api",
      bin: "",
      timeout_s: 120,
      provider: "openrouter",
      model: "glm-5.2",
      base_url: "",
      api_key: "sk-test",
    });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/settings/coder");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    kind: "api",
    bin: "",
    timeout_s: 120,
    provider: "openrouter",
    model: "glm-5.2",
    base_url: "",
    api_key: "sk-test",
  });
});

test("useTestCoder POSTs /api/v1/settings/coder/test with no body", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ ok: true, reply: "Hello from your coder" })),
  );
  const { result } = renderHook(() => useTestCoder(), { wrapper: wrapper() });
  let response: { ok: boolean; reply?: string } | undefined;
  await act(async () => {
    response = await result.current.mutateAsync();
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/settings/coder/test");
  expect((init as RequestInit).method).toBe("POST");
  expect((init as RequestInit).body).toBeUndefined();
  expect(response?.reply).toBe("Hello from your coder");
});

test("useChangeMasterPassword PUTs current/new_password/confirm", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ ok: true })));
  const { result } = renderHook(() => useChangeMasterPassword(), { wrapper: wrapper() });
  await act(async () => {
    await result.current.mutateAsync({ current: "old-pw", new_password: "new-pw-123", confirm: "new-pw-123" });
  });
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/v1/settings/master-password");
  expect((init as RequestInit).method).toBe("PUT");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({
    current: "old-pw",
    new_password: "new-pw-123",
    confirm: "new-pw-123",
  });
});
