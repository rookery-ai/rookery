import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import AgentsPage from "./AgentsPage";
import { RunPanel } from "./RunPanel";
import type { Agent } from "@/lib/agents";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const stopCalls: string[] = [];
let stopStatus = 204;
let agents: Agent[] = [];

function mockFetch() {
  stopCalls.length = 0;
  stopStatus = 204;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/run/stop") && init?.method === "POST") {
        stopCalls.push(url);
        if (stopStatus === 204) return Promise.resolve(new Response(null, { status: 204 }));
        return Promise.resolve(
          jsonResponse({ error: { code: "not_running", message: "no active run" } }, stopStatus),
        );
      }
      if (url === "/api/v1/agents") return Promise.resolve(jsonResponse({ agents, draft: null }));
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function panel(running: boolean) {
  return render(
    <QueryClientProvider client={client()}>
      <RunPanel agentId="a1" agentName="Looper" liveRun={false} running={running} />
    </QueryClientProvider>,
  );
}

function list() {
  return render(
    <QueryClientProvider client={client()}>
      <MemoryRouter initialEntries={["/agents"]}>
        <Routes>
          <Route path="/agents" element={<AgentsPage />} />
          <Route path="/agents/:id" element={<div>agent page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function agent(over: Partial<Agent>): Agent {
  return {
    id: "a1",
    name: "Looper",
    description: "Loops.",
    active: true,
    created_at: "2026-07-01T00:00:00Z",
    running: false,
    ...over,
  } as Agent;
}

beforeEach(() => {
  agents = [];
  mockFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("RunPanel", () => {
  it("offers Run now when nothing is running", () => {
    panel(false);
    expect(screen.getByRole("button", { name: /run now/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^stop$/i })).toBeNull();
  });

  // A scheduled run has no stream, so Stop must not depend on one.
  it("offers Stop for a run with no live stream, and posts to the route", async () => {
    panel(true);
    expect(screen.queryByRole("button", { name: /run now/i })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /^stop$/i }));
    await waitFor(() => expect(stopCalls).toEqual(["/api/v1/agents/a1/run/stop"]));
    expect(await screen.findByRole("button", { name: /stopping/i })).toBeDisabled();
  });

  it("treats an already-finished run as harmless", async () => {
    stopStatus = 404;
    panel(true);
    await userEvent.click(screen.getByRole("button", { name: /^stop$/i }));
    await waitFor(() => expect(stopCalls).toHaveLength(1));
    expect(await screen.findByRole("button", { name: /^stop$/i })).toBeEnabled();
    expect(screen.queryByText(/no active run/i)).toBeNull();
  });

  it("shows a real failure and lets the owner retry", async () => {
    stopStatus = 500;
    panel(true);
    await userEvent.click(screen.getByRole("button", { name: /^stop$/i }));
    expect(await screen.findByText(/no active run/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^stop$/i })).toBeEnabled();
  });
});

describe("agent list", () => {
  it("shows Stop only on a running agent", async () => {
    agents = [agent({ id: "a1", name: "Looper", running: true }), agent({ id: "a2", name: "Calm" })];
    list();
    await screen.findByText("Calm");
    expect(screen.getAllByRole("button", { name: /^stop$/i })).toHaveLength(1);
  });

  it("stops the run without opening the agent", async () => {
    agents = [agent({ running: true })];
    list();
    const stop = await screen.findByRole("button", { name: /^stop$/i });
    // A button inside the card's anchor would be invalid HTML and would navigate.
    expect(stop.closest("a")).toBeNull();
    await userEvent.click(stop);
    await waitFor(() => expect(stopCalls).toEqual(["/api/v1/agents/a1/run/stop"]));
    expect(screen.queryByText("agent page")).toBeNull();
  });
});
