import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import KBPage from "./KBPage";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderAtPath(initialEntry: string) {
  // retry:false — matches the established pattern elsewhere in this suite
  // (tree.test.tsx, NoteEditor.test.tsx, lib/*.test.ts). Without it, a
  // query that errors (the delete-navigation tests deliberately fail the
  // parent-directory note fetch to reproduce the regression) retries with
  // backoff and the assertion times out mid-retry instead of observing the
  // settled error state.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <KBPage />
          <ToastHost />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

// Review fix: KBPage used to decide "does this path open a document pane at
// all" from a filename heuristic (last segment contains a dot). An agent
// legitimately writes extensionless files (a skill script named `run`,
// Makefile, Dockerfile, LICENSE, a shebang shim) — the backend sniffs these
// correctly into kind "code", but the old heuristic never even asked, so
// clicking one in the tree did nothing (useKBNote never called, empty state
// rendered, no error). This is the case that must open.
test("an extensionless path opens the file viewer, not the empty state", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "agents/x/tools/run",
            content: "#!/bin/sh\necho hi\n",
            html: "",
            backlinks: [],
            kind: "code",
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderAtPath("/?path=agents%2Fx%2Ftools%2Frun");

  // The file viewer's breadcrumb renders the filename once the note loads.
  expect(await screen.findByText("run")).toBeInTheDocument();
  expect(screen.queryByText(/select a note or create one/i)).not.toBeInTheDocument();
});

// A path can contain a dot and still be a directory (e.g. a dotted config
// dir name). The routed `dir=1` hint — not a filename guess — is what must
// decide this, so a dotted directory name must NOT attempt to open as a
// file (no note fetch at all). A directory now opens the FolderPage (its
// children as a page) rather than the bare empty state — but the invariant
// that matters here is unchanged: it's fetched via /kb/tree, never /kb/note.
test("a directory path (routed via the dir hint) opens the folder page and never fetches a note", async () => {
  const noteFetches: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) noteFetches.push(url);
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderAtPath("/?path=agents%2Fsite.config&dir=1");

  // FolderPage renders the folder's name as its title (derived from the path
  // basename when the parent listing has no display name).
  expect(await screen.findByRole("heading", { name: "site.config" })).toBeInTheDocument();
  expect(noteFetches).toHaveLength(0);
});

test("a markdown path still opens the rich-text editor", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "notes/todo.md",
            content: "# Todo\n",
            html: "",
            backlinks: [],
            kind: "markdown",
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderAtPath("/?path=notes%2Ftodo.md");

  await waitFor(() => expect(screen.queryByText(/select a note or create one/i)).not.toBeInTheDocument());
  // NoteEditor's title input carries the filename minus ".md".
  expect(await screen.findByDisplayValue("todo")).toBeInTheDocument();
});

// Review fix: NoteEditor's and FileViewer's handleDelete onSuccess navigate
// to the parent directory (`path.split("/").slice(0,-1).join("/")`) but
// didn't carry the `dir=1` hint this same commit introduced everywhere
// else (breadcrumbs, FileTree, SearchBox). Without it, KBPage's new
// default — attempt to open — fetches the parent DIRECTORY as if it were a
// note. Almost every real vault file lives under a subdirectory, so this
// regressed "delete a file" from "return to the tree" to an error screen
// for nearly every delete. These two tests exercise the integration seam
// (KBPage, not the component standalone) because that's the only level
// where the *navigation outcome* — what renders in the content pane next —
// is observable; a standalone-component test can only see that DELETE
// fired, not what happens after.
//
// The mock fails any note fetch for a path other than the exact file being
// deleted (mirroring the real backend: os.ReadFile on a directory errors),
// so if the fix regresses, this reproduces the reviewer's exact repro:
// a second /api/v1/kb/note?path=<parent> fetch and a "Couldn't load this
// file." render.
function mockDeleteFetch(fullPath: string, kind: "code" | "markdown", content: string) {
  const noteFetches: string[] = [];
  const deleteCalls: string[] = [];
  const fn = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method === "DELETE") {
      deleteCalls.push(url);
      return Promise.resolve(jsonResponse({ ok: true }));
    }
    if (url.startsWith("/api/v1/kb/note")) {
      noteFetches.push(url);
      const requested = new URL(url, "http://localhost").searchParams.get("path");
      if (requested === fullPath) {
        return Promise.resolve(
          jsonResponse({ path: fullPath, content, html: "", backlinks: [], kind }),
        );
      }
      // Any other note fetch — i.e. a fetch for the parent directory, which
      // only happens if the `dir=1` hint was dropped — fails the way the
      // real backend fails reading a directory as a file.
      return Promise.resolve(
        new Response(
          JSON.stringify({ error: { code: "internal", message: "is a directory" } }),
          { status: 500, headers: { "Content-Type": "application/json" } },
        ),
      );
    }
    return Promise.resolve(jsonResponse({}));
  });
  return { fn, noteFetches, deleteCalls };
}

