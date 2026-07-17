import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RunPanel } from "./RunPanel";

// Minimal EventSource stub — mirrors pages/agents/detail.test.tsx.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  url: string;
  readyState = FakeEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners: Record<string, Array<() => void>> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockRun(handler: () => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/agents/a1/run" && method === "POST") {
        return Promise.resolve(handler());
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <RunPanel agentId="a1" agentName="Inbox Triager" liveRun={false} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test("already_running: shows an informational note (not an error banner) and attaches the SSE", async () => {
  mockRun(() => jsonResponse({ status: "already_running" }, 202));
  wrap();

  await userEvent.click(screen.getByRole("button", { name: /run now/i }));

  expect(await screen.findByText("A run is already in progress")).toBeInTheDocument();
  expect(screen.queryByText(/something went wrong/i)).not.toBeInTheDocument();

  // The note must render outside the red error-banner styling.
  const note = screen.getByText("A run is already in progress");
  expect(note.className).not.toMatch(/text-danger/);

  // Still attaches to the live run's SSE stream.
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  expect(FakeEventSource.instances[0]!.url).toBe("/api/v1/agents/a1/run/progress");
});

test("started: does not show the already-running note", async () => {
  mockRun(() => jsonResponse({ status: "started" }, 202));
  wrap();

  await userEvent.click(screen.getByRole("button", { name: /run now/i }));

  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  expect(screen.queryByText("A run is already in progress")).not.toBeInTheDocument();
});

test("503 not_configured: shows the error banner with the server's message", async () => {
  mockRun(() =>
    jsonResponse({ error: { code: "not_configured", message: "agent runner is not configured" } }, 503),
  );
  wrap();

  await userEvent.click(screen.getByRole("button", { name: /run now/i }));

  const banner = await screen.findByText("agent runner is not configured");
  expect(banner).toBeInTheDocument();
  expect(banner.closest("div")?.className).toMatch(/text-danger/);
  expect(FakeEventSource.instances).toHaveLength(0);
});

test("generic error: shows a fallback message when the response has no error envelope", async () => {
  mockRun(() => new Response("internal server error", { status: 500 }));
  wrap();

  await userEvent.click(screen.getByRole("button", { name: /run now/i }));

  expect(await screen.findByText(/internal server error|something went wrong/i)).toBeInTheDocument();
  expect(FakeEventSource.instances).toHaveLength(0);
});
