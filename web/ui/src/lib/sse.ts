// Thin EventSource wrapper for the design/run progress endpoints
// (web/handlers_agents.go handleDesignProgress, web/run_tracker.go
// handleRunProgress). Both emit plain `data: <string>` lines and close the
// stream when generation/a run completes; a 404 JSON response means there's
// no active stream to attach to (EventSource fires `error` without ever
// opening in that case).
export type SSEHandle = { close(): void };

export function openSSE(
  url: string,
  opts: {
    onMessage(line: string): void;
    onDone(): void; // stream closed by server (normal completion)
    onError?(): void; // failed to connect / connection lost (after internal retry ONCE)
  },
): SSEHandle {
  let source: EventSource | null = null;
  let closed = false;
  let everOpened = false;
  let retried = false;

  function teardown() {
    source?.close();
    source = null;
  }

  function connect() {
    const es = new EventSource(url);
    source = es;

    es.onopen = () => {
      everOpened = true;
    };

    es.onmessage = (ev: MessageEvent) => {
      if (closed) return;
      everOpened = true;
      opts.onMessage(ev.data);
    };

    es.onerror = () => {
      if (closed) return;

      if (everOpened) {
        // We had a working connection. EventSource always fires `error` when
        // the server closes the stream (handleDesignProgress/handleRunProgress
        // return once generation/run finishes) — readyState CLOSED there means
        // "done", not "broken". A CONNECTING readyState here means the browser
        // is transparently retrying a transient drop; leave it alone.
        if (es.readyState === EventSource.CLOSED) {
          closed = true;
          teardown();
          opts.onDone();
        }
        return;
      }

      // Never successfully opened — connect-failure path: one silent retry,
      // then report failure (covers the 404-no-active-stream case too, since
      // that never reaches `open`/`message` either).
      teardown();
      if (!retried) {
        retried = true;
        connect();
        return;
      }
      closed = true;
      opts.onError?.();
    };
  }

  connect();

  return {
    close() {
      if (closed) return;
      closed = true;
      teardown();
    },
  };
}
