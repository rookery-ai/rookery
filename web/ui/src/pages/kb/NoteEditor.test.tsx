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

// A manually-resolvable Promise — used to hold a mocked PUT "in flight"
// until the test decides to let it settle, so tests can assert on the
// window WHILE a save is pending rather than only on its eventual outcome.
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
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

function renderAtPath(initialPath: string, qc: QueryClient = new QueryClient()) {
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

// Regression test for a user-reported crash: `TypeError: Cannot read
// properties of null (reading 'length')` on clicking any KB note. Before the
// fix, GET /api/v1/kb/note could serialize "backlinks":null for a note with
// no incoming [[wikilinks]] (vault.Backlinks's nil `var out []string`).
// useKBNote's queryFn now normalizes it with `?? []` as a belt-and-braces
// guard alongside the backend fix (web/api_kb.go's orEmpty). Mock the OLD
// broken (null) response shape directly — bypassing the KBNote TS type,
// which the real fetch response isn't checked against either — to prove the
// editor renders without throwing.
test("a note response with backlinks:null (pre-fix API shape) renders without throwing", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "notes/lonely.md",
            content: "# Lonely\n\nno links here",
            html: "<h1>Lonely</h1>\n<p>no links here</p>\n",
            backlinks: null,
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderAtPath("notes/lonely.md");

  expect(await screen.findByDisplayValue("lonely")).toBeInTheDocument();
});

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

// Re-review ruling: a failed pre-flush must ABORT the rename outright — the
// earlier fix let it proceed after a failed flush ("settling" not
// "succeeding"), which meant a network blip could rename the file with
// stale server-side content while the actual edit died with the unmounting
// instance. This was the last live data-loss path in the flow.
test("dirty edit + rename: a failed pre-flush ABORTS the rename; retry after a successful flush proceeds PUT-then-rename", async () => {
  let putShouldFail = true;
  const calls: Array<{ method: string; url: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (method === "PUT") {
        return Promise.resolve(putShouldFail ? errorResponse(500, "put boom") : jsonResponse({ ok: true }));
      }
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

  // First attempt: the pre-flush PUT fails — no rename POST must fire.
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(await screen.findByText(/rename cancelled/i)).toBeInTheDocument();
  await new Promise((r) => setTimeout(r, 50));
  expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(false);
  // Still on the same note — the rename never happened.
  expect(screen.getByDisplayValue("summer")).toBeInTheDocument();

  // Retry: the PUT now succeeds — flush lands, THEN the rename POST fires.
  putShouldFail = false;
  await user.click(screen.getByDisplayValue("summer"));
  await user.keyboard("{Enter}");

  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(true),
  );
  const putCalls = calls.filter((c) => c.method === "PUT");
  expect(putCalls).toHaveLength(2);
  const lastPutIndex = calls.lastIndexOf(putCalls[1]);
  const renameIndex = calls.findIndex((c) => c.method === "POST" && c.url === "/api/v1/kb/rename");
  expect(lastPutIndex).toBeLessThan(renameIndex);
});

// Bug: flushForHandoff's own onError sets errorMessage+saveState:"error" (the
// generic autosave-failure banner) BEFORE handleRename sets renameError and
// aborts — so a failed pre-flush rendered BOTH the generic "put boom" banner
// AND the "rename cancelled" banner at once. Only one banner should show;
// the specific rename-abort message takes priority over the generic one.
test("a failed pre-flush rename shows exactly one red banner (the rename-abort message, not the duplicate generic autosave one)", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "PUT") return Promise.resolve(errorResponse(500, "put boom"));
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

  expect(await screen.findByText(/rename cancelled/i)).toBeInTheDocument();
  // The generic autosave-error banner (surfacing the raw "put boom" message)
  // must be suppressed while the rename-specific banner is shown — not a
  // second banner stacked underneath it.
  expect(screen.queryByText("put boom")).not.toBeInTheDocument();

  // Belt-and-braces: exactly one danger banner element is present, not two
  // stacked border-danger divs.
  const banners = document.querySelectorAll(".border-danger\\/30");
  expect(banners).toHaveLength(1);
});