test("deleting a nested code file (FileViewer) returns to the tree, never re-fetching the parent as a note", async () => {
  const fullPath = "agents/demo/tools/script.py";
  const parentPath = "agents/demo/tools";
  const { fn, noteFetches, deleteCalls } = mockDeleteFetch(fullPath, "code", "print('hi')\n");
  vi.stubGlobal("fetch", fn);

  const user = userEvent.setup();
  renderAtPath(`/?path=${encodeURIComponent(fullPath)}`);

  await screen.findByText("script.py");
  await user.click(screen.getByLabelText(/file actions/i));
  await user.click(await screen.findByText("Delete…"));
  await user.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() => expect(deleteCalls).toHaveLength(1));
  // Delete navigates to the parent folder, which now opens as the FolderPage.
  await screen.findByText(/this folder is empty/i);

  expect(screen.queryByText(/couldn't load this file/i)).not.toBeInTheDocument();
  const parentFetched = noteFetches.some(
    (u) => new URL(u, "http://localhost").searchParams.get("path") === parentPath,
  );
  expect(parentFetched).toBe(false);
});

test("deleting a nested markdown note (NoteEditor) returns to the tree, never re-fetching the parent as a note", async () => {
  const fullPath = "notes/todo.md";
  const parentPath = "notes";
  const { fn, noteFetches, deleteCalls } = mockDeleteFetch(fullPath, "markdown", "# Todo\n");
  vi.stubGlobal("fetch", fn);

  const user = userEvent.setup();
  renderAtPath(`/?path=${encodeURIComponent(fullPath)}`);

  await screen.findByDisplayValue("todo");
  await user.click(screen.getByLabelText(/note actions/i));
  await user.click(await screen.findByText("Delete…"));
  await user.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() => expect(deleteCalls).toHaveLength(1));
  // Delete navigates to the parent folder, which now opens as the FolderPage.
  await screen.findByText(/this folder is empty/i);

  expect(screen.queryByText(/couldn't load this file/i)).not.toBeInTheDocument();
  const parentFetched = noteFetches.some(
    (u) => new URL(u, "http://localhost").searchParams.get("path") === parentPath,
  );
  expect(parentFetched).toBe(false);
});

// ── ?new=note (the ⌘K palette's "New note" action lands here) ───────────────

// Rendered inside AppShell, unlike the tests above: the new-note dialog lives
// in the CONTEXT PANE header, and <ContextPane> renders null on its own — it
// hands its children to AppShell, which is what actually mounts them.
function renderInShell(initialEntry: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/kb" element={<KBPage />} />
            </Route>
          </Routes>
          <ToastHost />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

test("landing on /kb?new=note opens the new-note dialog", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/auth/session") {
        return Promise.resolve(
          jsonResponse({
            authenticated: true,
            owner: { id: "o1", username: "admin", must_change_password: false },
            workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
            workspaces: [],
          }),
        );
      }
      if (url.startsWith("/api/v1/kb/tree")) return Promise.resolve(jsonResponse({ tree: [] }));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderInShell("/kb?new=note");

  // The dialog is the KB's only real create affordance; the palette action
  // used to navigate to bare /kb, which opens the page and creates nothing.
  expect(await screen.findByRole("dialog")).toBeInTheDocument();
  expect(await screen.findByText("New note")).toBeInTheDocument();
});

// The real timing: useRecentFiles waits for the session query's workspace id,
// so the recents list arrives a tick AFTER first render — which is why the
// auto-open used to win the race against the ?new=note intent and open an
// unrelated note behind the dialog. When that recents entry was stale the
// editor rendered "Couldn't load this note.", the reported symptom.
test("landing on /kb?new=note does not auto-open the last recent note", async () => {
  localStorage.setItem(
    "sa.kb.recent.w1",
    JSON.stringify([{ path: "notes/stale.md", title: "stale" }]),
  );
  const noteFetches: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/auth/session") {
        return Promise.resolve(
          jsonResponse({
            authenticated: true,
            owner: { id: "o1", username: "admin", must_change_password: false },
            workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
            workspaces: [],
          }),
        );
      }
      if (url.startsWith("/api/v1/kb/note")) {
        noteFetches.push(url);
        return Promise.resolve(
          new Response(JSON.stringify({ error: { code: "not_found", message: "gone" } }), { status: 404 }),
        );
      }
      if (url.startsWith("/api/v1/kb/tree")) return Promise.resolve(jsonResponse({ path: "", nodes: [], order: [] }));
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderInShell("/kb?new=note");

  expect(await screen.findByRole("dialog")).toBeInTheDocument();
  // Wait for the recents strip to render the entry rather than a fixed delay:
  // that is the observable proof the list has loaded, which is the exact moment
  // the auto-open effect would have fired. A timeout instead would let this
  // test pass vacuously by finishing before the race even happens.
  expect(await screen.findByTitle("notes/stale.md")).toBeInTheDocument();
  // …then let the navigation the auto-open would have triggered settle. The
  // findByTitle above is the gate that the race has actually been reached; this
  // window is what lets its CONSEQUENCE (a note fetch) show up if it happens.
  await new Promise((r) => setTimeout(r, 200));
  expect(noteFetches).toHaveLength(0);
  expect(screen.queryByText(/couldn't load this note/i)).not.toBeInTheDocument();

  localStorage.clear();
});
