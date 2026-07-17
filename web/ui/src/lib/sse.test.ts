import { openSSE } from "./sse";

// Stubbed global EventSource: captures every instance created (one per
// connect/reconnect attempt) so tests can drive open/message/error events
// manually without a real network/EventSource implementation (jsdom has
// none, which is exactly what makes this stub the global).
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

  // Named-event support (run_tracker.go's handleRunProgress emits
  // `event: done` before closing) — mirrors the real EventSource
  // addEventListener API rather than the plain onmessage property.
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

function resetFake() {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
}

beforeEach(() => {
  resetFake();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test("onMessage receives data lines in arrival order", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  openSSE("/api/v1/agents/design/progress", { onMessage, onDone });

  const es = FakeEventSource.instances[0];
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.onmessage?.({ data: "⚙️ Preparing workspace…" });
  es.onmessage?.({ data: "🔧 run_script(...)" });

  expect(onMessage.mock.calls.map((c) => c[0])).toEqual([
    "⚙️ Preparing workspace…",
    "🔧 run_script(...)",
  ]);
  expect(onDone).not.toHaveBeenCalled();
});

test("server closing the stream (error with readyState CLOSED after a successful open) calls onDone", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  const onError = vi.fn();
  openSSE("/api/v1/agents/design/progress", { onMessage, onDone, onError });

  const es = FakeEventSource.instances[0];
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.onmessage?.({ data: "✓ done" });

  // Server ends the stream: EventSource always fires error on server close.
  es.readyState = FakeEventSource.CLOSED;
  es.onerror?.();

  expect(onDone).toHaveBeenCalledTimes(1);
  expect(onError).not.toHaveBeenCalled();
  expect(es.closed).toBe(true);
});

test("run endpoint's named `done` event calls onDone exactly once and closes the source", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  const onError = vi.fn();
  openSSE("/api/v1/agents/abc/run/progress", { onMessage, onDone, onError });

  const es = FakeEventSource.instances[0];
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();
  es.onmessage?.({ data: "🔧 running…" });

  // handleRunProgress writes `event: done\ndata: 1\n\n` right before closing
  // — a named event, not a plain `data:` line, so it must NOT go through
  // onMessage.
  es.dispatchNamedEvent("done");

  expect(onDone).toHaveBeenCalledTimes(1);
  expect(onError).not.toHaveBeenCalled();
  expect(es.closed).toBe(true);
  expect(onMessage).toHaveBeenCalledTimes(1); // only the run_script line, not "done"
});

test("named `done` followed by the browser's own error-on-close event still fires onDone only once", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  const onError = vi.fn();
  openSSE("/api/v1/agents/abc/run/progress", { onMessage, onDone, onError });

  const es = FakeEventSource.instances[0];
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();

  es.dispatchNamedEvent("done");
  expect(onDone).toHaveBeenCalledTimes(1);

  // The connection drop that follows the server closing the stream still
  // reaches the browser as an `error` event — must be a no-op here since
  // the handle already closed itself off the named event.
  es.readyState = FakeEventSource.CLOSED;
  es.onerror?.();

  expect(onDone).toHaveBeenCalledTimes(1);
  expect(onError).not.toHaveBeenCalled();
});

test("two connect failures (never opened) trigger one silent retry then onError", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  const onError = vi.fn();
  openSSE("/api/v1/agents/design/progress", { onMessage, onDone, onError });

  expect(FakeEventSource.instances).toHaveLength(1);
  const first = FakeEventSource.instances[0];
  first.onerror?.(); // never opened -> silent retry, new EventSource created
  expect(onError).not.toHaveBeenCalled();
  expect(onDone).not.toHaveBeenCalled();
  expect(FakeEventSource.instances).toHaveLength(2);
  expect(first.closed).toBe(true);

  const second = FakeEventSource.instances[1];
  second.onerror?.(); // still never opened -> give up, report onError
  expect(onError).toHaveBeenCalledTimes(1);
  expect(onDone).not.toHaveBeenCalled();
  expect(second.closed).toBe(true);
});

test("close() detaches — underlying source is closed and further events are ignored", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  const onError = vi.fn();
  const handle = openSSE("/api/v1/agents/design/progress", { onMessage, onDone, onError });

  const es = FakeEventSource.instances[0];
  es.readyState = FakeEventSource.OPEN;
  es.onopen?.();

  handle.close();
  expect(es.closed).toBe(true);

  // Idempotent — calling again must not throw or double-fire anything.
  expect(() => handle.close()).not.toThrow();

  // Events arriving after close must not surface to callbacks.
  es.onmessage?.({ data: "late line" });
  es.readyState = FakeEventSource.CLOSED;
  es.onerror?.();
  expect(onMessage).not.toHaveBeenCalled();
  expect(onDone).not.toHaveBeenCalled();
  expect(onError).not.toHaveBeenCalled();
});

test("a 404/never-opens stream with no onError provided does not throw", () => {
  const onMessage = vi.fn();
  const onDone = vi.fn();
  expect(() => {
    openSSE("/api/v1/agents/design/progress", { onMessage, onDone });
  }).not.toThrow();

  const first = FakeEventSource.instances[0];
  first.onerror?.();
  const second = FakeEventSource.instances[1];
  expect(() => second.onerror?.()).not.toThrow();
});
