import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import { AppShell } from "@/components/shell/AppShell";
import KBPage from "./KBPage";
import { recentStorageKey, readRecent, type RecentFile } from "./useRecentFiles";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

// A vault with one folder and one root note, plus a note endpoint that serves
// anything asked for — enough to drive open/record/auto-open.
function mockVault(opts?: { missing?: string[] }) {
  const missing = new Set(opts?.missing ?? []);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/kb/tree?path=") {
        return Promise.resolve(
          jsonResponse({
            path: "",
            nodes: [
              { name: "notes", display_name: "Notes", path: "notes", is_dir: true, system: false },
              { name: "README.md", display_name: "README.md", path: "README.md", is_dir: false, system: false },
            ],
          }),
        );
      }
      if (url.startsWith("/api/v1/kb/tree")) return Promise.resolve(jsonResponse({ path: "", nodes: [] }));
      // AppShell's own chrome — only needed by the tests that mount the shell
      // to reach the context pane.
      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/inbox/poll") return Promise.resolve(jsonResponse({ unread: 0, recent: [] }));
      if (url.startsWith("/api/v1/kb/note")) {
        const path = decodeURIComponent(new URL(url, "http://x").searchParams.get("path") ?? "");
        if (missing.has(path)) return Promise.resolve(jsonResponse({ error: "not_found" }, 404));
        return Promise.resolve(
          jsonResponse({ path, content: "# Hello\n\nbody\n", html: "", backlinks: [], kind: "markdown" }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function renderKB(initialEntry = "/") {
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

// The KB context pane (the file tree and the Recent strip) is rendered through
// AppShell's ContextPane slot, so it only exists when the shell is mounted.
// renderKB above is enough for the document pane; anything asserting on the
// pane itself has to go through here.
function renderKBInShell(initialEntry = "/") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/" element={<KBPage />} />
            </Route>
          </Routes>
          <ToastHost />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

// Seeds under the workspace id SESSION_FIXTURE reports, since the history is
// scoped per workspace.
const WS = SESSION_FIXTURE.workspace.id;

function seedRecent(entries: RecentFile[]) {
  localStorage.setItem(recentStorageKey(WS), JSON.stringify(entries));
}

beforeEach(() => {
  localStorage.clear();
});

// The reported symptom: opening the knowledge base showed nothing at all.
test("with no history the empty state still shows and no Recent section is rendered", async () => {
  mockVault();
  renderKB("/");

  expect(await screen.findByText("Select a note or create one.")).toBeInTheDocument();
  // An empty "Recent" heading over blank space is worse than no section.
  expect(screen.queryByText("Recent")).not.toBeInTheDocument();
});

test("clicking a file in the tree records it under Recent", async () => {
  mockVault();
  renderKBInShell("/");

  await userEvent.click(await screen.findByText("README.md"));

  // The heading appears and the file is listed under it.
  expect(await screen.findByText("Recent")).toBeInTheDocument();
  await waitFor(() => expect(readRecent(WS).map((e) => e.path)).toEqual(["README.md"]));
});

// The list is a VIEW history — a directory is not a document and must never
// enter it, or "recent" fills up with folders the user merely expanded.
test("expanding a directory does not record it", async () => {
  mockVault();
  renderKBInShell("/");

  await userEvent.click(await screen.findByText("Notes"));

  await waitFor(() => expect(screen.getByText("Notes")).toBeInTheDocument());
  expect(readRecent(WS)).toEqual([]);
});

// The second half of the requirement: the last-viewed note opens in the
// document pane when the knowledge base is opened with no explicit path.
test("landing on the KB with no path auto-opens the most recent file", async () => {
  seedRecent([
    { path: "notes/trip.md", title: "trip" },
    { path: "README.md", title: "README.md" },
  ]);
  mockVault();
  renderKB("/");

  await waitFor(() => {
    const noteCalls = vi
      .mocked(fetch)
      .mock.calls.map((c) => String(c[0]))
      .filter((u) => u.startsWith("/api/v1/kb/note"));
    expect(noteCalls.some((u) => u.includes("notes%2Ftrip.md"))).toBe(true);
  });
  expect(screen.queryByText("Select a note or create one.")).not.toBeInTheDocument();
});

// An explicit path in the URL is the user's intent and must win over history.
test("an explicit path is not overridden by the recents auto-open", async () => {
  seedRecent([{ path: "notes/trip.md", title: "trip" }]);
  mockVault();
  renderKB("/?path=README.md");

  await waitFor(() => {
    const noteCalls = vi
      .mocked(fetch)
      .mock.calls.map((c) => String(c[0]))
      .filter((u) => u.startsWith("/api/v1/kb/note"));
    expect(noteCalls.some((u) => u.includes("README.md"))).toBe(true);
    expect(noteCalls.some((u) => u.includes("trip.md"))).toBe(false);
  });
});

// A file deleted or renamed outside this UI would otherwise sit in the list
// forever AND be auto-opened on every single visit — the worst failure mode
// for this feature, since it makes the KB open on a broken note every time.
test("a recents entry whose file has vanished is dropped and the next one opens", async () => {
  seedRecent([
    { path: "notes/gone.md", title: "gone" },
    { path: "README.md", title: "README.md" },
  ]);
  mockVault({ missing: ["notes/gone.md"] });
  renderKB("/");

  await waitFor(() => expect(readRecent(WS).map((e) => e.path)).toEqual(["README.md"]));
  await waitFor(() => {
    const noteCalls = vi
      .mocked(fetch)
      .mock.calls.map((c) => String(c[0]))
      .filter((u) => u.startsWith("/api/v1/kb/note"));
    expect(noteCalls.some((u) => u.includes("README.md"))).toBe(true);
  });
});
