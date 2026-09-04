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

test("designing-state Build button posts the literal approval phrase and attaches the SSE card before the POST resolves", async () => {
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
  expect(designCalls()[1]!.body).toEqual({ message: "approve and build it" });

  // Two streams by now, not one. The "describe" turn opens its own progress
  // stream (the designer's read-only tool calls are streamed too) and closes it
  // when its reply lands; the build then opens a fresh one. The build's is the
  // most recent — driving instances[0] would drive the turn's closed stream.
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
  const es = FakeEventSource.instances.at(-1)!;
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
  // and a stale `{active:false}` snapshot silently wiped the approval
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
  expect(screen.getByText("approve and build it")).toBeInTheDocument();

  // THEN the SSE stream closes.
  // Two streams by now, not one. The "describe" turn opens its own progress
  // stream (the designer's read-only tool calls are streamed too) and closes it
  // when its reply lands; the build then opens a fresh one. The build's is the
  // most recent — driving instances[0] would drive the turn's closed stream.
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
  const es = FakeEventSource.instances.at(-1)!;
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.readyState = FakeEventSource.CLOSED;
  es.onerror?.();

  // Give any (buggy) refetch a chance to land.
  await new Promise((r) => setTimeout(r, 0));

  // Transcript unchanged — no refetch fired at all past the initial mount GET.
  expect(screen.getByText("approve and build it")).toBeInTheDocument();
  expect(screen.getAllByText("Built it!")).toHaveLength(1);
  expect(stateCalls).toBe(1); // only the mount-recovery GET — none from SSE onDone

  const designCalls = calls.filter((c) => c.url === "/x/design");
  expect(designCalls[1]!.body).toEqual({ message: "approve and build it" });
});

