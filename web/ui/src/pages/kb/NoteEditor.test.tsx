import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useSearchParams } from "react-router";
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

// Mirrors KBPage's real wiring (`?path=` search param + `key={path}` remount,
// gated on the ".md" extension) rather than a static `path` prop — the
// rename/delete review-fix tests below need a REAL unmount of the old
// NoteEditor instance to happen (triggered by NoteEditor's own
// setSearchParams call on rename/delete success) to prove the unmount-flush
// cleanup doesn't fire a stray PUT afterward.
function PathBoundEditor() {
  const [params] = useSearchParams();
  const path = params.get("path");
  const isFile = !!path && path.endsWith(".md");
  return isFile ? <NoteEditor path={path} key={path} /> : <div data-testid="kb-empty-state" />;
}

function renderAtPath(initialPath: string) {
  const qc = new QueryClient();
  return render(
    <MemoryRouter initialEntries={[`/?path=${encodeURIComponent(initialPath)}`]}>
      <QueryClientProvider client={qc}>
        <PathBoundEditor />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

const TRIP_NOTE_FIXTURE = {
  path: "notes/trip plan.md",
  content: "# Trip\n\n<!-- placeholder -->\n",
  html: "",
  backlinks: [] as string[],
};

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
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <NoteEditor path="memory/USER.md" onStateChange={(s) => states.push(s)} />
      </QueryClientProvider>
    </MemoryRouter>,
  );

  // HTML-comment content is lossy -> raw mode, giving a plain textarea to
  // drive without needing a real TipTap DOM round-trip.
  await waitFor(() => expect(screen.getByText(/protect formatting/)).toBeInTheDocument());
  const textarea = screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement;
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
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <NoteEditor path="memory/USER.md" onStateChange={(s) => states.push(s)} />
      </QueryClientProvider>
    </MemoryRouter>,
  );

  await waitFor(() => expect(screen.getByText(/protect formatting/)).toBeInTheDocument());
  const textarea = screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement;
  await user.click(textarea);
  await user.type(textarea, "extra");

  expect(states).toContain("dirty");
  await waitFor(() => expect(putBodies.length).toBe(1), { timeout: 3000 });
  expect(putBodies[0]).toContain("extra");
  await waitFor(() => expect(states[states.length - 1]).toBe("raw"));
});

// Review fix: rename/delete used to race the debounce/unmount-flush
// machinery. A dirty edit + rename/delete → setSearchParams → key={path}
// remount → the OLD instance's unmount cleanup flushed a PUT to the OLD
// path (an upsert), silently resurrecting a file a delete just removed, or
// stomping content back onto the path a rename just moved away from.
test("dirty edit + rename: exactly one PUT to the old path before the rename POST, and none after (no stale unmount-flush)", async () => {
  const calls: Array<{ method: string; url: string; body?: { path?: string } }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (method === "PUT") return Promise.resolve(jsonResponse({ ok: true }));
      if (method === "POST" && url === "/api/v1/kb/rename") return Promise.resolve(jsonResponse({ ok: true }));
      if (url.startsWith("/api/v1/kb/note")) return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup({ delay: null });
  renderAtPath("notes/trip plan.md");

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(textarea);
  await user.type(textarea, "extra");

  const titleInput = screen.getByLabelText("Note title");
  await user.clear(titleInput);
  await user.type(titleInput, "summer");
  await user.keyboard("{Enter}");

  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(true),
  );

  const putCalls = calls.filter((c) => c.method === "PUT");
  expect(putCalls).toHaveLength(1);
  expect(putCalls[0].body?.path).toBe("notes/trip plan.md");

  const putIndex = calls.indexOf(putCalls[0]);
  const renameIndex = calls.findIndex((c) => c.method === "POST" && c.url === "/api/v1/kb/rename");
  expect(putIndex).toBeLessThan(renameIndex);

  // The rename's setSearchParams remounts NoteEditor (key={path}) at the
  // new path — wait for that, then confirm the OLD instance's unmount
  // cleanup didn't sneak in an extra PUT.
  await screen.findByLabelText("Note title");
  await new Promise((r) => setTimeout(r, 50));
  expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
});

test("dirty edit + delete: zero PUTs, exactly one DELETE (edit discarded, not resurrected)", async () => {
  const calls: Array<{ method: string; url: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (method === "DELETE") return Promise.resolve(jsonResponse({ ok: true }));
      if (method === "PUT") return Promise.resolve(jsonResponse({ ok: true }));
      if (url.startsWith("/api/v1/kb/note")) return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup({ delay: null });
  renderAtPath("notes/trip plan.md");

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(textarea);
  await user.type(textarea, "extra");

  await user.click(screen.getByLabelText("Note actions"));
  await user.click(await screen.findByText("Delete…"));
  await user.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  await screen.findByTestId("kb-empty-state");

  await new Promise((r) => setTimeout(r, 50));
  expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
});

test("a failed delete surfaces 'Delete failed: <message>' and the note stays (no navigation)", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "DELETE") return Promise.resolve(errorResponse(500, "boom"));
      if (url.startsWith("/api/v1/kb/note")) return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup();
  renderAtPath("notes/trip plan.md");

  await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(screen.getByLabelText("Note actions"));
  await user.click(await screen.findByText("Delete…"));
  await user.click(screen.getByRole("button", { name: "Delete" }));

  expect(await screen.findByText("Delete failed: boom")).toBeInTheDocument();
  // Still on the same note — didn't navigate away.
  expect(screen.getByDisplayValue("trip plan")).toBeInTheDocument();
  expect(screen.queryByTestId("kb-empty-state")).not.toBeInTheDocument();
});
