import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import WorkspaceMenu from "./WorkspaceMenu";

const session = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "personal", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [
    { id: "w1", name: "personal", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
    { id: "w2", name: "new-biz", about: "", needs_setup: true, created_at: "2026-01-02T00:00:00Z" },
    { id: "w3", name: "other", about: "", needs_setup: false, created_at: "2026-01-03T00:00:00Z" },
  ],
};

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <WorkspaceMenu />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("switching to a needs_setup workspace enters without a master-password prompt", async () => {
  const enterCalls: unknown[] = [];
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url);
    if (u.endsWith("/auth/session"))
      return Promise.resolve(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }));
    if (u.endsWith("/workspaces/w2/enter")) {
      enterCalls.push(init?.body ? JSON.parse(String(init.body)) : undefined);
      return Promise.resolve(new Response(JSON.stringify({ ok: true, needs_setup: true }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }
    return Promise.resolve(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  });
  vi.stubGlobal("fetch", fetchMock);
  // jsdom doesn't implement navigation; the component sets window.location.href
  // on success, which we don't need to follow — just observe the call was made.
  vi.spyOn(window, "location", "get").mockReturnValue({ href: "" } as unknown as Location);

  wrap();
  await userEvent.click(await screen.findByLabelText("Workspace"));
  await userEvent.click(await screen.findByText(/switch to new-biz/i));

  await waitFor(() => expect(enterCalls).toHaveLength(1));
  expect(enterCalls[0]).toEqual({});
  expect(screen.queryByLabelText(/master password/i)).not.toBeInTheDocument();
});

// Two bugs shipped together here. (1) EnterWorkspaceDialog navigated on
// success but never closed itself — and from WorkspaceMenu the dialog lives in
// the always-mounted icon rail, so nav("/") doesn't unmount it and the
// master-password modal stayed up over the workspace you'd just entered.
// (2) Query keys are per-RESOURCE, not per-workspace, so the previous
// workspace's cached rows kept rendering until each query happened to refetch.
test("entering a workspace closes the dialog and drops the previous workspace's cached data", async () => {
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL) => {
    const u = String(url);
    const ok = (b: unknown) =>
      Promise.resolve(new Response(JSON.stringify(b), { status: 200, headers: { "Content-Type": "application/json" } }));
    if (u.endsWith("/auth/session")) return ok(session);
    if (u.endsWith("/workspaces/w3/enter")) return ok({ ok: true, needs_setup: false });
    return ok({});
  });
  vi.stubGlobal("fetch", fetchMock);

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Stand-in for any tenant-scoped cache entry (agents, kb-tree, secrets, …).
  qc.setQueryData(["agents"], { agents: [{ id: "a1", name: "old-workspace-agent" }] });

  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <WorkspaceMenu />
      </MemoryRouter>
    </QueryClientProvider>,
  );

  await userEvent.click(await screen.findByLabelText("Workspace"));
  await userEvent.click(await screen.findByText(/switch to other/i));

  const pw = await screen.findByLabelText(/master password/i);
  await userEvent.type(pw, "correct-horse");
  await userEvent.click(screen.getByRole("button", { name: /enter workspace/i }));

  await waitFor(() => expect(screen.queryByLabelText(/master password/i)).not.toBeInTheDocument());
  expect(qc.getQueryData(["agents"])).toBeUndefined();
  // The session itself is invalidated, not evicted — RequireAuth renders a
  // full-page loader while it's pending, so dropping it would flash the app.
  expect(qc.getQueryData(["session"])).toBeDefined();
});
