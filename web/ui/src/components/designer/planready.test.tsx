import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { DesignerSurface, type DesignerEndpoints, type DesignerLabels } from "./DesignerSurface";

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
  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  close() {}
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type Handler = (body: unknown, method: string) => Response | Promise<Response>;

function mockFetch(handlers: Record<string, Handler>) {
  const calls: Array<{ url: string; method: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });
      const handler = handlers[url];
      if (handler) return Promise.resolve(handler(body, method));
      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
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

async function sendViaComposer(text: string) {
  // With endpoints.state wired, the surface starts in `recovering` and disables
  // the composer until mount recovery resolves. Typing into a disabled textarea
  // silently does nothing, which is exactly what a whole failing suite looks
  // like when you forget this.
  // Explicit timeout: these wait on a real fetch settling, and the 1000ms
  // default is not enough on a loaded CI runner — it failed there while passing
  // locally six times over, which is the signature of a latency flake rather
  // than a behavioural one. Raising the ceiling does not weaken the assertion;
  // the composer still has to become enabled.
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled(), {
    timeout: 5000,
  });
  const box = screen.getByRole("textbox");
  await userEvent.type(box, text);
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
}

// Vitest's per-test budget defaults to 5000ms — the SAME number as the
// waitFor timeouts below, which is unpassable by construction: one wait can
// consume the whole test and leave nothing for the rest. That is what failed
// on CI while passing locally, and raising only the inner timeouts (as an
// earlier pass did) could not fix it. Set once for the file rather than per
// test: every test here calls sendViaComposer, which carries a waitFor(5000),
// and two of them call it twice with a userEvent.type of ~20 characters in
// between — so they all share the same latent structure and would surface one
// at a time on loaded runners.
vi.setConfig({ testTimeout: 30000 });

const inactive = () => jsonResponse({ active: false });

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// The reported bug: `fsmState === "designing"` covers the whole conversation,
// so the button offered itself under "Which page should I watch?" and clicking
// it built an agent against a half-specified plan.
test("no build button while the designer is still asking questions", async () => {
  mockFetch({
    "/x/state": inactive,
    "/x/design": () =>
      jsonResponse({
        response: "Which page should I watch?",
        done: false,
        state: "designing",
        plan_ready: false,
      }),
  });
  wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/a" gateBuildOnPlanReady onDone={vi.fn()} />,
  );

  await sendViaComposer("watch a page");
  expect(await screen.findByText("Which page should I watch?")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Approve & build/ })).not.toBeInTheDocument();
  // The composer is the whole interface during a Q&A — and typing "approve"
  // still works, which is why withholding the button is safe.
  expect(screen.getByRole("textbox")).toBeInTheDocument();
});

test("the build button appears once the plan is ready, and retracts on a follow-up question", async () => {
  let turn = 0;
  mockFetch({
    "/x/state": inactive,
    "/x/design": () => {
      turn += 1;
      if (turn === 1) {
        return jsonResponse({
          response: "Here's the plan.",
          done: false,
          state: "designing",
          plan_ready: true,
          pending_spec: "Tier: 1\nSchedule: 0 8 * * *",
        });
      }
      return jsonResponse({
        response: "Every hour on the hour, or every 60 minutes?",
        done: false,
        state: "designing",
        plan_ready: false,
      });
    },
  });
  wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/a" gateBuildOnPlanReady onDone={vi.fn()} />,
  );

  await sendViaComposer("watch example.com daily");
  expect(await screen.findByRole("button", { name: /Approve & build/ })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /View spec/ })).toBeInTheDocument();

  // A settled plan closes the message box: approving is a decision, and it goes
  // through a button rather than a guess at which words the server accepts.
  // "Make changes" is the way back to typing.
  expect(screen.getByRole("textbox")).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: /Make changes/ }));
  // Explicit timeout: these wait on a real fetch settling, and the 1000ms
  // default is not enough on a loaded CI runner — it failed there while passing
  // locally six times over, which is the signature of a latency flake rather
  // than a behavioural one. Raising the ceiling does not weaken the assertion;
  // the composer still has to become enabled.
  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled(), {
    timeout: 5000,
  });

  await sendViaComposer("actually make it hourly");
  await screen.findByText("Every hour on the hour, or every 60 minutes?");
  // A latch-once-true flag would leave the button armed under a question the
  // user has not answered — a worse defect than the one being fixed.
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Approve & build/ })).not.toBeInTheDocument(),
  );
});

// The SPA coerces a MISSING field to false. That is the safe direction here —
// the typed word still builds — but it must be pinned, because the opposite
// coercion would silently restore the original bug.
test("a response with no plan_ready field withholds the button", async () => {
  mockFetch({
    "/x/state": inactive,
    "/x/design": () => jsonResponse({ response: "Here's the plan.", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/a" gateBuildOnPlanReady onDone={vi.fn()} />,
  );

  await sendViaComposer("do a thing");
  await screen.findByText("Here's the plan.");
  expect(screen.queryByRole("button", { name: /Approve & build/ })).not.toBeInTheDocument();
});

// The skill designer shares this component, returns its own body with no
// plan_ready, and must be unaffected — coercing its missing field to false
// would hide its build button entirely.
test("without the opt-in, the button behaves exactly as before", async () => {
  mockFetch({
    "/x/state": inactive,
    "/x/design": () => jsonResponse({ response: "Here's the plan.", done: false, state: "designing" }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/a" onDone={vi.fn()} />);

  await sendViaComposer("do a thing");
  await screen.findByText("Here's the plan.");
  expect(await screen.findByRole("button", { name: /Approve & build/ })).toBeInTheDocument();
});

test("View spec switches to the Spec view and shows the proposed plan", async () => {
  mockFetch({
    "/x/state": inactive,
    "/x/design": () =>
      jsonResponse({
        response: "Here's the plan.",
        done: false,
        state: "designing",
        plan_ready: true,
        pending_spec: "Tier: 1\nSchedule: 0 8 * * *\nSecrets: none",
      }),
  });
  wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/a" gateBuildOnPlanReady onDone={vi.fn()} />,
  );

  await sendViaComposer("watch example.com");
  await userEvent.click(await screen.findByRole("button", { name: /View spec/ }));

  expect(await screen.findByText("Proposed plan")).toBeInTheDocument();
  expect(screen.getByText("0 8 * * *")).toBeInTheDocument();
});

// The dry run used to render as an ordinary bubble and scroll past. It is the
// one turn where action is required.
test("the verifying turn renders as a review card, not a chat bubble", async () => {
  const calls = mockFetch({
    "/x/state": inactive,
    "/x/design": (body: any) => {
      if (body.message === "save") {
        return jsonResponse({ response: "Saved.", done: true, agent_id: "a1" });
      }
      return jsonResponse({
        response: "Sample output: 3 deals found.",
        done: false,
        state: "verifying",
      });
    },
  });
  wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/a" gateBuildOnPlanReady onDone={vi.fn()} />,
  );

  await sendViaComposer("approve");

  const card = await screen.findByTestId("review-card");
  expect(card).toHaveTextContent("Sample output: 3 deals found.");
  expect(screen.getByText(/Dry run/)).toBeInTheDocument();
  // The same text must not ALSO be sitting in an ordinary bubble.
  expect(screen.queryByTestId("bubble-row")).not.toHaveTextContent(
    "Sample output: 3 deals found.",
  );

  await userEvent.click(screen.getByRole("button", { name: /Save agent/ }));
  await waitFor(() => {
    const design = calls.filter((c) => c.url === "/x/design");
    expect(design[design.length - 1]!.body).toEqual({ message: "save" });
  });
});
