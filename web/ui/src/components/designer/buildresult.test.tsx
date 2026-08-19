import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { DesignerSurface, type DesignerEndpoints, type DesignerLabels } from "./DesignerSurface";

// A build is DETACHED server-side: the design POST returns `building: true`
// immediately and the real outcome — the dry run — is written into History
// minutes later, when runGeneration finishes. Nothing in that POST carries it.
//
// So the review the user is there to read reaches the browser only if one of the
// completion signals fires AND its refetch is applied. reviewgate.test.tsx does
// NOT cover this: its POST returns `state: "verifying"` directly, which is the
// one shape the real server never produces for a build. These tests drive the
// actual lifecycle, for both ways a surface can be attached to a running build:
// the tab that STARTED it ("live") and a tab that joined it later ("recovery").

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 1;
  private listeners: Record<string, Array<() => void>> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  close() {
    this.readyState = 2;
  }
  // The server closes the design stream with a named `done` event (see
  // handleDesignProgress) — this is that event, not a synthetic shortcut.
  emitDone() {
    for (const l of this.listeners["done"] ?? []) l();
  }
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
  buildButton: "Build it",
  saveButton: "Save agent",
  entityName: "agent",
};

// The exact shape reviewMessage() emits for an executed dry run, sample and all.
const SAMPLE = "Skopje is 24C right now, high 29 and no rain — leave the umbrella.";
const REVIEW = `Here's what a test run produces:\n\n---\n${SAMPLE}\n---\n\nDoes this look right? Type **approve** to save the agent, or tell me what to change.`;

function verifyingState(extra: Record<string, unknown> = {}) {
  return {
    active: true,
    generating: false,
    state: "verifying",
    origin: "web",
    history: [
      { role: "user", content: "approve" },
      { role: "assistant", content: REVIEW },
    ],
    pending_agent_md: "# Suggested schedule: 0 8 * * 1\nWeather watcher.",
    pending_tools: {},
    ...extra,
  };
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function wrap(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

// The tab that started the build. Its POST returned building:true, so the
// outcome must arrive via the stream — awaitingBuildResultRef is what tells
// onDone to go and get it.
test("the tab that started the build shows the dry run when the stream ends", async () => {
  // Mount recovery runs BEFORE anything is sent, so the session must start
  // absent — returning the finished state here would drop the surface straight
  // into review, lock the composer, and the send this test is about would never
  // happen (that mistake made an earlier draft of this test pass vacuously).
  let finished = false;
  mockFetch({
    "/x/state": () => jsonResponse(finished ? verifyingState() : { active: false }),
    "/x/design": () => {
      finished = true; // the detached build completes while the stream is open
      return jsonResponse({ response: "Building your agent…", building: true, state: "designing" });
    },
  });

  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  const box = await screen.findByRole("textbox");
  await userEvent.type(box, "approve");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  // The stream opens for the running build, then the server closes it.
  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  const es = FakeEventSource.instances.at(-1)!;
  expect(es.url).toContain("/x/progress");
  es.emitDone();

  await waitFor(() => {
    expect(screen.getByTestId("review-card")).toBeInTheDocument();
  });
  expect(screen.getByTestId("review-body")).toHaveTextContent(SAMPLE);
});

// A second tab opened onto a build already in flight (the "Open it" path, or
// simply a reload). It never saw the POST, so recovery is the only thing that
// attaches it — and onDone must still fetch the finished result.
test("a tab that joined a running build shows the dry run when the stream ends", async () => {
  let generating = true;
  mockFetch({
    "/x/state": () =>
      jsonResponse(
        generating
          ? {
              active: true,
              generating: true,
              state: "designing",
              origin: "web",
              history: [{ role: "user", content: "approve" }],
            }
          : verifyingState(),
      ),
  });

  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  const es = FakeEventSource.instances.at(-1)!;
  generating = false; // the build finished on the server
  es.emitDone();

  await waitFor(() => {
    expect(screen.getByTestId("review-card")).toBeInTheDocument();
  });
  expect(screen.getByTestId("review-body")).toHaveTextContent(SAMPLE);
});

// The Spec tab REPLACES the transcript (DesignerSurface renders one or the
// other), while the action bar renders outside both. So a user who opens the
// spec to re-read the plan while the build runs is left, when it finishes,
// looking at Save / Request changes with no dry run anywhere on screen - the
// exact "buttons appeared but no dry run" report. The build is fine; the one
// turn it exists to show is behind a tab nobody was told to go back to.
test("the dry run is shown even when the build finishes on the Spec tab", async () => {
  let finished = false;
  mockFetch({
    "/x/state": () => jsonResponse(finished ? verifyingState() : { active: false }),
    "/x/design": () => {
      finished = true;
      return jsonResponse({ response: "Building your agent...", building: true, state: "designing" });
    },
  });

  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  const box = await screen.findByRole("textbox");
  await userEvent.type(box, "approve");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));

  // The user goes to read the spec while it builds.
  await userEvent.click(screen.getByRole("button", { name: /^spec$/i }));

  FakeEventSource.instances.at(-1)!.emitDone();

  // The finished dry run must reach them without their having to know to click
  // back to the transcript.
  await waitFor(() => {
    expect(screen.getByTestId("review-card")).toBeInTheDocument();
  });
  expect(screen.getByTestId("review-body")).toHaveTextContent(SAMPLE);
});

// jsdom has no layout engine, so nothing here can prove the dry run is VISIBLE
// — only that the declaration which makes it so is present. Same reasoning the
// KB pane records for overscroll-contain: the behavioural check needs a real
// browser, and the class assertion is the strongest proxy available.
//
// ChatScroll is `flex flex-col`, where a flex item shrinks below its content
// height by default. ReviewCard also sets overflow-hidden (its rounded corners
// and header border need it), so without shrink-0 the card was squeezed to its
// header and clipped the sample with nothing to scroll — the dry run rendered,
// sized to nothing, and hidden. Bubbles escape the same squeeze only because
// they have no overflow-hidden and simply spill.
test("the dry run card cannot be squeezed to nothing by the scroll column", async () => {
  let finished = false;
  mockFetch({
    "/x/state": () => jsonResponse(finished ? verifyingState() : { active: false }),
    "/x/design": () => {
      finished = true;
      return jsonResponse({ response: "Building...", building: true, state: "designing" });
    },
  });

  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  const box = await screen.findByRole("textbox");
  await userEvent.type(box, "approve");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  FakeEventSource.instances.at(-1)!.emitDone();

  const card = await screen.findByTestId("review-card");
  expect(card.className).toMatch(/(^|\s)shrink-0(\s|$)/);
  // The whole sample is in the DOM, not just the heading the user could see.
  expect(screen.getByTestId("review-body")).toHaveTextContent(SAMPLE);
});
