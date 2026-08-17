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

test("the action bar is not inside the scrolling transcript", async () => {
  // The property that actually keeps this fixed, and the one jsdom CAN check.
  //
  // The buttons used to live inside ChatScroll, which auto-scrolls only while the
  // reader is within 80px of the bottom. Scrolling up during a five-minute build
  // clears that flag, so the review card rendered off-screen: the buttons were in
  // the DOM, below the fold, while the composer sat locked by actions the user
  // could not see or reach. It was diagnosed twice as a logic bug and patched
  // twice; it was layout.
  //
  // jsdom has no layout engine and no scrolling, so no test here can prove a
  // button is visible on screen. What it CAN prove is that the bar is not a
  // descendant of the scrollable element — which makes "scrolled out of view"
  // structurally impossible rather than merely unlikely.
  mockFetch({ "/x/state": () => jsonResponse(stateSnapshot()) });
  const { container } = wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />,
  );

  const bar = await screen.findByTestId("designer-actions");
  const scroller = container.querySelector(".overflow-y-auto");
  expect(scroller).not.toBeNull();
  expect(scroller!.contains(bar)).toBe(false);
  // And the button really is in the bar, not merely somewhere on the page.
  expect(bar.contains(screen.getByRole("button", { name: LABELS.saveButton }))).toBe(true);
});

test("the Spec tab keeps the action bar and the closed box", async () => {
  // Reading the generated AGENT.md is a reasonable thing to do before accepting,
  // and it used to remove every button while leaving the box locked. Now the bar
  // lives outside both views, so Spec keeps its actions — which is what lets the
  // composer stay closed here at all.
  mockFetch({ "/x/state": () => jsonResponse(stateSnapshot()) });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />);

  await screen.findByRole("button", { name: LABELS.saveButton });
  fireEvent.click(screen.getByRole("button", { name: /^Spec$/ }));

  await waitFor(() => {
    expect(screen.getByRole("button", { name: LABELS.saveButton })).toBeInTheDocument();
  });
  expect(screen.getByRole("textbox")).toBeDisabled();
});

test("the composer is closed while a build is running", async () => {
  // Reported: the box stayed open after clicking build, inviting messages at a
  // designer that is mid-generation and cannot read them. The header's Cancel is
  // the escape during a build, which is why this state is allowed to have no
  // action bar.
  mockFetch({ "/x/state": () => jsonResponse(stateSnapshot({ generating: true })) });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} gateBuildOnPlanReady />);

  await waitFor(() => expect(screen.getByRole("textbox")).toBeDisabled());
  expect(screen.queryByTestId("designer-actions")).toBeNull();
  expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
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

  // Open Spec. The bar lives outside both views, so the actions survive — which
  // is the whole reason the box is allowed to stay closed here.
  fireEvent.click(screen.getByRole("button", { name: /^Spec$/ }));
  await waitFor(() => {
    expect(screen.getByRole("button", { name: LABELS.saveButton })).toBeInTheDocument();
  });
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
      // Cancel counts: during a build there is deliberately no action bar (there is
      // nothing to accept yet), and Cancel in the header is the genuine escape.
      const actionable = buttons.some((b) =>
        /save agent|approve & build|request changes|make changes|resume|discard|cancel/i.test(
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
