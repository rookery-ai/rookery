// Thin EventSource wrapper for the design/run progress endpoints
// (web/handlers_agents.go handleDesignProgress, web/run_tracker.go
// handleRunProgress). Both stream plain `data: <string>` milestone lines
// and end the stream when generation/a run completes — but they signal
// that completion differently: handleRunProgress emits a named
// `event: done\ndata: 1\n\n` immediately before closing (see
// run_tracker.go's `rs.progressCh` close branch), while handleDesignProgress
// just closes the stream with no named event. A 404 JSON response means
// there's no active stream to attach to (EventSource fires `error` without
// ever opening in that case).
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

    // Primary completion signal for endpoints that emit it (currently just
    // handleRunProgress): deterministic, no need to wait on a subsequent
    // `error` + reconnect + 404 round trip. Guarded by `closed` so a stray
    // `error` event dispatched afterward (browsers fire `error` on the
    // connection drop that follows the server closing the stream) can never
    // double-fire onDone.
    es.addEventListener("done", () => {
      if (closed) return;
      closed = true;
      teardown();
      opts.onDone();
    });

    es.onerror = () => {
      if (closed) return;

      if (everOpened) {
        // Fallback done-detection for endpoints with no named "done" event
        // (handleDesignProgress — it just closes the stream when generation
        // finishes). EventSource always fires `error` when the server closes
        // the stream; readyState CLOSED there means "done", not "broken". A
        // CONNECTING readyState here means the browser is transparently
        // retrying a transient drop; leave it alone. (For handleRunProgress
        // this branch is now mostly moot — the "done" listener above already
        // closed and returned before this fires — but stays as a safety net.)
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
