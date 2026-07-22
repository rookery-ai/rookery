import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useRecentFiles, recentStorageKey, readRecent } from "./useRecentFiles";

// Switching workspaces is a first-class action in this app (the owner re-enters
// a master password each time), and the session query is what reports which one
// is active. These tests drive that transition through the hook, because the
// dangerous part is not the key derivation — it is the ORDER of the load and
// save effects. A save that runs before the reload would write the outgoing
// workspace's list (or the empty initial state) under the incoming workspace's
// key, silently destroying real history.

function sessionFor(workspaceID: string) {
  return {
    authenticated: true,
    owner: { id: "o1", username: "admin", must_change_password: false },
    workspace: { id: workspaceID, name: workspaceID, about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
    workspaces: [],
  };
}

function mockSession(workspaceID: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(sessionFor(workspaceID)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
}

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  localStorage.clear();
});

test("loads the active workspace's history, not another workspace's", async () => {
  localStorage.setItem(recentStorageKey("ws-a"), JSON.stringify([{ path: "notes/a.md", title: "a" }]));
  localStorage.setItem(recentStorageKey("ws-b"), JSON.stringify([{ path: "notes/b.md", title: "b" }]));
  mockSession("ws-b");

  const { result } = renderHook(() => useRecentFiles(), { wrapper: wrapper() });

  await waitFor(() => expect(result.current.recent.map((e) => e.path)).toEqual(["notes/b.md"]));
});

test("does not clobber the stored list with the empty initial state before the session resolves", async () => {
  localStorage.setItem(recentStorageKey("ws-a"), JSON.stringify([{ path: "notes/a.md", title: "a" }]));
  mockSession("ws-a");

  const { result } = renderHook(() => useRecentFiles(), { wrapper: wrapper() });

  // The hook starts with an empty list and only learns the workspace id once the
  // session query settles. If the persist effect fired during that window, the
  // stored history would already be gone.
  await waitFor(() => expect(result.current.recent).toHaveLength(1));
  expect(readRecent("ws-a").map((e) => e.path)).toEqual(["notes/a.md"]);
});

test("recording writes under the active workspace's key only", async () => {
  localStorage.setItem(recentStorageKey("ws-a"), JSON.stringify([{ path: "notes/seed.md", title: "seed" }]));
  localStorage.setItem(recentStorageKey("ws-other"), JSON.stringify([{ path: "notes/other.md", title: "other" }]));
  mockSession("ws-a");

  const { result } = renderHook(() => useRecentFiles(), { wrapper: wrapper() });
  // Wait for the workspace's OWN history to load before recording. An assertion
  // on the empty list would pass instantly against the pre-session initial
  // state, and the record would then be discarded by the load effect — a race
  // the real UI cannot hit (the file tree the user clicks does not render until
  // the session has resolved) but which would make this test meaningless.
  await waitFor(() => expect(result.current.recent.map((e) => e.path)).toEqual(["notes/seed.md"]));

  act(() => result.current.record({ path: "notes/new.md", title: "new" }));

  await waitFor(() =>
    expect(readRecent("ws-a").map((e) => e.path)).toEqual(["notes/new.md", "notes/seed.md"]),
  );
  // The other workspace's history is untouched.
  expect(readRecent("ws-other").map((e) => e.path)).toEqual(["notes/other.md"]);
});