// When a design POST returns building:true, the build's real outcome (the
// verifying transition + the generated spec) arrives via the SSE stream, not
// that POST — so the "live" SSE onDone MUST refetch /state to pick it up.
// Without this, the surface never leaves Build and the Spec stays empty (the
// "name is required" / empty-Spec bugs). A normal same-tab build (POST returns
// verifying directly) still must NOT refetch — pinned by the round-2 test above.
test("a building:true build refetches /state on SSE done and surfaces the verifying result", async () => {
  let stateCalls = 0;
  mockFetch({
    "/x/design": () =>
      jsonResponse({
        response: "⏳ Still building your agent…",
        done: false,
        building: true,
      }),
    "/x/state": () => {
      stateCalls += 1;
      if (stateCalls === 1) return jsonResponse({ active: false }); // mount recovery
      return jsonResponse({
        active: true,
        generating: false,
        state: "verifying",
        history: [
          { role: "user", content: "build it" },
          { role: "assistant", content: "Built it — does this look right?" },
        ],
        pending_agent_md: "# Daily digest\n\nSummarises your mail.",
        pending_tools: {},
      });
    },
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  await screen.findByRole("textbox"); // mount recovery settled (state call 1)
  await sendViaComposer("build it"); // POST returns building:true

  // ONE stream: this turn opened it, and the building:true response upgraded it
  // IN PLACE rather than opening a second (see ensureSSE). Only a turn that has
  // already ended closes its stream, which is why the two-message tests above
  // see two.
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  const es = FakeEventSource.instances.at(-1)!;
  es.dispatchNamedEvent("done"); // SSE completes → live onDone must refetch

  // The refetched verifying result reaches the transcript...
  expect(await screen.findByText(/does this look right/i)).toBeInTheDocument();
  // ...and the Save action (verifying state) is now available.
  expect(await screen.findByRole("button", { name: LABELS.saveButton })).toBeInTheDocument();
  expect(stateCalls).toBeGreaterThanOrEqual(2);
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

  // ONE stream: this turn opened it, and the building:true response upgraded it
  // IN PLACE rather than opening a second (see ensureSSE). Only a turn that has
  // already ended closes its stream, which is why the two-message tests above
  // see two.
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  const es = FakeEventSource.instances.at(-1)!;
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
  // The click's own GET must actually fire — asserted directly via the call
  // count below, since both the mount snapshot and the click snapshot are
  // legitimately `{active:false}` here (there's nothing built yet either
  // way). See "Spec tab renders the built brief..." below for the sibling
  // case where the response itself changes between mount and click.
  let calls = 0;
  mockFetch({
    "/x/state": () => {
      calls += 1;
      // no pending_agent_md/pending_tools keys at all, on every call
      return jsonResponse({ active: false });
    },
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  await screen.findByRole("textbox"); // mount recovery settled (1st GET)
  fireEvent.click(screen.getByRole("button", { name: "Spec" }));

  expect(await screen.findByText(/appears here once you.*built the agent/i)).toBeInTheDocument();
  expect(calls).toBeGreaterThanOrEqual(2); // the click fired its own GET
});

test("no Spec tab renders when the caller has no state endpoint (the skill designer's shape)", async () => {
  // ENDPOINTS (module-level fixture) omits `state` by default — mirrors
  // SkillNewPage's real ENDPOINTS, which never sets it (DesignerSurface's own
  // binding comment: "state?: ... ABSENT for the skill designer"). A Spec tab
  // here would be permanently dead — it has no state endpoint to fetch from.
  mockFetch({
    "/x/design": () => jsonResponse({ response: "sounds good", done: false, state: "designing" }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/skills" onDone={vi.fn()} />);

  await sendViaComposer("describe");

  expect(screen.queryByRole("button", { name: "Spec" })).not.toBeInTheDocument();
});

test("Spec tab renders the built brief and tool files once the state endpoint reports them", async () => {
  // Regression for a red-verified false-positive: a mock that returns the
  // SAME populated snapshot on every /x/state call lets this test pass even
  // with the Spec button's onClick gutted to a no-op `setView("spec")` —
  // mount-time recovery alone already populates pendingAgentMD/pendingTools
  // before the click ever happens, so the click's own fetch is redundant in
  // that scenario. Sequencing the mock (mount = unpopulated/inactive, click
  // = populated) means the brief can ONLY appear once the click's own GET
  // has actually run.
  let calls = 0;
  mockFetch({
    "/x/state": () => {
      calls += 1;
      if (calls === 1) return jsonResponse({ active: false }); // mount recovery: nothing yet
      return jsonResponse({
        active: true,
        generating: false,
        state: "verifying",
        history: [],
        pending_agent_md: "# Daily digest\n\nSummarises your mail.",
        pending_tools: { "tools/main.py": "print('hi')" },
      });
    },
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  await screen.findByRole("textbox"); // mount recovery settled (call 1: inactive)
  fireEvent.click(screen.getByRole("button", { name: "Spec" })); // triggers call 2: populated

  expect(await screen.findByRole("heading", { name: "Daily digest" })).toBeInTheDocument();
  expect(screen.getByText("tools/main.py")).toBeInTheDocument();
  expect(calls).toBeGreaterThanOrEqual(2);
});

test("a build finishing while the Spec tab is already open refreshes it automatically, with an in-progress note beforehand", async () => {
  // The Composer renders below both views, so the user can click Build (or
  // type "build it") while sitting on the Spec tab. Nothing used to refetch
  // pendingAgentMD/pendingTools until the Spec button was clicked again, so
  // the exact "does this look right?" moment could show stale/empty content
  // with no signal. This pins the `generating`-driven auto-refetch effect —
  // triggered only by `generating` flipping false while view === "spec" —
  // and the "build in progress" note shown meanwhile.
  let resolveBuild!: (r: Response) => void;
  let stateCalls = 0;
  mockFetch({
    "/x/design": (body: any) => {
      if (body.message === "describe") return jsonResponse({ response: "sounds good", done: false, state: "designing" });
      return new Promise<Response>((resolve) => {
        resolveBuild = resolve;
      });
    },
    "/x/state": () => {
      stateCalls += 1;
      // Only the LAST call (after the build POST resolves and `generating`
      // flips false) should see the populated snapshot — every call while
      // the build is still in flight reports nothing built yet.
      if (stateCalls <= 2) return jsonResponse({ active: false });
      return jsonResponse({
        active: true,
        generating: false,
        state: "verifying",
        history: [],
        pending_agent_md: "# Daily digest\n\nSummarises your mail.",
        pending_tools: {},
      });
    },
  });
  wrap(
    <DesignerSurface endpoints={{ ...ENDPOINTS, state: "/x/state" }} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />,
  );

  await screen.findByRole("textbox"); // mount recovery settled (state call 1)
  await sendViaComposer("describe");

  const buildBtn = await screen.findByRole("button", { name: LABELS.buildButton });
  fireEvent.click(buildBtn); // generating -> true; build POST hangs

  fireEvent.click(screen.getByRole("button", { name: "Spec" })); // state call 2 (still inactive)
  expect(await screen.findByText(/appears here once you.*built the agent/i)).toBeInTheDocument();
  expect(screen.getByText(/build is in progress/i)).toBeInTheDocument();

  // The build POST resolves -> generating flips false -> the effect
  // refetches automatically, with no further click on the Spec button.
  resolveBuild(jsonResponse({ response: "Built it!", done: false, state: "verifying" }));

  // The surface now returns to the TRANSCRIPT when a build lands, because the
  // dry run is the turn that requires action and Spec replaces the transcript
  // entirely — sitting on Spec is how a finished build came to show Save /
  // Request changes with no output anywhere (see the transition effect in
  // DesignerSurface). So the refreshed spec is no longer on screen here.
  //
  // What this test still guarantees, and the reason the refetch above must keep
  // happening: the spec is not STALE when the user goes back to it. Without the
  // refetch they would click Spec and see the pre-build placeholder.
  // Wait for the REFETCH, which is the thing under test — not for the placeholder
  // to disappear, which is a different event that merely tends to happen first.
  //
  // `generating` flipping false is what removes that text, and the refetch is an
  // effect of the same flip: two consequences of one state change, with no
  // ordering between them. Waiting on one and then asserting the other passes
  // whenever the machine is quick enough and fails under load — it failed in CI
  // at exactly this line with stateCalls == 2, while the whole suite passed
  // locally.
  await waitFor(() => {
    expect(stateCalls).toBeGreaterThanOrEqual(3);
  });
  expect(screen.queryByText(/build is in progress/i)).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Spec" }));
  expect(await screen.findByRole("heading", { name: "Daily digest" })).toBeInTheDocument();
  expect(screen.queryByText(/build is in progress/i)).not.toBeInTheDocument();
});

// A fresh session used to render an empty transcript with a chatbox under it —
// no signal that anything had started, or what to type. The intro is a static
// affordance, NOT a fabricated assistant turn: it must never be posted back and
// must disappear once a real message exists.
test("intro shows on a fresh session and is replaced by the real transcript", async () => {
  const calls = mockFetch({
    "/x/design": () => jsonResponse({ response: "What should it do?", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
      intro={<div>Tell me what you want</div>}
    />,
  );

  expect(await screen.findByText("Tell me what you want")).toBeInTheDocument();

  await sendViaComposer("a daily digest");
  expect(await screen.findByText("What should it do?")).toBeInTheDocument();
  expect(screen.queryByText("Tell me what you want")).not.toBeInTheDocument();

  // The intro is presentation only — the first POST carries the user's own
  // message and nothing else.
  const design = calls.filter((c) => c.url === "/x/design");
  expect(design).toHaveLength(1);
  expect(design[0]!.body).toEqual({ message: "a daily digest" });
});

test("no intro is rendered when the caller doesn't supply one (edit mode)", async () => {
  mockFetch({});
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);
  await screen.findByRole("textbox");
  expect(screen.queryByText("Tell me what you want")).not.toBeInTheDocument();
});

// The resume banner is a different screen entirely — showing a "start here"
// card in front of a session that's about to be restored would be worse than
// the blank page it replaces.
test("intro is suppressed while the resume banner is showing", async () => {
  mockFetch({});
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
      draft={{ name: "Half-built" }}
      intro={<div>Tell me what you want</div>}
    />,
  );

  expect(await screen.findByText(/unfinished draft/i)).toBeInTheDocument();
  expect(screen.queryByText("Tell me what you want")).not.toBeInTheDocument();
});

test("design turns render a timestamp footer like every other chat", async () => {
  mockFetch({
    "/x/design": () => jsonResponse({ response: "What should it do?", done: false, state: "designing" }),
  });
  wrap(<DesignerSurface endpoints={ENDPOINTS} labels={LABELS} cancelTo="/agents" onDone={vi.fn()} />);

  await sendViaComposer("Build me a thing");
  await screen.findByText("What should it do?");

  // Both the optimistic user turn and the assistant reply are stamped locally —
  // the design POST returns prose, never a time.
  await waitFor(() => expect(screen.getAllByTestId("message-time")).toHaveLength(2));
});

test("resumed history keeps the server's timestamps and stamps the resume message", async () => {
  mockFetch({
    "/x/state": () => jsonResponse({ active: false }),
    "/x/resume": () =>
      jsonResponse({
        response: "Where were we — you wanted a daily digest.",
        state: "designing",
        history: [
          { role: "user", content: "a daily digest", created_at: "2026-07-28T09:30:00Z" },
          { role: "assistant", content: "how often?", created_at: "2026-07-28T09:30:05Z" },
        ],
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      draft={{ name: "Digest" }}
      autoResume
      onDone={vi.fn()}
    />,
  );

  await screen.findByText("Where were we — you wanted a daily digest.");
  // Two restored turns + the freshly generated resume message, which is NOT part
  // of `history` and so has to be stamped client-side.
  await waitFor(() => expect(screen.getAllByTestId("message-time")).toHaveLength(3));
});

test("startEndpoint takes the first message; later messages go to the design endpoint", async () => {
  const calls = mockFetch({
    "/x/start": () => jsonResponse({ response: "Here's what I found.", done: false, state: "designing" }),
    "/x/design": () => jsonResponse({ response: "Updated.", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      startEndpoint="/x/start"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("make it hourly");
  await screen.findByText("Here's what I found.");
  await sendViaComposer("actually daily");
  await screen.findByText("Updated.");

  const posts = calls.filter((c) => c.method === "POST").map((c) => c.url);
  expect(posts).toEqual(["/x/start", "/x/design"]);
});

test("startPayload is never merged into a startEndpoint POST", async () => {
  const calls = mockFetch({
    "/x/start": () => jsonResponse({ response: "ok", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      startEndpoint="/x/start"
      startPayload={{ name: "MyAgent" }}
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("first");
  await screen.findByText("ok");

  const start = calls.find((c) => c.url === "/x/start");
  expect(start?.body).toEqual({ message: "first" });
});

test("a recovered session the caller rejects is not adopted and its build is not streamed", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({
        active: true,
        generating: true,
        state: "designing",
        is_edit: false,
        agent_id: "someone-else",
        history: [{ role: "user", content: "an unrelated conversation" }],
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      acceptRecoveredSession={(s) => s.isEdit && s.agentId === "a1"}
      onDone={vi.fn()}
    />,
  );

  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  expect(screen.queryByText("an unrelated conversation")).not.toBeInTheDocument();
  expect(FakeEventSource.instances).toHaveLength(0);
});

test("a recovered session the caller accepts is still adopted", async () => {
  mockFetch({
    "/x/state": () =>
      jsonResponse({
        active: true,
        state: "designing",
        is_edit: true,
        agent_id: "a1",
        history: [{ role: "user", content: "make it daily" }],
      }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      acceptRecoveredSession={(s) => s.isEdit && s.agentId === "a1"}
      onDone={vi.fn()}
    />,
  );

  expect(await screen.findByText("make it daily")).toBeInTheDocument();
});

test("cancelling an untouched surface navigates without cancelling anyone's session", async () => {
  const calls = mockFetch({ "/x/state": () => jsonResponse({ active: false }) });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

  expect(calls.some((c) => c.url === "/x/cancel")).toBe(false);
});

test("cancelling after a message still cancels the session", async () => {
  const calls = mockFetch({
    "/x/state": () => jsonResponse({ active: false }),
    "/x/design": () => jsonResponse({ response: "ok", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={{ ...ENDPOINTS, state: "/x/state" }}
      labels={LABELS}
      cancelTo="/agents"
      onDone={vi.fn()}
    />,
  );

  await waitFor(() => expect(screen.getByRole("textbox")).not.toBeDisabled());
  await sendViaComposer("hello");
  await screen.findByText("ok");
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

  await waitFor(() => expect(calls.some((c) => c.url === "/x/cancel")).toBe(true));
});

// A failed OPENING post used to strand the surface: the optimistic bubble made
// messages.length non-zero, so the retry was treated as an ordinary turn, sent
// to endpoints.design with no session to step, and dead-ended. "Design session
// already active; cancel it first" is an expected outcome of the agent editor's
// start endpoint, so this path is reachable in normal use, not exotic.
test("a failed opening POST is retried against the start endpoint, not the design one", async () => {
  let attempt = 0;
  const calls = mockFetch({
    "/x/start": () => {
      attempt += 1;
      if (attempt === 1) {
        return jsonResponse({ error: "design session already active; cancel it first" }, 500);
      }
      return jsonResponse({ response: "Here's what I found.", done: false, state: "designing" });
    },
    "/x/design": () => jsonResponse({ response: "WRONG ENDPOINT", done: false, state: "designing" }),
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      startEndpoint="/x/start"
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("run it once a day");
  expect(await screen.findByText("design session already active; cancel it first")).toBeInTheDocument();
  // The failed turn's bubble is rolled back — nothing was created, and leaving it
  // would duplicate the message once the retry succeeds.
  await waitFor(() => expect(screen.queryByText("run it once a day")).not.toBeInTheDocument());

  await sendViaComposer("run it once a day");
  expect(await screen.findByText("Here's what I found.")).toBeInTheDocument();
  expect(screen.queryByText("WRONG ENDPOINT")).not.toBeInTheDocument();

  const posts = calls.filter((c) => c.method === "POST").map((c) => c.url);
  expect(posts).toEqual(["/x/start", "/x/start"]);
});

// Same latent bug on the create path: a failed first POST used to strand the
// name, so the retry opened no session either.
test("a failed first design POST still carries startPayload on the retry", async () => {
  let attempt = 0;
  const calls = mockFetch({
    "/x/design": () => {
      attempt += 1;
      if (attempt === 1) return jsonResponse({ error: "something broke" }, 500);
      return jsonResponse({ response: "ok", done: false, state: "designing" });
    },
  });
  wrap(
    <DesignerSurface
      endpoints={ENDPOINTS}
      labels={LABELS}
      cancelTo="/agents"
      startPayload={{ name: "MyAgent" }}
      onDone={vi.fn()}
    />,
  );

  await sendViaComposer("first");
  await screen.findByText("something broke");
  await sendViaComposer("first");
  await screen.findByText("ok");

  const posts = calls.filter((c) => c.method === "POST");
  expect(posts).toHaveLength(2);
  expect(posts[1]!.body).toEqual({ message: "first", name: "MyAgent" });
});
