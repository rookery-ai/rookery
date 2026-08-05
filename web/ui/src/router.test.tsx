import { render, screen } from "@testing-library/react";

// Every assertion in this file waits on a React.lazy chunk to finish importing,
// and those chunks are the app's heaviest: SetupWizard and KBPage pull in
// ProviderLogo, whose logos.ts eagerly inlines all ~124 vendored SVGs (~320 KB
// of raw strings — eager by design, since a currentColor mark cannot be themed
// across an <img> boundary). Transforming that under a fully parallel suite on
// a small CPU regularly overruns testing-library's 1000 ms default, and the
// failure surfaces as a confusing "unable to find heading" rather than a
// timeout. These tests assert WHICH route renders, never how fast, so the wait
// is widened rather than the assertion weakened — a route that never renders
// still fails, just later.
const LAZY = { timeout: 15000 };

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
  window.history.pushState({}, "", "/");
  const { default: App } = await import("./App");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: /workspace basics/i }, LAZY),
  ).toBeInTheDocument();
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
  window.history.pushState({}, "", "/setup");
  const { default: App } = await import("./App");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: /change password/i }, LAZY),
  ).toBeInTheDocument();
});

// The workspace picker is where Sign out lives, so an unauthenticated session
// sitting on it must land on the login screen. It used to be a bare route with
// no guard at all: signing out flipped the session to unauthenticated and
// nothing navigated, leaving the owner on an empty picker. This also covers a
// logged-out visitor typing /workspaces directly.
test("an unauthenticated session on /workspaces is sent to the login screen", async () => {
  vi.resetModules();
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ authenticated: false }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
  window.history.pushState({}, "", "/workspaces");
  const { default: App } = await import("./App");
  render(<App />);

  expect(await screen.findByLabelText(/password/i, undefined, LAZY)).toBeInTheDocument();
  expect(screen.queryByText(/pick a workspace/i)).not.toBeInTheDocument();
});

// The guard must not over-reach: an authenticated owner with no active
// workspace is exactly who the picker is FOR. RequireAuth cannot be reused here
// because it redirects that same session to /workspaces — an infinite loop.
test("an authenticated owner with no active workspace still sees the picker", async () => {
  vi.resetModules();
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            authenticated: true,
            owner: { id: "1", username: "admin", must_change_password: false },
            workspace: null,
            workspaces: [
              { id: "ws1", name: "Test WS", icon: "", about: "", needs_setup: false, created_at: "" },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    ),
  );
  window.history.pushState({}, "", "/workspaces");
  const { default: App } = await import("./App");
  render(<App />);

  expect(await screen.findByText("Test WS", undefined, LAZY)).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: /sign out/i })).toBeInTheDocument();
});

// /kb is route-split (React.lazy + Suspense in router.tsx) because it pulls
// in the whole TipTap editor. Proves the split works end to end through the
// real app router + auth guard: the Suspense fallback shows first, then the
// real KB page content resolves once the chunk's dynamic import settles.
test("navigating to /kb shows the Suspense fallback, then resolves to the KB page", async () => {
  vi.resetModules();
  const SESSION_FIXTURE = {
    authenticated: true,
    owner: { id: "1", username: "admin", must_change_password: false },
    workspace: { id: "ws1", name: "Test WS", about: "", needs_setup: false, created_at: "" },
    workspaces: [{ id: "ws1", name: "Test WS", about: "", needs_setup: false, created_at: "" }],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/tree")) {
        return Promise.resolve(
          new Response(JSON.stringify({ path: "", nodes: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      if (url === "/api/v1/inbox/poll") {
        return Promise.resolve(
          new Response(JSON.stringify({ unread: 0, recent: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify(SESSION_FIXTURE), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }),
  );

  window.history.pushState({}, "", "/kb");
  const { default: App } = await import("./App");
  render(<App />);

  // The fallback appears while the session resolves and/or the lazy KBPage
  // chunk is still importing — same loading affordance the auth guards use.
  expect(await screen.findByText("Loading…", undefined, LAZY)).toBeInTheDocument();

  // Once the dynamic import settles, the real page replaces the fallback.
  expect(await screen.findByText("Knowledge Base", undefined, LAZY)).toBeInTheDocument();
  expect(screen.getByText("Select a note or create one.")).toBeInTheDocument();
});
