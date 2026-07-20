import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import KBPage from "./KBPage";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderAtPath(initialEntry: string) {
  const qc = new QueryClient();
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={qc}>
        <KBPage />
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
// file (no note fetch at all).
test("a directory path (routed via the dir hint) resolves to the empty state and never fetches a note", async () => {
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

  await screen.findByText(/select a note or create one/i);
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
