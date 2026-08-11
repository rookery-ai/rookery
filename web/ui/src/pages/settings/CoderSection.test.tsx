import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider, useMutation } from "@tanstack/react-query";
import { CoderSection } from "./CoderSection";
import type { CoderCatalogEntry, CoderConfig, DetectedCoder, SaveCoderInput } from "@/lib/settings";

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
  { name: "openrouter", label: "OpenRouter", base: "https://openrouter.ai/api/v1", model: "glm-5.2", docs: "https://openrouter.ai/keys", requiresKey: true, custom: false, hasKey: true, group: "hosted" },
  { name: "zai", label: "Z.AI (GLM)", base: "https://api.z.ai/v1", model: "glm-4.7", docs: "https://z.ai", requiresKey: true, custom: false, hasKey: false, group: "hosted" },
  { name: "ollama_local", label: "Ollama (Local)", base: "http://localhost:11434/v1", model: "qwen2.5-coder", docs: "https://docs.ollama.com", requiresKey: false, custom: false, hasKey: false, group: "local" },
  { name: "generic", label: "Custom (OpenAI-compatible)", base: "", model: "", docs: "", requiresKey: true, custom: true, hasKey: false, group: "hosted" },
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

function wrap(
  coder: CoderConfig | undefined,
  detected: DetectedCoder[] = DETECTED,
  catalog: CoderCatalogEntry[] = CATALOG,
  coderMode: "full" | "slim" = "full",
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <CoderSection coder={coder} detectedCoders={detected} catalog={catalog} coderMode={coderMode} />
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

// The local engine had no Model field at all, and the save payload hardcoded
// model: "" — so OpenCode, which has no default model of its own, could not be
// configured through the UI and 401'd against its hardcoded OpenRouter default.
test("Local engine: a typed model is sent with the save payload", async () => {
  const calls = mockFetch();
  wrap(LOCAL_CODER);

  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/model \(optional\)/i), "ollama-cloud/glm-5.2");
  await user.click(screen.getByRole("button", { name: /save coder/i }));

  await waitFor(() => {
    expect(calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")).toBeDefined();
  });
  const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")!;
  expect(putCall.body).toMatchObject({ kind: "local", model: "ollama-cloud/glm-5.2" });
});

// OpenCode's requirement is surfaced as help text, never as a blocking
// validation: a user relying on a host-level default must not be locked out.
test("Local engine: selecting OpenCode explains why a model is needed, without blocking save", async () => {
  const calls = mockFetch();
  wrap({ ...LOCAL_CODER, bin: "opencode" });

  expect(screen.getByText(/OpenCode has no default model/i)).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /save coder/i }));
  await waitFor(() => {
    expect(calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")).toBeDefined();
  });
});

test("API engine: providers missing a key are disabled in the select", async () => {
  mockFetch();
  wrap({ ...LOCAL_CODER, kind: "api", provider: "", model: "" });

  const user = userEvent.setup();
  await user.click(screen.getByRole("radio", { name: /^api$/i }));

  const openrouterOpt = screen.getByRole("option", { name: /openrouter/i }) as HTMLOptionElement;
  const zaiOpt = screen.getByRole("option", { name: /z\.ai/i }) as HTMLOptionElement;
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

test("shows a hint that Test exercises the last saved configuration, not unsaved form edits", () => {
  mockFetch();
  wrap(LOCAL_CODER);

  expect(screen.getByText(/tests the last saved configuration/i)).toBeInTheDocument();
});

// ── Wizard-mode props (hideTest / saveOverride / showApiKeyInput) ──────────
// These back the onboarding wizard's coder step (SetupWizard), which posts
// through /api/v1/setup instead of /api/v1/settings/coder and can't reach
// ProviderCards' /api/v1/secrets endpoint while needs_setup is still true.

test("hideTest=true hides the Test button and its result area", () => {
  mockFetch();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <CoderSection coder={LOCAL_CODER} detectedCoders={DETECTED} catalog={CATALOG} hideTest />
    </QueryClientProvider>,
  );
  expect(screen.queryByRole("button", { name: /^test$/i })).not.toBeInTheDocument();
  expect(screen.queryByText(/tests the last saved configuration/i)).not.toBeInTheDocument();
});

// Regression test for the real wizard path: NO pre-seeded `coder` prop (a
// fresh workspace has no coder configured yet), and the provider is picked
// by actually driving the <select> — not by pre-setting `provider` via the
// `coder` prop's useEffect, which would bypass the disabled-option bug
// entirely. "zai" has hasKey:false in CATALOG (no key saved yet, exactly the
// first-time-setup scenario), so this only passes if wizard mode makes every
// provider selectable regardless of hasKey (the inline key field supplies
// it instead of ProviderCards).
test("showApiKeyInput: a provider with no saved key yet is still selectable, and its save payload carries provider/model/api_key", async () => {
  const calls = mockFetch();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <CoderSection coder={undefined} detectedCoders={DETECTED} catalog={CATALOG} showApiKeyInput />
    </QueryClientProvider>,
  );

  const user = userEvent.setup();
  await user.click(screen.getByRole("radio", { name: /^api$/i }));

  const zaiOption = screen.getByRole("option", { name: /z\.ai/i }) as HTMLOptionElement;
  expect(zaiOption.disabled).toBe(false);

  await user.selectOptions(screen.getByLabelText(/^provider$/i), "zai");
  await user.type(screen.getByLabelText(/^model$/i), "glm-4.7");
  await waitFor(() => expect(screen.getByLabelText(/api key/i)).toBeInTheDocument());
  await user.type(screen.getByLabelText(/api key/i), "sk-zai-secret");
  await user.click(screen.getByRole("button", { name: /save coder/i }));

  await waitFor(() => {
    const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT");
    expect(putCall).toBeDefined();
  });
  const putCall = calls.find((c) => c.url === "/api/v1/settings/coder" && c.method === "PUT")!;
  expect(putCall.body).toEqual({
    kind: "api", bin: "", timeout_s: 120,
    provider: "zai", model: "glm-4.7", base_url: "", api_key: "sk-zai-secret",
  });
});

