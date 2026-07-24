import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import AgentNewPage from "./AgentNewPage";
import { AGENT_TEMPLATES, SCRATCH_TEMPLATE_ID, templateMatches } from "./templates";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/agents" && method === "GET") {
        return Promise.resolve(jsonResponse({ agents: [], draft: null }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/agents/new"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/agents/new" element={<AgentNewPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockFetch();
});

async function openGallery() {
  await screen.findByLabelText(/what should it do/i);
  fireEvent.click(screen.getByRole("button", { name: /view all templates/i }));
  return screen.findByRole("dialog");
}

test("templateMatches searches label, category, keywords AND the description", () => {
  const digest = AGENT_TEMPLATES.find((t) => t.id === "daily-digest")!;

  expect(templateMatches(digest, "morning")).toBe(true); // label
  expect(templateMatches(digest, "Email & comms")).toBe(true); // category
  expect(templateMatches(digest, "gmail")).toBe(true); // keyword (not in prose)
  expect(templateMatches(digest, "newsletters")).toBe(true); // description only
  expect(templateMatches(digest, "")).toBe(true); // empty query matches all
  expect(templateMatches(digest, "kubernetes")).toBe(false);

  // Multi-word queries narrow (every term must appear), rather than being
  // treated as one literal substring.
  expect(templateMatches(digest, "email newsletters")).toBe(true);
  expect(templateMatches(digest, "email kubernetes")).toBe(false);
});

test("View all templates opens a gallery listing the non-featured templates too", async () => {
  wrap();
  await openGallery();

  // A template that is deliberately NOT on the start screen is reachable here.
  const hidden = AGENT_TEMPLATES.find((t) => !t.featured)!;
  expect(await screen.findByRole("button", { name: new RegExp(hidden.label, "i") })).toBeInTheDocument();

  // The escape hatch is not something you browse a gallery for.
  const scratch = AGENT_TEMPLATES.find((t) => t.id === SCRATCH_TEMPLATE_ID)!;
  const dialog = screen.getByRole("dialog");
  expect(dialog).not.toHaveTextContent(scratch.label);
});

test("searching filters by a word that appears only in a description", async () => {
  const user = userEvent.setup();
  wrap();
  await openGallery();

  const search = screen.getByLabelText(/search templates/i);
  await user.type(search, "newsletters");

  // "newsletters" appears in the morning-digest brief but not in its label.
  expect(screen.getByRole("button", { name: /morning email digest/i })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /uptime check/i })).not.toBeInTheDocument();
});

test("searching filters by context (a keyword that isn't in the prose)", async () => {
  const user = userEvent.setup();
  wrap();
  await openGallery();

  await user.type(screen.getByLabelText(/search templates/i), "downtime");

  // "downtime" is a keyword on the uptime template, not a word in its brief.
  expect(screen.getByRole("button", { name: /uptime check/i })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /morning email digest/i })).not.toBeInTheDocument();
});

test("a query matching nothing shows the empty state", async () => {
  const user = userEvent.setup();
  wrap();
  await openGallery();

  await user.type(screen.getByLabelText(/search templates/i), "zzzznotathing");

  expect(await screen.findByText(/no templates match/i)).toBeInTheDocument();
});

test("selecting from the gallery fills the description and closes the modal", async () => {
  const user = userEvent.setup();
  wrap();
  await openGallery();

  await user.type(screen.getByLabelText(/search templates/i), "downtime");
  fireEvent.click(screen.getByRole("button", { name: /uptime check/i }));

  const uptime = AGENT_TEMPLATES.find((t) => t.id === "uptime-check")!;
  const field = screen.getByLabelText(/what should it do/i) as HTMLTextAreaElement;
  expect(field.value).toBe(uptime.description);

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
});
