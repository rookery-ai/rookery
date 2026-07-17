import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CoderSection } from "./CoderSection";
import type { CoderCatalogEntry, CoderConfig, DetectedCoder } from "@/lib/settings";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const LOCAL_CODER: CoderConfig = {
  kind: "local",
  bin: "/usr/bin/claude",
  timeout_s: 120,
  provider: "",
  model: "",
  base_url: "",
  api_key_secret: "",
};

const DETECTED: DetectedCoder[] = [
  { name: "Claude Code", bin: "/usr/bin/claude", backend_type: "claude" },
];

const CATALOG: CoderCatalogEntry[] = [
  { name: "openrouter", base: "https://openrouter.ai/api/v1", model: "glm-5.2", docs: "https://openrouter.ai/keys", requiresKey: true, custom: false, hasKey: true },
  { name: "zai", base: "https://api.z.ai/v1", model: "glm-4.7", docs: "https://z.ai", requiresKey: true, custom: false, hasKey: false },
  { name: "generic", base: "", model: "", docs: "", requiresKey: true, custom: true, hasKey: false },
];

type Handlers = {
  save?: (body: unknown) => Response;
  test?: () => Response;
};

function mockFetch(handlers: Handlers = {}) {
  const calls: { url: string; method: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });
      if (url === "/api/v1/settings/coder" && method === "PUT") {
        return Promise.resolve(handlers.save ? handlers.save(body) : jsonResponse({ ok: true }));
      }
      if (url === "/api/v1/settings/coder/test" && method === "POST") {
        return Promise.resolve(handlers.test ? handlers.test() : jsonResponse({ ok: true, reply: "pong" }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

function wrap(coder: CoderConfig | undefined, detected: DetectedCoder[] = DETECTED, catalog: CoderCatalogEntry[] = CATALOG) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <CoderSection coder={coder} detectedCoders={detected} catalog={catalog} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("shows the current config summary line", () => {
  mockFetch();
  wrap(LOCAL_CODER);
  expect(screen.getByText(/local cli · \/usr\/bin\/claude/i)).toBeInTheDocument();
});

test("Local engine: empty detected list shows a note instead of a select", () => {
  mockFetch();
  wrap(LOCAL_CODER, []);
  expect(screen.getByText(/no coder clis found on the server/i)).toBeInTheDocument();
});

test("Local engine save posts kind/bin/timeout_s with empty provider/model/base_url", async () => {
  const calls = mockFetch();
  wrap(LOCAL_CODER);

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /save coder/i }));

  await waitFor(() => {
    const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT");
    expect(putCall).toBeDefined();
  });
  const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")!;
  expect(putCall.body).toEqual({
    kind: "local",
    bin: "/usr/bin/claude",
    timeout_s: 120,
    provider: "",
    model: "",
    base_url: "",
    api_key: "",
  });
});

test("API engine: providers missing a key are disabled in the select", async () => {
  mockFetch();
  wrap({ ...LOCAL_CODER, kind: "api", provider: "", model: "" });

  const user = userEvent.setup();
  await user.click(screen.getByRole("radio", { name: /^api$/i }));

  const openrouterOpt = screen.getByRole("option", { name: /openrouter/i }) as HTMLOptionElement;
  const zaiOpt = screen.getByRole("option", { name: /zai/i }) as HTMLOptionElement;
  expect(openrouterOpt.disabled).toBe(false);
  expect(zaiOpt.disabled).toBe(true);
});

test("API engine: custom provider without a base URL blocks save with a client-side error", async () => {
  const calls = mockFetch();
  wrap({ ...LOCAL_CODER, kind: "api", provider: "generic", model: "gpt-4o-mini" });

  const user = userEvent.setup();
  await waitFor(() => expect((screen.getByLabelText(/provider/i) as HTMLSelectElement).value).toBe("generic"));
  await user.click(screen.getByRole("button", { name: /save coder/i }));

  expect(await screen.findByText(/base url is required/i)).toBeInTheDocument();
  expect(calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")).toBeUndefined();
});

test("API engine save posts kind/provider/model/base_url/timeout_s with no api_key field", async () => {
  const calls = mockFetch();
  wrap({ kind: "api", bin: "", timeout_s: 90, provider: "openrouter", model: "glm-5.2", base_url: "", api_key_secret: "CODER_KEY_OPENROUTER" });

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /save coder/i }));

  await waitFor(() => {
    const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT");
    expect(putCall).toBeDefined();
  });
  const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")!;
  expect(putCall.body).toEqual({
    kind: "api",
    bin: "",
    timeout_s: 90,
    provider: "openrouter",
    model: "glm-5.2",
    base_url: "",
    api_key: "",
  });
});

test("Test button: success shows the green reply", async () => {
  mockFetch({ test: () => jsonResponse({ ok: true, reply: "Hello from your coder" }) });
  wrap(LOCAL_CODER);

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /^test$/i }));

  expect(await screen.findByText(/hello from your coder/i)).toBeInTheDocument();
});

test("Test button: failure shows a red error", async () => {
  mockFetch({ test: () => jsonResponse({ ok: false, error: "connection refused" }) });
  wrap(LOCAL_CODER);

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /^test$/i }));

  expect(await screen.findByText(/connection refused/i)).toBeInTheDocument();
});
