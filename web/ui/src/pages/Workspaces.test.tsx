import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import Workspaces from "./Workspaces";

const session = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: null,
  workspaces: [
    { id: "w1", name: "personal", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  ],
};

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Workspaces />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("enter flow prompts for master password and surfaces wrong-password error", async () => {
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, _init?: RequestInit) => {
    const u = String(url);
    if (u.endsWith("/auth/session"))
      return Promise.resolve(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }));
    if (u.endsWith("/workspaces/w1/enter"))
      return Promise.resolve(
        new Response(JSON.stringify({ error: { code: "wrong_master_password", message: "Incorrect master password" } }), {
          status: 401, headers: { "Content-Type": "application/json" },
        }),
      );
    return Promise.resolve(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  });
  vi.stubGlobal("fetch", fetchMock);

  wrap();
  await userEvent.click(await screen.findByText("personal"));
  // password dialog appears
  const pw = await screen.findByLabelText(/master password/i);
  await userEvent.type(pw, "nope");
  await userEvent.click(screen.getByRole("button", { name: /enter/i }));
  await waitFor(() => expect(screen.getByText(/incorrect master password/i)).toBeInTheDocument());
});

// A needs_setup workspace has no master password yet — clicking it enters
// directly (no dialog) and navigates home, where RequireAuth routes to
// /setup. The old "finish setup in the classic UI" banner + ?setup= param
// handling is gone now that the SPA has its own onboarding wizard.
test("needs_setup workspace enters directly without a master-password prompt", async () => {
  const needsSetupSession = {
    ...session,
    workspaces: [
      { id: "w2", name: "fresh", about: "", needs_setup: true, created_at: "2026-01-01T00:00:00Z" },
    ],
  };
  let enterCalled = false;
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL) => {
    const u = String(url);
    if (u.endsWith("/auth/session"))
      return Promise.resolve(
        new Response(JSON.stringify(needsSetupSession), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    if (u.endsWith("/workspaces/w2/enter")) {
      enterCalled = true;
      return Promise.resolve(
        new Response(JSON.stringify({ ok: true, needs_setup: true }), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    }
    return Promise.resolve(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  });
  vi.stubGlobal("fetch", fetchMock);

  wrap();
  await userEvent.click(await screen.findByText("fresh"));

  await waitFor(() => expect(enterCalled).toBe(true));
  expect(screen.queryByLabelText(/master password/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/finish setup/i)).not.toBeInTheDocument();
});
