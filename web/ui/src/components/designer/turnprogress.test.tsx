import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import {
  DesignerSurface,
  type DesignerEndpoints,
  type DesignerLabels,
} from "./DesignerSurface";

// Same minimal EventSource stub the other designer suites use: the surface
// never inspects connection internals, it just needs `new EventSource(url)` to
// work and a way to drive a message.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  url: string;
  readyState = FakeEventSource.CONNECTING;
  closed = false;
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
  dispatchNamedEvent(type: string) {
    this.listeners[type]?.forEach((l) => l());
  }
  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type Handler = (body: unknown, method: string) => Response | Promise<Response>;

function mockFetch(handlers: Record<string, Handler>) {
  const calls: Array<{ url: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ url, method });
      const handler = handlers[url];
      if (handler) {
        const body = init?.body ? JSON.parse(String(init.body)) : undefined;
        return Promise.resolve(handler(body, method));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

// `state` is omitted for the same reason the sibling suite omits it: its
// presence turns on mount recovery, which none of these tests are about — with
// it, the surface renders a recovered session before the test has sent anything.
const ENDPOINTS: DesignerEndpoints = {
  design: "/x/design",
  cancel: "/x/cancel",
  resume: "/x/resume",
  dismiss: "/x/dismiss",
  progress: "/x/progress",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Review"],
  buildButton: "Build it",
  saveButton: "✅ Save agent",
  entityName: "agent",
};

function wrap(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

async function sendViaComposer(text: string) {
  const box = screen.getByRole("textbox");
  await userEvent.type(box, text);
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// The reported bug: since the design conversation gained read-only tools it
// reads the user's knowledge base while it answers, and none of that was
// visible. ensureSSE was only ever called on recovery, on a build, or when a
// POST reported one already running — never for an ordinary turn — so the user
// watched a silent spinner while the designer searched their notes.
test("an ordinary conversation turn attaches the progress stream", async () => {
  mockFetch({
    "/x/design": () =>
      jsonResponse({ response: "Which page?", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("watch a page for me");
  await screen.findByText("Which page?");

  expect(
    FakeEventSource.instances.some((es) => es.url.includes("/x/progress")),
  ).toBe(true);
});

// The card is shared with the build, whose title is "Building your agent…".
// Showing that over a turn's knowledge-base reads would be worse than the
// silence it replaces — it would claim a build that is not happening.
test("a turn's tool calls are shown as knowledge-base activity, not as a build", async () => {
  mockFetch({
    "/x/design": () =>
      jsonResponse({ response: "Found it.", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("what did I write about the dentist?");

  const es = FakeEventSource.instances.find((e) =>
    e.url.includes("/x/progress"),
  )!;
  es.onmessage?.({ data: "🔧 search_files(dentist)" });

  expect(await screen.findByText("🔧 search_files(dentist)")).toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByText(/knowledge base/i)).toBeInTheDocument(),
  );
  expect(screen.queryByText(/Building your agent/i)).not.toBeInTheDocument();
});

// Most turns read nothing, and the server opens no channel for a text-only
// turn. An empty activity card on every reply would be noise claiming work that
// never happened.
test("a turn that reads nothing shows no activity card", async () => {
  mockFetch({
    "/x/design": () =>
      jsonResponse({ response: "Sure.", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("call it Watcher");
  await screen.findByText("Sure.");

  expect(screen.queryByText(/knowledge base/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/Building your agent/i)).not.toBeInTheDocument();
});

// The turn stream and the build stream are the SAME server channel, so a build
// starting while a turn's stream is attached finds the handle slot taken.
// Early-returning would leave it labelled "turn" — which never refetches on
// done — and the build's result would never be picked up, which is the dead
// spinner this surface has a documented history of.
//
// This asserts the visible half. The refetch half is covered by
// designer.test.tsx's "a building:true build refetches /state on SSE done",
// which routes through handleSend and so exercises this same upgrade.
test("a build upgrades a turn's stream instead of being locked out of it", async () => {
  mockFetch({
    "/x/design": () =>
      jsonResponse({
        response: "⏳ Still building your agent — I'll show the result when it's done.",
        done: false,
        building: true,
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("go ahead");

  // The card stops calling itself a knowledge-base read and becomes a build.
  const card = await screen.findByTestId("activity-card");
  await waitFor(() => expect(card).toHaveTextContent(/Building your agent/i));
  expect(card).not.toHaveTextContent(/Looking through/i);
});
