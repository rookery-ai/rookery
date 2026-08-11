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

test("creating a workspace asks for a name only, and posts no about", async () => {
  // "What is this workspace about?" belongs to the setup wizard the new
  // workspace lands in. It used to be asked here too, so a user answered the
  // same question twice in a row.
  const calls: Array<{ url: string; body: unknown }> = [];
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url);
    if (u.endsWith("/auth/session")) {
      return Promise.resolve(
        new Response(JSON.stringify(session), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }
    calls.push({ url: u, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return Promise.resolve(
      new Response(JSON.stringify({ id: "w2", name: "Work", about: "", needs_setup: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  });
  vi.stubGlobal("fetch", fetchMock);

  wrap();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /create workspace/i }));

  const nameInput = await screen.findByLabelText(/^name$/i);
  expect(screen.queryByLabelText(/about/i)).toBeNull();

  await user.type(nameInput, "Work");
  await user.click(screen.getByRole("button", { name: /^create$/i }));

  await waitFor(() => {
    expect(calls.find((c) => c.url.endsWith("/api/v1/workspaces"))).toBeDefined();
  });
  const post = calls.find((c) => c.url.endsWith("/api/v1/workspaces"))!;
  expect(post.body).toEqual({ name: "Work" });
});

// A workspace is a tenant, so creating one sits behind requireOwnerVerified.
// The dialog does not predict whether the confirmation is still fresh — it
// submits, and only a server refusal turns it into a password step. The typed
// name must survive that swap, and the create must be retried automatically.
test("create asks for the owner password when the server demands it, then retries", async () => {
  const calls: Array<{ url: string; body: unknown }> = [];
  let createAttempts = 0;
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url);
    if (u.endsWith("/auth/session")) {
      return Promise.resolve(
        new Response(JSON.stringify(session), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }
    calls.push({ url: u, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (u.endsWith("/api/v1/workspaces")) {
      createAttempts++;
      // Refused until confirmed, exactly as requireOwnerVerified answers.
      if (createAttempts === 1) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              error: {
                code: "owner_verification_required",
                message: "confirm your owner password to continue",
              },
            }),
            { status: 403, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify({ id: "w2", name: "Work", about: "", needs_setup: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }
    return Promise.resolve(
      new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }),
    );
  });
  vi.stubGlobal("fetch", fetchMock);

  wrap();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /create workspace/i }));
  await user.type(await screen.findByLabelText(/^name$/i), "Work");
  await user.click(screen.getByRole("button", { name: /^create$/i }));

  // Refused → the dialog becomes an owner-password step.
  const pw = await screen.findByLabelText(/owner password/i);
  await user.type(pw, "owner-pw");
  await user.click(screen.getByRole("button", { name: /confirm and create/i }));

  await waitFor(() => expect(createAttempts).toBe(2));
  expect(calls.some((c) => c.url.endsWith("/api/v1/auth/owner-verify"))).toBe(true);
  // The name typed before the gate appeared is carried through, not re-asked.
  const retry = calls.filter((c) => c.url.endsWith("/api/v1/workspaces")).at(-1)!;
  expect(retry.body).toEqual({ name: "Work" });
});

// Sign out is reachable from the workspace picker — the screen an owner lands
// on after leaving a workspace, and the app's only sign-out affordance.
test("the workspace picker offers sign out", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: RequestInfo | URL) =>
      Promise.resolve(
        new Response(String(url).endsWith("/auth/session") ? JSON.stringify(session) : "{}", {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
  wrap();
  expect(await screen.findByRole("button", { name: /sign out/i })).toBeInTheDocument();
});

// Each row carries its own image, matching the rail's workspace switcher — so a
// workspace is recognised by the same picture wherever it appears, rather than
// by reading names in one place and scanning pictures in the other.
test("each workspace row shows its icon", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          ...session,
          workspaces: [
            { id: "w1", name: "personal", icon: "aurora", about: "", needs_setup: false, created_at: "" },
            { id: "w2", name: "work", icon: "ember", about: "", needs_setup: false, created_at: "" },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  const { container } = wrap();

  expect(await screen.findByText("personal")).toBeInTheDocument();
  // The avatar is a decorative <svg>, so it is queried structurally: it carries
  // aria-hidden by design and has no accessible name to select on.
  const rows = container.querySelectorAll("ul > li");
  expect(rows).toHaveLength(2);
  for (const row of rows) {
    expect(row.querySelector("svg")).not.toBeNull();
  }
});

// A workspace with no icon set still needs something on the left, or rows with
// and without one would not line up. That used to be a monogram; it is now the
// Rookery mark, so an install looks like the product before anyone picks a tile.
test("a workspace with no icon falls back to the Rookery mark", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          ...session,
          workspaces: [
            { id: "w1", name: "personal", about: "", needs_setup: false, created_at: "" },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  wrap();

  expect(await screen.findByText("personal")).toBeInTheDocument();
  // WorkspaceAvatar's unset branch now renders the default preset. The row's
  // own <li> is checked rather than the document, because this screen carries
  // the brand mark in its header too.
  const row = screen.getByText("personal").closest("li")!;
  expect(row.querySelector("svg")).not.toBeNull();
  expect(row.textContent).not.toContain("P ");
});
