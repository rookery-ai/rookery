// Test harness for the durable chat turn.
//
// A chat turn no longer completes inside the POST that starts it: the server
// answers 202, runs the coder on a detached context, and the browser follows an
// EventSource until it reports done. So a test that used to assert on a
// {"response": …} body now has to drive two things — the 202, and the stream's
// terminal event. This holds both in one place so the three chat suites cannot
// drift in how they simulate it.
//
// jsdom implements no EventSource at all, which is why a stub is mandatory
// rather than a convenience.

export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static autoComplete = false;
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;

  url: string;
  readyState = FakeEventSource.CONNECTING;
  closed = false;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners: Record<string, Array<() => void>> = {};

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
    // Fire on the microtask queue rather than synchronously: the caller is
    // still inside `new EventSource(...)` here, and the promise it is about to
    // await does not exist yet.
    if (FakeEventSource.autoComplete && type === "done") {
      queueMicrotask(() => this.dispatchNamedEvent("done"));
    }
  }

  /** Push one milestone line, as the server's `data:` frames do. */
  emit(line: string) {
    this.onmessage?.({ data: line });
  }

  /** Fire a named event — "done" or "error" are the terminal ones. */
  dispatchNamedEvent(type: string) {
    this.listeners[type]?.forEach((l) => l());
  }

  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
}

/**
 * Installs the stub and clears any instances left by a previous test.
 *
 * `autoComplete` ends every stream as soon as it opens. Use it in suites whose
 * subject is what a turn PRODUCES rather than how it progresses — the
 * attachment suites, which send one confirmation turn per file serially and
 * would otherwise need per-turn choreography for a batch of ten.
 */
export function installFakeEventSource(opts: { autoComplete?: boolean } = {}) {
  FakeEventSource.instances = [];
  FakeEventSource.autoComplete = opts.autoComplete ?? false;
  vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
}

/** The most recently opened stream, which is the one under test. */
export function latestStream(): FakeEventSource | undefined {
  return FakeEventSource.instances[FakeEventSource.instances.length - 1];
}

/**
 * Waits for the turn's stream to open, then ends it.
 *
 * Ending the stream is what makes the browser refetch the chat, so a test that
 * expects an assistant bubble must call this AND serve that message from the
 * chat detail endpoint — the reply now comes from persisted history rather than
 * from the POST's own response.
 */
export async function completeTurn(kind: "done" | "error" = "done") {
  const es = await vi.waitFor(() => {
    const s = latestStream();
    if (!s) throw new Error("no EventSource was opened for the turn");
    return s;
  });
  es.dispatchNamedEvent(kind);
  return es;
}

/** The 202 body the start endpoint now returns. */
export function turnAcceptedResponse(turnID = "t1") {
  return new Response(JSON.stringify({ turn_id: turnID }), {
    status: 202,
    headers: { "Content-Type": "application/json" },
  });
}
