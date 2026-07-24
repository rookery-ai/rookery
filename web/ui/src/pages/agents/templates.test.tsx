import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import AgentNewPage from "./AgentNewPage";
import { AGENT_TEMPLATES, featuredTemplates, SCRATCH_TEMPLATE_ID } from "./templates";

// Template labels contain regex-significant characters ("Page-change watch" is
// fine, but a future "Q&A (daily)" would not be) — escape before building a
// name matcher from one.
function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

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

  fireEvent.click(screen.getByRole("button", { name: /morning email digest/i }));

  // toHaveValue doesn't support asymmetric matchers (it compares literal
  // values) — read the live value and match against it directly instead.
  const field = screen.getByLabelText(/what should it do/i) as HTMLTextAreaElement;
  expect(field.value).toMatch(/summar/i);
});

test("the filled description stays editable", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  fireEvent.click(screen.getByRole("button", { name: /morning email digest/i }));
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

test("the featured templates render as selectable cards with a label and blurb", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  // The start screen shows only the promoted ones — 6 real templates plus the
  // "start from scratch" escape hatch. The rest live behind "View all
  // templates", asserted in TemplateGallery.test.tsx.
  const featured = featuredTemplates();
  expect(featured.filter((t) => t.id !== SCRATCH_TEMPLATE_ID)).toHaveLength(6);
  for (const t of featured) {
    const card = screen.getByRole("button", { name: new RegExp(escapeRe(t.label), "i") });
    expect(card).toHaveTextContent(t.blurb);
  }
});

// The whole point of a template is to save the user the designer's THREE
// questions, which it can only do if the brief already answers them. The
// designer treats itself as ready once it knows what the agent does, when it
// runs, and whether it notifies (internal/prompts' <conversation_discipline>),
// so every shipped brief must actually say those things.
test("every template is a full brief: schedule, notification behaviour, and substance", () => {
  // A schedule can be stated as a cadence ("every weekday"), a frequency
  // ("twice a day"), or a clock time ("at 9:00am") — all answer "when does it
  // run". Deliberately NOT loose: an earlier version accepted the bare word
  // "each", so "30 minutes before each meeting" passed as though it were a
  // cadence, letting an event-driven template ship on a platform that has no
  // event triggers. "each" now only counts when followed by a time unit.
  const scheduleCue =
    /(\bevery\b|\beach (day|week|month|morning|evening|weekday|hour)\b|\bonce (a|an)\b|\btwice\b|\btimes a day\b|\bon the \d|\d{1,2}:\d{2})/i;
  const notifyCue = /\b(message me|tell me|remind me|send me|alert|stay quiet|don't message|note)\b/i;

  for (const t of AGENT_TEMPLATES) {
    if (t.id === SCRATCH_TEMPLATE_ID) continue;
    expect(t.category, `${t.id} needs a category`).toBeTruthy();
    expect(t.keywords.length, `${t.id} needs keywords`).toBeGreaterThan(0);
    // Long enough to be a real brief rather than the old one-line gesture.
    expect(t.description.length, `${t.id} description is too thin`).toBeGreaterThan(200);
    expect(t.description, `${t.id} must say WHEN it runs`).toMatch(scheduleCue);
    expect(t.description, `${t.id} must say whether it notifies`).toMatch(notifyCue);
  }
});

// The platform runs agents on a CRON SCHEDULER and has no webhook/event hook
// of any kind (internal/scheduler polls agent_schedules; the chat adapters are
// outbound-only). A template phrased as event-driven therefore promises a
// trigger that cannot exist, and the agent built from it would either not fire
// or quietly degrade to polling with no de-duplication. Both the user-facing
// blurb and the brief itself have to stay honest about that.
test("no template promises an event trigger the scheduler cannot provide", () => {
  const eventPhrasing =
    /\b(as soon as|the moment|immediately (when|after)|right when|before each|whenever (a|an|my|the)\b[^.]*\b(arrives|comes in|is sent|is created|happens))\b/i;

  for (const t of AGENT_TEMPLATES) {
    expect(t.description, `${t.id} description implies an event trigger`).not.toMatch(eventPhrasing);
    expect(t.blurb, `${t.id} blurb implies an event trigger`).not.toMatch(eventPhrasing);
  }
});

// A polling agent re-sees the same item on every run, so any template that
// reacts to individual items (rather than reporting a whole period) must tell
// the agent to remember what it already handled — otherwise it notifies about
// the same meeting/outage/price on every single check.
test("templates that react to individual items ask the agent to remember what it handled", () => {
  const reactive = ["meeting-prep", "uptime-check", "price-watch", "watch-for-changes", "follow-up-chaser"];
  const remembers = /\b(remember|already (briefed|told|reported|flagged|sent)|don't repeat|haven't already|keep a running note)\b/i;

  for (const id of reactive) {
    const t = AGENT_TEMPLATES.find((x) => x.id === id)!;
    expect(t.description, `${id} must tell the agent to remember what it already handled`).toMatch(remembers);
  }
});

test("template ids are unique", () => {
  const ids = AGENT_TEMPLATES.map((t) => t.id);
  expect(new Set(ids).size).toBe(ids.length);
});

test("selecting a template marks it visibly active, and switching moves the mark", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  const digest = screen.getByRole("button", { name: /morning email digest/i });
  const watch = screen.getByRole("button", { name: /page-change watch/i });

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

  const digest = screen.getByRole("button", { name: /morning email digest/i });
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

  fireEvent.click(screen.getByRole("button", { name: /page-change watch/i }));
  expect(confirmSpy).toHaveBeenCalled();
  // Declined — the hand-typed text survives.
  expect(field).toHaveValue("my own hand-typed brief");

  confirmSpy.mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: /page-change watch/i }));
  expect(field).toHaveValue(
    AGENT_TEMPLATES.find((t) => t.id === "watch-for-changes")!.description,
  );

  confirmSpy.mockRestore();
});

test("continuing with a filled-in template SENDS it to the designer as the opening message", async () => {
  wrap();
  await screen.findByLabelText(/what should it do/i);

  fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "Test Agent" } });
  fireEvent.click(screen.getByRole("button", { name: /morning email digest/i }));
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