// Task-4 review follow-up: markDirty must clear a stale renameError, or a
// LATER, distinct autosave failure gets silently swallowed by the render
// gate (`errorMessage && saveState === "error" && !renameError`) — the old
// rename-abort banner would keep suppressing it forever.
test("rename-abort: typing again clears the stale renameError, so a later distinct autosave failure shows the generic banner", async () => {
  let putShouldFail = true;
  let putMessage = "put boom";
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "PUT") {
        return Promise.resolve(putShouldFail ? errorResponse(500, putMessage) : jsonResponse({ ok: true }));
      }
      if (url.startsWith("/api/v1/kb/note")) return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup({ delay: null });
  renderAtPath("notes/trip plan.md");

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(textarea);
  await user.type(textarea, "extra");

  // Trigger the rename-abort path: pre-flush PUT fails, rename never fires.
  const titleInput = screen.getByLabelText("Note title");
  await user.clear(titleInput);
  await user.type(titleInput, "summer");
  await user.keyboard("{Enter}");

  expect(await screen.findByText(/rename cancelled/i)).toBeInTheDocument();

  // Resume editing — markDirty must clear the stale rename-abort banner.
  putMessage = "second boom";
  await user.click(textarea);
  await user.type(textarea, " more");
  expect(screen.queryByText(/rename cancelled/i)).not.toBeInTheDocument();

  // A later, distinct autosave failure must render ITS OWN banner — not be
  // suppressed by the (now-cleared) stale renameError.
  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  expect(await screen.findByText("second boom")).toBeInTheDocument();
});

// Re-review minor: a failed delete must re-arm the dirty/"Unsaved" contract
// for the edit it discarded — otherwise the chip lies "saved" and Ctrl+S
// silently no-ops for content that was never actually persisted anywhere.
test("a failed delete re-arms dirtyRef for the edit it discarded (chip reports dirty, Ctrl+S retries)", async () => {
  const putBodies: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "DELETE") return Promise.resolve(errorResponse(500, "boom"));
      if (method === "PUT") {
        putBodies.push(JSON.parse(String(init?.body)).content);
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url.startsWith("/api/v1/kb/note")) return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const states: string[] = [];
  const user = userEvent.setup();
  const qc = new QueryClient();
  render(
    <MemoryRouter initialEntries={["/?path=notes%2Ftrip%20plan.md"]}>
      <QueryClientProvider client={qc}>
        <NoteEditor path="notes/trip plan.md" onStateChange={(s) => states.push(s)} />
      </QueryClientProvider>
    </MemoryRouter>,
  );

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(textarea);
  await user.type(textarea, "extra");

  await user.click(screen.getByLabelText("Note actions"));
  await user.click(await screen.findByText("Delete…"));
  await user.click(screen.getByRole("button", { name: "Delete" }));

  await screen.findByText("Delete failed: boom");
  expect(states[states.length - 1]).toBe("dirty");

  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  await waitFor(() => expect(putBodies).toHaveLength(1));
  expect(putBodies[0]).toContain("extra");
});

