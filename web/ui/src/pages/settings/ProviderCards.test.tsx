import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProviderCards } from "./ProviderCards";
import type { APIProvider, CoderCatalogEntry } from "@/lib/settings";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const PROVIDERS: APIProvider[] = [
  {
    name: "openrouter",
    label: "OpenRouter",
    schema: "openai",
    model_placeholder: "glm-5.2",
    docs_url: "https://openrouter.ai/keys",
    requires_key: true,
    custom: false,
  },
  {
    name: "ollama_local",
    label: "Ollama (Local)",
    schema: "openai",
    model_placeholder: "qwen2.5-coder",
    docs_url: "https://docs.ollama.com",
    requires_key: false,
    custom: false,
  },
  {
    name: "generic",
    label: "Custom (OpenAI-compatible)",
    schema: "openai",
    model_placeholder: "",
    docs_url: "",
    requires_key: true,
    custom: true,
  },
];

function catalog(overrides: Partial<CoderCatalogEntry> = {}): CoderCatalogEntry[] {
  return [
    {
      name: "openrouter",
      label: "OpenRouter",
      base: "https://openrouter.ai/api/v1",
      model: "glm-5.2",
      docs: "https://openrouter.ai/keys",
      requiresKey: true,
      custom: false,
      hasKey: false,
      group: "hosted",
      ...overrides,
    },
  ];
}

function wrap(entries: CoderCatalogEntry[], providers: APIProvider[] = PROVIDERS) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { qc, ...render(
    <QueryClientProvider client={qc}>
      <ProviderCards catalog={entries} providers={providers} />
    </QueryClientProvider>,
  ) };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("hasKey shows the green 'Key saved' state", () => {
  wrap(catalog({ hasKey: true }));
  expect(screen.getByText("Key saved")).toBeInTheDocument();
  expect(screen.queryByText(/add key/i)).not.toBeInTheDocument();
});

test("requiresKey===false shows a muted 'No key needed' state and is not expandable", async () => {
  const entries: CoderCatalogEntry[] = [
    { name: "ollama_local", label: "Ollama (Local)", base: "http://localhost:11434/v1", model: "qwen2.5-coder", docs: "https://docs.ollama.com", requiresKey: false, custom: false, hasKey: false, group: "local" },
  ];
  wrap(entries);
  expect(screen.getByText("No key needed")).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /ollama \(local\)/i }));
  expect(screen.queryByLabelText(/api key/i)).not.toBeInTheDocument();
});

test("no key yet shows '+ Add key' and expanding reveals a password input", async () => {
  wrap(catalog({ hasKey: false }));
  expect(screen.getByText(/add key/i)).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /openrouter/i }));

  const input = screen.getByLabelText("OpenRouter API key") as HTMLInputElement;
  expect(input.type).toBe("password");
});

test("saving posts the CODER_KEY_<PROVIDER> convention name and invalidates settings", async () => {
  const calls: { url: string; method: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });
      if (url === "/api/v1/secrets" && method === "POST") {
        return Promise.resolve(jsonResponse({ ok: true }, 201));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const { qc } = wrap(catalog({ hasKey: false }));
  const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /openrouter/i }));
  await user.type(screen.getByLabelText("OpenRouter API key"), "sk-abc123");
  await user.click(screen.getByRole("button", { name: /save key/i }));

  await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());
  // The Check icon already conveys success — no duplicate literal checkmark.
  expect(screen.queryByText("Saved ✓")).not.toBeInTheDocument();

  const postCall = calls.find((c) => c.url === "/api/v1/secrets" && c.method === "POST");
  expect(postCall).toBeDefined();
  expect(postCall!.body).toEqual({ name: "CODER_KEY_OPENROUTER", value: "sk-abc123" });
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["settings"] });

  // Form collapses immediately on success.
  expect(screen.queryByLabelText("OpenRouter API key")).not.toBeInTheDocument();
});

test("hasKey===true shows an 'already set' hint when expanded", async () => {
  wrap(catalog({ hasKey: true }));
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /openrouter/i }));
  expect(screen.getByText(/already set/i)).toBeInTheDocument();
});

test("custom provider shows a base-URL note when expanded", async () => {
  wrap(
    catalog({ name: "generic", label: "Custom (OpenAI-compatible)", custom: true, docs: "", hasKey: false }),
    PROVIDERS,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /custom \(openai-compatible\)/i }));
  expect(screen.getByText(/base url/i)).toBeInTheDocument();
});

test("never renders the saved key value anywhere", () => {
  wrap(catalog({ hasKey: true }));
  expect(screen.queryByText(/sk-/)).not.toBeInTheDocument();
});

// Thirty-one flat cards is a wall, and mixing the tiers makes the local tier's
// "No key needed" read as an inconsistency rather than a property of the group.
test("cards are split into hosted and local sections", () => {
  const entries: CoderCatalogEntry[] = [
    ...catalog(),
    { name: "ollama_local", label: "Ollama (Local)", base: "http://localhost:11434/v1", model: "qwen2.5-coder", docs: "https://docs.ollama.com", requiresKey: false, custom: false, hasKey: false, group: "local" },
  ];
  wrap(entries);
  expect(screen.getByRole("heading", { name: /^hosted$/i })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: /local & self-hosted/i })).toBeInTheDocument();
});

// A card filed under the wrong heading is recoverable; one that vanishes is not.
test("an unrecognised group falls back to the hosted section", () => {
  wrap(catalog({ group: "" }));
  expect(screen.getByRole("heading", { name: /^hosted$/i })).toBeInTheDocument();
  expect(screen.getByText("OpenRouter")).toBeInTheDocument();
});
