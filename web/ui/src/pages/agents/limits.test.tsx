import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { AppShell } from "@/components/shell/AppShell";
import AgentDetailPage from "./AgentDetailPage";
import { RunStatusChip } from "./StatusChip";
import type { AgentDetail, AgentLimits } from "@/lib/agents";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

function limits(over: Partial<AgentLimits> = {}): AgentLimits {
  return {
    run: { tokens: 50000, turns: 20, usd: 1.5 },
    day: { tokens: 0, usd: 0 },
    month: { tokens: 1000000, usd: 30 },
    enforced: true,
    dollars_supported: true,
    spend: {
      day: { tokens: 12300, usd: 0.42, usd_reported: true },
      month: { tokens: 99000, usd: 3, usd_reported: false },
    },
    paused_until: null,
    pause_reason: "",
    ...over,
  };
}

function baseDetail(over: Partial<AgentDetail> = {}): AgentDetail {
  return {
    agent: { id: "a1", name: "Budget Bot", description: "", active: true, created_at: "2026-07-01T00:00:00Z", running: false },
    schedule: null,
    runs: [],
    agent_md: "# Agent",
    state: "{}",
    logs: [],
    last_log: "",
    attached_skills: [],
    core_skills: [],
    all_skills: [],
    workspace_connections: [],
    attached_connection_ids: [],
    missing_secrets: [],
    running: false,
    live_run: false,
    limits: limits(),
    ...over,
  };
}

let detail: AgentDetail;
let puts: unknown[];

beforeEach(() => {
  puts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION));
      if (url === "/api/v1/agents/a1" && method === "GET") return Promise.resolve(jsonResponse(detail));
      if (url === "/api/v1/agents/a1/limits" && method === "PUT") {
        puts.push(JSON.parse(String(init?.body)));
        return Promise.resolve(jsonResponse(detail.limits));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/agents/a1"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/agents/:id" element={<AgentDetailPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("renders the inputs from the detail and the spend lines", async () => {
  detail = baseDetail();
  wrap();
  expect(await screen.findByRole("heading", { name: "Limits" })).toBeInTheDocument();
  expect(screen.getByLabelText("Per run tokens")).toHaveValue(50000);
  expect(screen.getByLabelText("Per run turns")).toHaveValue(20);
  expect(screen.getByLabelText("Per month dollars")).toHaveValue(30);
  expect(screen.getByLabelText("Per day tokens")).toHaveValue(null);
  expect(screen.getByText("12,300 tokens · $0.42 today")).toBeInTheDocument();
  // Cost not reported for the month: no dollar figure at all.
  expect(screen.getByText("99,000 tokens this month")).toBeInTheDocument();
});

test("clearing a field saves 0 and sends the whole object", async () => {
  detail = baseDetail();
  wrap();
  const tokens = await screen.findByLabelText("Per run tokens");
  await userEvent.clear(tokens);
  await userEvent.click(screen.getByRole("button", { name: "Save limits" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toEqual({
    run: { tokens: 0, turns: 20, usd: 1.5 },
    day: { tokens: 0, usd: 0 },
    month: { tokens: 1000000, usd: 30 },
  });
});

test("hides dollar inputs when the provider cannot report cost", async () => {
  detail = baseDetail({ limits: limits({ dollars_supported: false }) });
  wrap();
  await screen.findByRole("heading", { name: "Limits" });
  expect(screen.queryByLabelText("Per run dollars")).not.toBeInTheDocument();
  expect(
    screen.getByText("Dollar limits need a provider that reports cost (OpenRouter, xAI)."),
  ).toBeInTheDocument();
});

test("disables the whole card when limits are not enforced", async () => {
  detail = baseDetail({ limits: limits({ enforced: false }) });
  wrap();
  await screen.findByRole("heading", { name: "Limits" });
  expect(screen.getByText(/Limits apply only when this agent runs on an API coder/)).toBeInTheDocument();
  expect(screen.getByLabelText("Per run tokens")).toBeDisabled();
  expect(screen.getByRole("button", { name: "Save limits" })).toBeDisabled();
});

test("shows the paused banner and disables Run now", async () => {
  detail = baseDetail({
    limits: limits({ paused_until: "2026-12-01T00:00:00Z", pause_reason: "monthly limit of $30.00 reached" }),
  });
  wrap();
  expect(await screen.findByText(/Paused — monthly limit of \$30\.00 reached\. Resumes/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Change limits" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /run now/i })).toBeDisabled();
});

test("no banner and Run now enabled when not paused", async () => {
  detail = baseDetail();
  wrap();
  await screen.findByRole("heading", { name: "Limits" });
  expect(screen.queryByText(/^Paused —/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /run now/i })).toBeEnabled();
});

test("RunStatusChip renders Limited with the reason as tooltip", () => {
  render(<RunStatusChip status="limited" reason="per-run limit of 50,000 tokens reached" />);
  const chip = screen.getByText("Limited");
  expect(chip).toHaveAttribute("title", "per-run limit of 50,000 tokens reached");
});