// SP3 final review, item 1: deleting the currently-open note from the
// FileTree row (NOT via the header's own delete) used to leave dirtyRef
// (possibly stuck true from an earlier failed autosave) and the
// suppression ref untouched — a later unmount would fire the unmount-flush
// PUT and resurrect the file the tree-delete had just removed.
test("the open note vanishing elsewhere (tree-delete) disarms dirty/suppression and shows a dedicated notice — zero PUTs ever fire", async () => {
  let noteExists = true;
  const calls: Array<{ method: string; url: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (method === "PUT") return Promise.resolve(jsonResponse({ ok: true }));
      if (url.startsWith("/api/v1/kb/note")) {
        if (!noteExists) return Promise.resolve(errorResponse(404, "not found"));
        return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const user = userEvent.setup();
  renderAtPath("notes/trip plan.md", qc);

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(textarea);
  // Simulate a stuck-dirty edit (e.g. from an earlier failed autosave) —
  // it's still dirty, but no debounce/Ctrl+S has fired yet.
  await user.type(textarea, "extra");
  expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);

  // The note is deleted elsewhere (a FileTree row) — simulate the query
  // refetching into a 404, exactly as invalidating the tree query would
  // eventually surface here.
  noteExists = false;
  await qc.refetchQueries({ queryKey: ["kb-note", "notes/trip plan.md"] });

  expect(await screen.findByText(/deleted elsewhere/i)).toBeInTheDocument();

  // Past the 1s autosave debounce window — if disarming hadn't cancelled
  // the timer and cleared dirtyRef, a stray PUT would land right about now.
  await new Promise((r) => setTimeout(r, 1100));
  expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
}, 10000);

// Delta review: gating the disarm on bare `isError` (rather than
// specifically a 404) meant a TRANSIENT refetch failure — e.g. `make
// deploy` restarting the server mid-edit, which the operator's daily loop
// hits routinely — falsely showed "deleted elsewhere" AND discarded the
// dirty edit. A non-404 error must leave dirtyRef/the debounce machinery
// untouched, exactly as it did before the vanish fix existed.
test("a transient (non-404) refetch error does NOT disarm the editor — the dirty edit survives and a later flush still PUTs it", async () => {
  let noteShouldFail = false;
  const putBodies: string[] = [];
  const calls: Array<{ method: string; url: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (method === "PUT") {
        putBodies.push(JSON.parse(String(init?.body)).content);
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url.startsWith("/api/v1/kb/note")) {
        if (noteShouldFail) return Promise.resolve(errorResponse(500, "server restarting"));
        return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderAtPath("notes/trip plan.md", qc);

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  const user = userEvent.setup();
  await user.click(textarea);
  await user.type(textarea, "extra");

  // A transient server hiccup — the note refetches (a successful autosave
  // would also trigger this, via its own query invalidation) and gets a
  // 500, NOT a 404.
  noteShouldFail = true;
  await qc.refetchQueries({ queryKey: ["kb-note", "notes/trip plan.md"] });
  await waitFor(() =>
    expect(qc.getQueryState(["kb-note", "notes/trip plan.md"])?.status).toBe("error"),
  );

  expect(screen.queryByText(/deleted elsewhere/i)).not.toBeInTheDocument();

  // The server recovers — Ctrl+S must still issue a PUT with the edit
  // intact, proving dirtyRef/the debounce machinery were never disarmed.
  noteShouldFail = false;
  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  await waitFor(() => expect(putBodies).toHaveLength(1));
  expect(putBodies[0]).toContain("extra");
});

// SP3 final review, item 2: flushForHandoff used to ignore an
// already-in-flight save (e.g. the natural debounce, or a Ctrl+S the user
// fired moments before renaming). It would fire a SECOND concurrent PUT,
// and the earlier one could land server-side AFTER the rename's POST,
// resurrecting the old path (an upsert).
test("flushForHandoff awaits an in-flight save first — no second concurrent PUT when nothing new was typed", async () => {
  const putDeferreds: Array<ReturnType<typeof deferred<Response>>> = [];
  const calls: Array<{ method: string; url: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (method === "PUT") {
        const d = deferred<Response>();
        putDeferreds.push(d);
        return d.promise;
      }
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

  // Force an in-flight PUT immediately (Ctrl+S) instead of waiting out the
  // 1s debounce — it stays pending until its deferred is resolved.
  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  await waitFor(() => expect(putDeferreds).toHaveLength(1));

  // Rename WHILE that PUT is still in flight.
  const titleInput = screen.getByLabelText("Note title");
  await user.clear(titleInput);
  await user.type(titleInput, "summer");
  await user.keyboard("{Enter}");

  // handleRename's flushForHandoff must be awaiting the in-flight promise
  // right now, NOT firing a second PUT.
  await new Promise((r) => setTimeout(r, 50));
  expect(putDeferreds).toHaveLength(1);
  expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(false);

  // Content didn't change mid-flight, so resolving it clears dirtyRef —
  // flushForHandoff finds nothing left to flush and proceeds straight to
  // the rename with no second PUT.
  putDeferreds[0].resolve(jsonResponse({ ok: true }));

  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(true),
  );
  expect(putDeferreds).toHaveLength(1);
  const putIndex = calls.findIndex((c) => c.method === "PUT");
  const renameIndex = calls.findIndex((c) => c.method === "POST" && c.url === "/api/v1/kb/rename");
  expect(putIndex).toBeLessThan(renameIndex);
});

test("flushForHandoff issues exactly one more PUT when content changed while the earlier save was in flight", async () => {
  const putDeferreds: Array<ReturnType<typeof deferred<Response>>> = [];
  const putBodies: string[] = [];
  const calls: Array<{ method: string; url: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (method === "PUT") {
        putBodies.push(JSON.parse(String(init?.body)).content);
        const d = deferred<Response>();
        putDeferreds.push(d);
        return d.promise;
      }
      if (method === "POST" && url === "/api/v1/kb/rename") return Promise.resolve(jsonResponse({ ok: true }));
      if (url.startsWith("/api/v1/kb/note")) return Promise.resolve(jsonResponse(TRIP_NOTE_FIXTURE));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup({ delay: null });
  renderAtPath("notes/trip plan.md");

  const textarea = await screen.findByRole("textbox", { name: "Raw markdown" });
  await user.click(textarea);
  await user.type(textarea, "first");

  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  await waitFor(() => expect(putDeferreds).toHaveLength(1));

  // More content lands WHILE that first PUT is still in flight.
  await user.type(textarea, "-more");

  const titleInput = screen.getByLabelText("Note title");
  await user.clear(titleInput);
  await user.type(titleInput, "summer");
  await user.keyboard("{Enter}");

  await new Promise((r) => setTimeout(r, 50));
  expect(putDeferreds).toHaveLength(1); // still just the original in-flight one

  // Resolve the first PUT — its content snapshot is stale (missing
  // "-more"), so flushForHandoff must issue exactly one more PUT with the
  // latest content before the rename.
  putDeferreds[0].resolve(jsonResponse({ ok: true }));

  await waitFor(() => expect(putDeferreds).toHaveLength(2));
  expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(false);
  putDeferreds[1].resolve(jsonResponse({ ok: true }));

  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/kb/rename")).toBe(true),
  );
  expect(putDeferreds).toHaveLength(2);
  expect(putBodies[1]).toContain("-more");
  // Both PUTs settled strictly before the rename POST (a GET for the
  // renamed path follows it once the remount fires — irrelevant here).
  const renameIndex = calls.findIndex((c) => c.method === "POST" && c.url === "/api/v1/kb/rename");
  const putIndices = calls.reduce<number[]>((acc, c, i) => (c.method === "PUT" ? [...acc, i] : acc), []);
  expect(putIndices).toHaveLength(2);
  expect(Math.max(...putIndices)).toBeLessThan(renameIndex);
});

// web/api_kb.go's apiSaveKBNote refuses a save to an agent's state.md while
// that agent has a run in flight (409 {"error":{"code":"agent_running",...}})
// so the manual edit doesn't race the runner's own end-of-run write. This is
// the generic "a failed autosave keeps the edit dirty" contract exercised
// against that SPECIFIC envelope: the banner must show the server's message
// verbatim, and — same as any other save failure — the buffer must stay
// dirty (not silently marked clean) so the user's edit survives and Ctrl/Cmd+S
// retries once the run finishes.
test("a 409 agent_running save shows the server message and leaves the edit dirty", async () => {
  const AGENT_RUNNING_MESSAGE =
    "this agent is running right now — its state.md will be overwritten when the run finishes. Wait for it to finish, then save your edit.";
  let putCount = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT") {
        putCount += 1;
        return Promise.resolve(
          new Response(
            JSON.stringify({ error: { code: "agent_running", message: AGENT_RUNNING_MESSAGE } }),
            { status: 409, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "agents/agent-1/state.md",
            content: "# State\n\n<!-- placeholder -->\n",
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
        <NoteEditor path="agents/agent-1/state.md" onStateChange={(s) => states.push(s)} />
      </QueryClientProvider>
    </MemoryRouter>,
  );

  await waitFor(() => expect(screen.getByText(/protect formatting/)).toBeInTheDocument());
  const textarea = screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement;
  await user.click(textarea);
  await user.type(textarea, "extra");

  // The 1000ms debounce fires the PUT, which 409s.
  await waitFor(() => expect(putCount).toBe(1), { timeout: 3000 });
  await waitFor(() => expect(states[states.length - 1]).toBe("error"));

  // The 409's message is surfaced verbatim in the inline banner...
  await waitFor(() => expect(screen.getByText(AGENT_RUNNING_MESSAGE)).toBeInTheDocument());
  // ...and the user's edit is still in the textarea, not reverted/cleared.
  expect(textarea.value).toContain("extra");

  // The buffer is still dirty (not silently marked clean by the rejection):
  // Ctrl/Cmd+S issues a fresh retry PUT instead of being a silent no-op.
  fireEvent.keyDown(window, { key: "s", ctrlKey: true });
  await waitFor(() => expect(putCount).toBe(2));
});
