import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { DesignerSurface, type DesignerEndpoints, type DesignerLabels } from "./DesignerSurface";

// Same EventSource stub shape as designer.test.tsx — DesignerSurface never
// asserts on connection internals (that is sse.test.ts's job), it just needs
// `new EventSource(url)` to work and a way to drive error/close.
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
  buildButton: "Build it",
  saveButton: "✅ Save agent",
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
});

// ─── Read-only mirror ────────────────────────────────────────────────────────

// The design session is a per-workspace singleton and this surface adopts
// whatever session exists on mount. A chat-owned session must therefore render
// as a strictly read-only mirror: composer gone, actions gone, and above all no
// cancel POST, which would kill the live build on the other surface.
test("a chat-owned session renders read-only with no composer", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({
        active: true,
        state: "designing",
        origin: "chat",
        history: [{ role: "assistant", content: "Designing your agent." }],
      }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  expect(await screen.findByText(/running in your chat app/i)).toBeInTheDocument();
  expect(await screen.findByText("Designing your agent.")).toBeInTheDocument();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

test("cancel on a chat-owned session navigates without POSTing", async () => {
  const calls = mockFetch({
    "/x/state": () =>
      jsonResponse({ active: true, state: "designing", origin: "chat", history: [] }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await screen.findByText(/running in your chat app/i);
  await userEvent.click(screen.getByRole("button", { name: /cancel/i }));

  await waitFor(() => {
    expect(calls.filter((c) => c.url === "/x/cancel")).toHaveLength(0);
  });
});

// The owner is unaffected — without this the read-only gate could disable the
// whole surface and both tests above would still pass.
test("a web-owned session keeps its composer", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({ active: true, state: "designing", origin: "web", history: [] }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await waitFor(() => expect(screen.getByRole("textbox")).toBeInTheDocument());
  expect(screen.queryByText(/running in your chat app/i)).not.toBeInTheDocument();
});

// ─── Completion resilience ───────────────────────────────────────────────────

// A dropped or never-opened stream used to stop the spinner and give up,
// stranding a build whose result was already committed to History. That is the
// dead-spinner the user reported on a workspace with no chat platform.
test("a progress-stream error refetches state", async () => {
  let stateCalls = 0;
  mockFetch({
    "/x/state": () => {
      stateCalls++;
      return jsonResponse({ active: true, state: "designing", origin: "web", history: [] });
    },
    "/x/design": () => jsonResponse({ response: "building", done: false, building: true }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await waitFor(() => expect(screen.getByRole("textbox")).toBeInTheDocument());
  const box = screen.getByRole("textbox");
  await userEvent.type(box, "build it{Enter}");

  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  const es = FakeEventSource.instances[FakeEventSource.instances.length - 1]!;

  const before = stateCalls;
  // Never opened, then errors twice — openSSE retries once, then reports.
  es.onerror?.();
  const retry = FakeEventSource.instances[FakeEventSource.instances.length - 1]!;
  retry.onerror?.();

  await waitFor(() => expect(stateCalls).toBeGreaterThan(before));
});

// Third completion signal: even if the stream is swallowed entirely and never
// errors, the poll surfaces the result. Three properties in one flow, because
// each needs a real 5s interval to elapse.
//
// REAL timers, deliberately. Under this project's fake-timer setup an interval
// armed by an ASYNC state update never fires — the microtask that sets
// `generating` is not flushed inside an act() boundary, so the effect has not
// armed by the time the clock is advanced. Verified with a standalone probe
// reproducing it on a component of three lines; faking the clock here would
// assert nothing and pass.
test(
  "the poll preserves the live transcript, then adopts the result and stops",
  async () => {
    // Starts NOT generating and flips when the build POST lands, which is the real
    // order of events. Mounting straight into generating:true and then typing is
    // not reachable in the product: the composer is closed during a build, so a
    // build cannot be started from a surface that is already building.
    let generating = false;
    let history: Array<{ role: string; content: string }> = [];
    let stateCalls = 0;
    mockFetch({
      "/x/state": () => {
        stateCalls++;
        return jsonResponse({
          active: true,
          generating,
          state: "designing",
          origin: "web",
          history,
        });
      },
      "/x/design": () => {
        generating = true;
        return jsonResponse({ response: "🤖 Building…", done: false, building: true });
      },
    });
    wrap(
      <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
    );

    await waitFor(() => expect(screen.getByRole("textbox")).toBeInTheDocument());
    const box = screen.getByRole("textbox");
    await userEvent.type(box, "build it");
    fireEvent.keyDown(box, { key: "Enter", code: "Enter" });
    await screen.findByText("🤖 Building…");

    // 1. The poll runs while the build does.
    const afterMount = stateCalls;
    await waitFor(() => expect(stateCalls).toBeGreaterThan(afterMount), {
      timeout: 9000,
      interval: 250,
    });

    // 2. It has NOT adopted the (empty) mid-build server history. The approve
    //    turn and the placeholder exist only locally — startGeneration never
    //    records them — so adopting a mid-build snapshot would erase both and
    //    the user would watch their own message vanish.
    expect(screen.getByText("build it")).toBeInTheDocument();
    expect(screen.getByText("🤖 Building…")).toBeInTheDocument();

    // 3. Once the build ends, the next tick adopts the outcome.
    history = [{ role: "assistant", content: "Here is your agent — approve?" }];
    generating = false;
    await screen.findByText("Here is your agent — approve?", undefined, {
      timeout: 9000,
    });

    // …and the interval tears down, so nothing keeps polling afterwards.
    const afterStop = stateCalls;
    await new Promise((r) => setTimeout(r, 6000));
    expect(stateCalls).toBe(afterStop);
  },
  40000,
);
