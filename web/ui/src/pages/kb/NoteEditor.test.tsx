import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import NoteEditor from "./NoteEditor";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function errorResponse(status = 500, message = "boom") {
  return new Response(JSON.stringify({ error: { code: "internal", message } }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Regression test for a review-caught bug: flush() used to clear dirtyRef
// BEFORE the PUT resolved, so a failed save left the flag falsely clean —
// Ctrl/Cmd+S became a silent no-op and the unmount-flush skipped, losing the
// edit permanently. The fix clears dirty only in onSuccess.
test("a failed autosave keeps the edit dirty; Ctrl/Cmd+S retries with a fresh PUT", async () => {
  const putBodies: string[] = [];
  let putCount = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT") {
        putCount += 1;
        putBodies.push(JSON.parse(String(init.body)).content);
        if (putCount === 1) return Promise.resolve(errorResponse());
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "memory/USER.md",
            content: "# About Me\n\n<!-- placeholder -->\n",
            html: "",
            backlinks: [],
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const states: string[] = [];
  const qc = new QueryClient();
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={qc}>
      <NoteEditor path="memory/USER.md" onStateChange={(s) => states.push(s)} />
    </QueryClientProvider>,
  );

  // HTML-comment content is lossy -> raw mode, giving a plain textarea to
  // drive without needing a real TipTap DOM round-trip.
  await waitFor(() => expect(screen.getByText(/protect formatting/)).toBeInTheDocument());
  const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
  await user.click(textarea);
  await user.type(textarea, "extra");

  // The 1000ms debounce fires the first (failing) PUT.
  await waitFor(() => expect(putCount).toBe(1), { timeout: 3000 });
  await waitFor(() => expect(states[states.length - 1]).toBe("error"));

  // Ctrl+S must issue a SECOND PUT with the latest content — not be dropped
  // because dirtyRef was wrongly cleared by the failed first attempt.
  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  await waitFor(() => expect(putCount).toBe(2));
  expect(putBodies[1]).toContain("extra");
  // Raw mode's idle state is "raw", not "saved".
  await waitFor(() => expect(states[states.length - 1]).toBe("raw"));
});

test("a successful autosave transitions dirty -> saving -> raw with exactly one PUT", async () => {
  const putBodies: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT") {
        putBodies.push(JSON.parse(String(init.body)).content);
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "memory/USER.md",
            content: "# About Me\n\n<!-- placeholder -->\n",
            html: "",
            backlinks: [],
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const states: string[] = [];
  const qc = new QueryClient();
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={qc}>
      <NoteEditor path="memory/USER.md" onStateChange={(s) => states.push(s)} />
    </QueryClientProvider>,
  );

  await waitFor(() => expect(screen.getByText(/protect formatting/)).toBeInTheDocument());
  const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
  await user.click(textarea);
  await user.type(textarea, "extra");

  expect(states).toContain("dirty");
  await waitFor(() => expect(putBodies.length).toBe(1), { timeout: 3000 });
  expect(putBodies[0]).toContain("extra");
  await waitFor(() => expect(states[states.length - 1]).toBe("raw"));
});
