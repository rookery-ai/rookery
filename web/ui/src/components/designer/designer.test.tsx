import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { DesignerSurface, type DesignerEndpoints, type DesignerLabels } from "./DesignerSurface";

// Minimal EventSource stub — DesignerSurface never asserts on connection
// internals directly (that's sse.test.ts's job), it just needs `new
// EventSource(url)` to not throw and to let a test drive a message/close.
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
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
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
  // state omitted by default — most tests don't exercise mount recovery
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Review"],
  buildButton: "🔨 Build it",
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

test("send appends a user bubble then an assistant bubble from the response", async () => {
  mockFetch({
    "/x/design": () => jsonResponse({ response: "What should I call it?", done: false, state: "designing" }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("Build me a thing");

  expect(await screen.findByText("What should I call it?")).toBeInTheDocument();
  expect(screen.getByText("Build me a thing")).toBeInTheDocument();
});

test("startPayload is merged into the very first design POST only", async () => {
  const calls = mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "first") return jsonResponse({ response: "ok1", done: false, state: "designing" });
      return jsonResponse({ response: "ok2", done: false, state: "designing" });
    },
  });
  wrap(
    <DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" startPayload={{ name: "MyAgent" }} onDone={vi.fn()} />,
  );

  await sendViaComposer("first");
  await screen.findByText("ok1");
  await sendViaComposer("second");
  await screen.findByText("ok2");

  const designCalls = calls.filter((c) => c.url === "/x/design");
  expect(designCalls[0]!.body).toEqual({ message: "first", name: "MyAgent" });
  expect(designCalls[1]!.body).toEqual({ message: "second" });
});

test("designing-state Build button posts the literal phrase 'build it' and attaches the SSE card before the POST resolves", async () => {
  let resolveBuild!: (r: Response) => void;
  const calls = mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe") return jsonResponse({ response: "sounds good", done: false, state: "designing" });
      // The build POST intentionally hangs so the test can assert the
      // ActivityCard is already attached while it's still in flight.
      return new Promise<Response>((resolve) => {
        resolveBuild = resolve;
      });
    },
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("describe");
  const buildBtn = await screen.findByRole("button", { name: LABELS.buildButton });
  fireEvent.click(buildBtn);

  // SSE card attached immediately — before the design POST resolves.
  expect(screen.getByTestId("activity-card")).toBeInTheDocument();
  // Stepper shows "Build" (index 2) while the build POST is in flight.
  const buildStep = screen.getByTestId("stepper").querySelectorAll("li")[2]!;
  expect(buildStep).toHaveTextContent("Build");
  expect(buildStep.querySelector("span")).toHaveClass("border-foreground");

  const designCalls = () => calls.filter((c) => c.url === "/x/design");
  await waitFor(() => expect(designCalls()).toHaveLength(2));
  expect(designCalls()[1]!.body).toEqual({ message: "build it" });

  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  const es = FakeEventSource.instances[0]!;
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.onmessage?.({ data: "⚙️ Preparing workspace…" });
  expect(await screen.findByText("⚙️ Preparing workspace…")).toBeInTheDocument();

  resolveBuild(jsonResponse({ response: "Built it!", done: false, state: "verifying" }));
  await screen.findByText("Built it!");
});

test("SSE completing AFTER the build POST already resolved does not refetch or corrupt the transcript (regression, round 2)", async () => {
  // This is the realistic ordering for the design/progress endpoint: it has
  // no named "done" event, so completion is detected via reconnect -> 404 ->
  // onerror(readyState CLOSED) — which typically lands strictly after the
  // blocking design POST (bound to the very same generation) has already
  // resolved and updated the transcript. A round-1 fix that only guarded
  // "POST still pending" missed this ordering: onDone still refetched here,
  // and a stale `{active:false}` snapshot silently wiped the "build it"
  // bubble and the freshly-rendered "Built it!" response after the fact.
  let stateCalls = 0;
  const calls = mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe") return jsonResponse({ response: "sounds good", done: false, state: "designing" });
      return jsonResponse({ response: "Built it!", done: false, state: "verifying" });
    },
    "/x/state": () => {
      stateCalls += 1;
      return jsonResponse({ active: false });
    },
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await screen.findByRole("textbox"); // mount recovery settled — 1st GET /x/state
  await sendViaComposer("describe");
  const buildBtn = await screen.findByRole("button", { name: LABELS.buildButton });
  fireEvent.click(buildBtn);

  // The build POST resolves fully FIRST.
  expect(await screen.findByText("Built it!")).toBeInTheDocument();
  expect(screen.getByText("build it")).toBeInTheDocument();

  // THEN the SSE stream closes.
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  const es = FakeEventSource.instances[0]!;
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.readyState = FakeEventSource.CLOSED;
  es.onerror?.();

  // Give any (buggy) refetch a chance to land.
  await new Promise((r) => setTimeout(r, 0));

  // Transcript unchanged — no refetch fired at all past the initial mount GET.
  expect(screen.getByText("build it")).toBeInTheDocument();
  expect(screen.getAllByText("Built it!")).toHaveLength(1);
  expect(stateCalls).toBe(1); // only the mount-recovery GET — none from SSE onDone

  const designCalls = calls.filter((c) => c.url === "/x/design");
  expect(designCalls[1]!.body).toEqual({ message: "build it" });
});