test("saveOverride: Save posts through the override mutation instead of /api/v1/settings/coder", async () => {
  mockFetch();
  const posted: unknown[] = [];
  function Wrapper() {
    const override = useMutation({
      mutationFn: async (input: SaveCoderInput) => {
        posted.push(input);
        return { ok: true, next_step: 4 };
      },
    });
    return (
      <CoderSection
        coder={LOCAL_CODER}
        detectedCoders={DETECTED}
        catalog={CATALOG}
        saveOverride={override}
        hideTest
      />
    );
  }
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Wrapper />
    </QueryClientProvider>,
  );

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /save coder/i }));

  await waitFor(() => expect(posted).toHaveLength(1));
  expect(posted[0]).toEqual({
    kind: "local",
    bin: "/usr/bin/claude",
    timeout_s: 120,
    provider: "",
    model: "",
    base_url: "",
    api_key: "",
  });
});

// A slim build ships no CLI coder binary, so the engine toggle must not offer
// it. The server rejects it too (rejectLocalInSlim) — this is the convenience
// half of that guard.
test("slim build hides the Local CLI engine option", () => {
  mockFetch();
  wrap(LOCAL_CODER, [], CATALOG, "slim");
  expect(screen.queryByText("Local CLI")).not.toBeInTheDocument();
  expect(screen.getByText("API")).toBeInTheDocument();
});

test("full build still offers the Local CLI engine option", () => {
  mockFetch();
  wrap(LOCAL_CODER, DETECTED, CATALOG, "full");
  expect(screen.getByText("Local CLI")).toBeInTheDocument();
});

// ─── Base URL discoverability ────────────────────────────────────────────────
//
// Overriding a self-hosted server's port was always possible and always
// persisted; it was simply undiscoverable. The field sat behind Advanced,
// started empty, and showed a generic example placeholder, so a user running
// Ollama on a non-default port had no way to learn the override existed or what
// shape to type. These tests pin the fix AND the storage semantics it must not
// change: an unmodified prefill still means "follow the registry default".

test("API engine: the provider dropdown shows human labels, not registry slugs", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  expect(
    screen.getByRole("option", { name: /ollama \(local\)/i }),
  ).toBeInTheDocument();
});

test("API engine: selecting a provider prefills its default base URL", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  await user.selectOptions(screen.getByLabelText(/provider/i), "ollama_local");
  // Local providers auto-expand Advanced, so the field is visible without a click.
  expect(screen.getByLabelText(/base url/i)).toHaveValue(
    "http://localhost:11434/v1",
  );
});

test("API engine: an unmodified prefill posts an empty base_url", async () => {
  const calls = mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  await user.selectOptions(screen.getByLabelText(/provider/i), "ollama_local");
  await user.type(screen.getByLabelText(/^model$/i), "qwen2.5-coder");
  await user.click(screen.getByRole("button", { name: /save coder/i }));
  await waitFor(() => {
    const put = calls.find((c) => c.method === "PUT");
    expect(put).toBeTruthy();
    // Storing the default explicitly would pin this workspace to today's URL if
    // the registry default ever changed. Empty means "follow the default".
    expect((put!.body as { base_url: string }).base_url).toBe("");
  });
});

test("API engine: an edited base URL is posted verbatim", async () => {
  const calls = mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  await user.selectOptions(screen.getByLabelText(/provider/i), "ollama_local");
  const field = screen.getByLabelText(/base url/i);
  await user.clear(field);
  await user.type(field, "http://192.168.1.50:12345/v1");
  await user.type(screen.getByLabelText(/^model$/i), "qwen2.5-coder");
  await user.click(screen.getByRole("button", { name: /save coder/i }));
  await waitFor(() => {
    const put = calls.find((c) => c.method === "PUT");
    expect((put!.body as { base_url: string }).base_url).toBe(
      "http://192.168.1.50:12345/v1",
    );
  });
});

test("API engine: a saved override survives a provider switch", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap({
    kind: "api",
    bin: "",
    timeout_s: 90,
    provider: "ollama_local",
    model: "qwen2.5-coder",
    base_url: "http://nas.lan:11434/v1",
    api_key_secret: "CODER_KEY_OLLAMA_LOCAL",
  });
  // A stored override must not be clobbered by the prefill on mount...
  expect(screen.getByLabelText(/base url/i)).toHaveValue(
    "http://nas.lan:11434/v1",
  );
  // ...nor by a later provider change.
  await user.selectOptions(screen.getByLabelText(/provider/i), "zai");
  expect(screen.getByLabelText(/base url/i)).toHaveValue(
    "http://nas.lan:11434/v1",
  );
});
