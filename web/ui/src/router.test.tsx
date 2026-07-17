import { render, screen } from "@testing-library/react";

// RequireAuth must route an authenticated owner whose active workspace still
// needs onboarding to the full-screen setup wizard instead of rendering the
// shell — mirrors the no-workspace / must-change-password guards. The
// wizard route (RequireSetupWorkspace) then mounts and immediately fires its
// own GET /api/v1/setup, which the shared fetch stub below also answers.
test("redirects an authenticated needs_setup workspace to the setup wizard", async () => {
  vi.resetModules();
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      const sessionBody = {
        authenticated: true,
        owner: { id: "1", username: "admin", must_change_password: false },
        workspace: {
          id: "ws1", name: "Test WS", about: "", needs_setup: true, created_at: "",
        },
        workspaces: [
          { id: "ws1", name: "Test WS", about: "", needs_setup: true, created_at: "" },
        ],
      };
      const body = url === "/api/v1/auth/session" ? sessionBody : { step: 1, needs_setup: true };
      return Promise.resolve(
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }),
  );
  window.history.pushState({}, "", "/app/");
  const { default: App } = await import("./App");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /workspace basics/i })).toBeInTheDocument();
});

// RequireSetupWorkspace must check must_change_password BEFORE needs_setup —
// an owner who still owes a password change shouldn't be dropped into the
// setup wizard first.
test("a must_change_password owner is redirected to /change-password even for a needs_setup workspace", async () => {
  vi.resetModules();
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            authenticated: true,
            owner: { id: "1", username: "admin", must_change_password: true },
            workspace: {
              id: "ws1", name: "Test WS", about: "", needs_setup: true, created_at: "",
            },
            workspaces: [
              { id: "ws1", name: "Test WS", about: "", needs_setup: true, created_at: "" },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    ),
  );
  window.history.pushState({}, "", "/app/setup");
  const { default: App } = await import("./App");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /change password/i })).toBeInTheDocument();
});
