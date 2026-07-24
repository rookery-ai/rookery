import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import AgentNewPage from "./AgentNewPage";
import { AGENT_TEMPLATES } from "./templates";

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

test("picking a template fills the description", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  fireEvent.click(screen.getByRole("button", { name: /daily digest/i }));

  // toHaveValue doesn't support asymmetric matchers (it compares literal
  // values) — read the live value and match against it directly instead.
  const field = screen.getByLabelText(/what should it do/i) as HTMLTextAreaElement;
  expect(field.value).toMatch(/summar/i);
});

test("the filled description stays editable", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  fireEvent.click(screen.getByRole("button", { name: /daily digest/i }));
  const field = screen.getByLabelText(/what should it do/i);
  fireEvent.change(field, { target: { value: "Watch my calendar instead" } });

  expect(field).toHaveValue("Watch my calendar instead");
});

test("start from scratch leaves the field blank", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  fireEvent.click(screen.getByRole("button", { name: /from scratch/i }));

  expect(screen.getByLabelText(/what should it do/i)).toHaveValue("");
});

test("no template text mentions implementation", () => {
  const banned = /\b(script|python|cron|file|json|webhook|endpoint|api key)\b/i;
  for (const t of AGENT_TEMPLATES) {
    expect(t.description).not.toMatch(banned);
  }
});

test("all six templates render as selectable cards with a label and blurb", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  expect(AGENT_TEMPLATES).toHaveLength(6);
  for (const t of AGENT_TEMPLATES) {
    const card = screen.getByRole("button", { name: new RegExp(t.label, "i") });
    expect(card).toHaveTextContent(t.blurb);
  }
});

test("selecting a template marks it visibly active, and switching moves the mark", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  const digest = screen.getByRole("button", { name: /daily digest/i });
  const watch = screen.getByRole("button", { name: /watch for changes/i });

  fireEvent.click(digest);
  expect(digest).toHaveAttribute("aria-pressed", "true");
  expect(watch).toHaveAttribute("aria-pressed", "false");

  fireEvent.click(watch);
  expect(digest).toHaveAttribute("aria-pressed", "false");
  expect(watch).toHaveAttribute("aria-pressed", "true");
});

test("editing the filled-in text by hand deactivates the template mark", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  const digest = screen.getByRole("button", { name: /daily digest/i });
  fireEvent.click(digest);
  expect(digest).toHaveAttribute("aria-pressed", "true");

  fireEvent.change(screen.getByLabelText(/what should it do/i), {
    target: { value: "something totally different" },
  });

  expect(digest).toHaveAttribute("aria-pressed", "false");
});

test("switching templates after hand-typed text asks for confirmation before replacing it", async () => {
  const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
  wrap();
  await screen.findByLabelText(/what should it do/i);

  const field = screen.getByLabelText(/what should it do/i);
  fireEvent.change(field, { target: { value: "my own hand-typed brief" } });

  fireEvent.click(screen.getByRole("button", { name: /watch for changes/i }));
  expect(confirmSpy).toHaveBeenCalled();
  // Declined — the hand-typed text survives.
  expect(field).toHaveValue("my own hand-typed brief");

  confirmSpy.mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: /watch for changes/i }));
  expect(field).toHaveValue(
    AGENT_TEMPLATES.find((t) => t.id === "watch-for-changes")!.description,
  );

  confirmSpy.mockRestore();
});

test("continuing with a filled-in template SENDS it to the designer as the opening message", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "Test Agent" } });
  fireEvent.click(screen.getByRole("button", { name: /daily digest/i }));
  fireEvent.click(screen.getByRole("button", { name: /continue/i }));

  // The description is SENT as the first design message (auto-send on Continue),
  // not merely pre-filled into the composer — with the agent name attached so
  // the backend opens a session.
  await waitFor(() => {
    const designPost = vi
      .mocked(fetch)
      .mock.calls.find(
        ([url, init]) =>
          String(url) === "/api/v1/agents/design" && (init?.method ?? "GET") === "POST",
      );
    expect(designPost).toBeTruthy();
    const body = JSON.parse(String((designPost![1] as RequestInit).body));
    expect(body.message).toMatch(/summary/i);
    expect(body.name).toBe("Test Agent");
  });
});
