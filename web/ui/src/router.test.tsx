import { render, screen } from "@testing-library/react";

// RequireAuth must route an authenticated owner whose active workspace still
// needs onboarding to the workspaces picker (with ?setup=<id>) instead of
// rendering the shell — mirrors the no-workspace / must-change-password guards.
test("redirects an authenticated needs_setup workspace to the workspaces picker", async () => {
  vi.resetModules();
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          authenticated: true,
          owner: { id: "1", username: "admin", must_change_password: false },
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
  );
  window.history.pushState({}, "", "/app/");
  const { default: App } = await import("./App");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /workspaces/i })).toBeInTheDocument();
  expect(await screen.findByText(/needs onboarding/i)).toBeInTheDocument();
});