test("a plain message answered with building:true attaches the SSE, renders ActivityCard lines, and advances the stepper to Build", async () => {
  mockFetch({
    "/x/design": () =>
      jsonResponse({
        response: "⏳ Still building your agent — I'll show the result here as soon as it's done.",
        done: false,
        building: true,
      }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("are you done yet");

  expect(await screen.findByTestId("activity-card")).toBeInTheDocument();
  const buildStep = screen.getByTestId("stepper").querySelectorAll("li")[2]!;
  expect(buildStep).toHaveTextContent("Build");
  expect(buildStep.querySelector("span")).toHaveClass("border-foreground");

  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  const es = FakeEventSource.instances[0]!;
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.onmessage?.({ data: "🔧 run_script(...)" });
  expect(await screen.findByText("🔧 run_script(...)")).toBeInTheDocument();
});

test("verifying-state Save button posts the literal phrase 'save'", async () => {
  const calls = mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe") return jsonResponse({ response: "ready to review", done: false, state: "verifying" });
      return jsonResponse({ response: "saved", done: true, agent_id: "abc123" });
    },
  });
  const onDone = vi.fn();
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={onDone} />);

  await sendViaComposer("describe");
  const saveBtn = await screen.findByRole("button", { name: LABELS.saveButton });
  fireEvent.click(saveBtn);

  await waitFor(() => expect(onDone).toHaveBeenCalledWith("abc123"));
  const designCalls = calls.filter((c) => c.url === "/x/design");
  expect(designCalls[1]!.body).toEqual({ message: "save" });
});

test("done:true response calls onDone with the agent id", async () => {
  mockFetch({
    "/x/design": () => jsonResponse({ response: "All set!", done: true, agent_id: "agent-42" }),
  });
  const onDone = vi.fn();
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={onDone} />);

  await sendViaComposer("approve");

  await waitFor(() => expect(onDone).toHaveBeenCalledWith("agent-42"));
});

test("a thrown ApiError renders a red banner and re-enables the composer", async () => {
  mockFetch({
    "/x/design": () => jsonResponse({ error: "name is required to start a new session" }, 400),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("hello");

  expect(await screen.findByText("name is required to start a new session")).toBeInTheDocument();
  expect(screen.getByRole("textbox")).not.toBeDisabled();
});

test("mount recovery replays history from an active session", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({
        active: true,
        generating: false,
        state: "designing",
        history: [
          { role: "user", content: "hey there" },
          { role: "assistant", content: "hello! what do you need?" },
        ],
        name: "Recovered Agent",
      }),
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  expect(await screen.findByText("hey there")).toBeInTheDocument();
  expect(screen.getByText("hello! what do you need?")).toBeInTheDocument();
});

test("resume banner shows when not active and a draft is present; Resume replays returned history", async () => {
  mockFetch({
    "/x/state": () => jsonResponse({ active: false }),
    "/x/resume": () =>
      jsonResponse({
        response: "Resuming your draft for **Draft X**. Continue, or approve.",
        state: "designing",
        history: [
          { role: "user", content: "make it check email" },
          { role: "assistant", content: "got it, anything else?" },
        ],
        agent_name: "Draft X",
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS} cancelTo="/agents"
      onDone={vi.fn()}
      draft={{ name: "Draft X" }}
    />,
  );

  expect(await screen.findByText(/unfinished draft: Draft X/)).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: /resume/i }));

  expect(await screen.findByText("make it check email")).toBeInTheDocument();
  expect(screen.getByText("got it, anything else?")).toBeInTheDocument();
  expect(screen.getByText(/Resuming your draft for/)).toBeInTheDocument();
  expect(screen.queryByText(/unfinished draft/)).not.toBeInTheDocument();
});

// Task 2 (power-and-creation SP9): the Spec tab. The state endpoint omits
// pending_agent_md/pending_tools ENTIRELY (undefined, not {}/"") whenever
// active is false — this is the exact trap a prior sub-plan already hit once
// (nil slice -> JSON null -> frontend `.length` crash) reached through a
// different branch. These two tests pin that the Spec tab never crashes on
// that shape, and that it DOES show real content once a build exists.
test("Spec tab empty-states and does not crash when the state endpoint has no active session", async () => {
  mockFetch({
    "/x/state": () => jsonResponse({ active: false }), // no pending_agent_md/pending_tools keys at all
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  await screen.findByRole("textbox"); // mount recovery settled
  fireEvent.click(screen.getByRole("button", { name: "Spec" }));

  expect(await screen.findByText(/nothing built yet/i)).toBeInTheDocument();
});

test("Spec tab renders the built brief and tool files once the state endpoint reports them", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({
        active: true,
        generating: false,
        state: "verifying",
        history: [],
        pending_agent_md: "# Daily digest\n\nSummarises your mail.",
        pending_tools: { "tools/main.py": "print('hi')" },
      }),
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  await screen.findByRole("textbox");
  fireEvent.click(screen.getByRole("button", { name: "Spec" }));

  expect(await screen.findByRole("heading", { name: "Daily digest" })).toBeInTheDocument();
  expect(screen.getByText("tools/main.py")).toBeInTheDocument();
});
