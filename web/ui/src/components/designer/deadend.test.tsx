import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { DesignerSurface, type DesignerEndpoints, type DesignerLabels } from "./DesignerSurface";

// The dead end: a locked message box with no buttons to press.
//
// Reported from a real session — the composer was closed, no action row appeared,
// and there was no way to accept, reject or say anything. These tests pin the one
// invariant that makes that state unreachable: THE COMPOSER IS ONLY EVER LOCKED
// WHEN AT LEAST ONE ACTION BUTTON IS ON SCREEN. Whatever else changes about the
// designer, a user must always have some way to act.

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners: Record<string, Array<() => void>> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, l: () => void) {
    (this.listeners[type] ??= []).push(l);
  }
  close() {}
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type Handler = (body: any, method: string) => Response;

function mockFetch(handlers: Record<string, Handler>) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      const key = Object.keys(handlers).find((k) => url.startsWith(k));
      if (!key) return Promise.resolve(jsonResponse({}, 404));
      return Promise.resolve(handlers[key]!(body, method));
    }),
  );
}

const ENDPOINTS: DesignerEndpoints = {
  design: "/x/design",
  cancel: "/x/cancel",
  resume: "/x/resume",
  dismiss: "/x/dismiss",
  progress: "/x/progress",
  state: "/x/state",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Review"],
  buildButton: "Approve & build",
  saveButton: "Save agent",
  entityName: "agent",
};

function wrap(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// The real transcript that produced the report: clarifying questions, a settled
// plan that ends by inviting approval, then the dry run. Crucially the model
// emitted NO [TECHNICAL SPEC] marker, so the server reports plan_ready false.
const PLAN_TURN =
  "Everything's clear — here's the plan:\n\nIt will check AdGuard every morning at 8.\n\nType approve and I'll build it for you.";

function stateSnapshot(over: Record<string, unknown> = {}) {
  return {
    active: true,
    generating: false,
    state: "verifying",
    origin: "web",
    plan_ready: false,
    pending_spec: "",
    pending_agent_md: "# Suggested schedule: 0 8 * * *\n",
    pending_tools: {},
    history: [
      { role: "user", content: "watch my adguard" },
      { role: "assistant", content: "Which spreadsheet?" },
      { role: "user", content: "DNS watch" },
      { role: "assistant", content: PLAN_TURN },
      { role: "assistant", content: "Here's what a test run produces: 44 new domains" },
    ],
    ...over,
  };
}

test("the review step shows its actions and is never a locked box with no buttons", async () => {
  mockFetch({ "/x/state": () => jsonResponse(stateSnapshot()) });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />);

  // The dry run and its actions must be present even though plan_ready is false —
  // that flag gates the BUILD button during design, and must not reach the review
  // step, where the build already exists.
  expect(await screen.findByRole("button", { name: LABELS.saveButton })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /request changes/i })).toBeInTheDocument();
  expect(screen.getByText(/44 new domains/)).toBeInTheDocument();
});

test("a settled plan offers actions even when the model omitted the spec marker", async () => {
  // The reported dead end. gateBuildOnPlanReady exists so the build button does
  // not appear under a clarifying question — but a weak model never emits the
  // marker at all, so the gate never opened and the user was left with a plan,
  // no buttons, and (once the composer locks) nothing to press. The plan's own
  // invitation to approve is the fallback signal.
  mockFetch({
    "/x/state": () =>
      jsonResponse(stateSnapshot({ state: "designing", history: [
        { role: "user", content: "watch my adguard" },
        { role: "assistant", content: PLAN_TURN },
      ] })),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />);

  expect(await screen.findByRole("button", { name: LABELS.buildButton })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /make changes/i })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /view spec/i })).toBeInTheDocument();
  // A settled plan is a decision point, so the box is closed until Make changes.
  expect(screen.getByRole("textbox")).toBeDisabled();
});

test("a clarifying question offers no build button and leaves the composer open", async () => {
  // The behaviour gateBuildOnPlanReady was added for, which must survive: an
  // ordinary question is not a plan, so nothing is offered to approve and the
  // user must be able to type their answer.
  mockFetch({
    "/x/state": () =>
      jsonResponse(stateSnapshot({ state: "designing", history: [
        { role: "user", content: "watch my adguard" },
        { role: "assistant", content: "Which page should I watch?" },
      ] })),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />);

  const box = await screen.findByRole("textbox");
  await waitFor(() => expect(box).not.toBeDisabled());
  expect(screen.queryByRole("button", { name: LABELS.buildButton })).toBeNull();
});

test("switching to the Spec tab does not leave a locked box with no buttons", async () => {
  // The reported dead end, exactly. Both action rows are rendered INSIDE the
  // transcript; opening Spec replaces that subtree while the composer, which
  // lives outside it, keeps rendering. Locking the composer from actions the user
  // can no longer see left them with no buttons and no way to type — while the
  // finished build sat safely on the server. Reading the generated AGENT.md is a
  // completely reasonable thing to do at the review step, which is what made this
  // easy to hit and impossible to escape.
  mockFetch({ "/x/state": () => jsonResponse(stateSnapshot()) });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />);

  // Review step: buttons present, box closed.
  const save = await screen.findByRole("button", { name: LABELS.saveButton });
  expect(save).toBeInTheDocument();
  expect(screen.getByRole("textbox")).toBeDisabled();

  // Open Spec — the transcript and its buttons go away.
  fireEvent.click(screen.getByRole("button", { name: /^Spec$/ }));
  await waitFor(() => {
    expect(screen.queryByRole("button", { name: LABELS.saveButton })).toBeNull();
  });

  // …so the box must come back, or there is nothing at all the user can do.
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
});

test("the composer is never disabled while no action button is offered", async () => {
  // The invariant, stated directly. Every state below is one a user can land in;
  // in each, either a button is present or the box is usable — never neither.
  for (const snap of [
    stateSnapshot(), // review step
    stateSnapshot({ state: "designing", history: [{ role: "assistant", content: PLAN_TURN }] }),
    stateSnapshot({ state: "designing", history: [{ role: "assistant", content: "Which page?" }] }),
    stateSnapshot({ generating: true }),
    stateSnapshot({ state: "designing", history: [] }),
  ]) {
    mockFetch({ "/x/state": () => jsonResponse(snap) });
    const view = wrap(
      <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />,
    );
    await waitFor(() => {
      const box = screen.queryByRole("textbox");
      const buttons = screen.queryAllByRole("button");
      const actionable = buttons.some((b) =>
        /save agent|approve & build|request changes|make changes|resume|discard/i.test(
          b.textContent ?? "",
        ),
      );
      const canType = box !== null && !(box as HTMLTextAreaElement).disabled;
      if (!actionable && !canType) {
        throw new Error(`dead end: composer disabled and no action button for ${JSON.stringify(snap.state)}`);
      }
    });
    view.unmount();
  }
});
