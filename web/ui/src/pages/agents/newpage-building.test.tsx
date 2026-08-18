import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import AgentNewPage from "./AgentNewPage";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} } as never);
});
afterEach(() => vi.unstubAllGlobals());

// The design session is a per-workspace SINGLETON, so opening New Agent in a second
// tab while a build runs adopts the in-flight session rather than starting fresh.
// Presenting an apparently-blank form is what made that read as broken: the user
// filled it in and nothing they typed could start anything.
test("New Agent says a build is already running instead of offering a fresh form", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/design/state")) {
        return Promise.resolve(jsonResponse({ active: true, generating: true, name: "drive checker" }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  // AgentNewPage's own /api/v1/agents lookup (useAgents) goes through
  // react-query, same as every other test in this directory (see
  // agents.test.tsx's `wrap` helper) — the brief's bare MemoryRouter render
  // throws "No QueryClient set" before ever reaching the assertions.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <AgentNewPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );

  await waitFor(() => {
    expect(screen.getByText(/already building/i)).toBeInTheDocument();
  });
  expect(screen.getByRole("button", { name: /open|view/i })).toBeInTheDocument();
});
