import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { SignOutButton } from "./SignOutButton";

// Rendered inside a router carrying the routes that matter to signing out, so
// the assertion can be "which screen am I on" rather than "was a spy called".
// `initialEntries` defaults to the workspace picker because that is where the
// reported bug happened.
function wrap(initialPath = "/workspaces") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route
            path="/workspaces"
            element={
              <>
                <div>workspace picker</div>
                <SignOutButton />
              </>
            }
          />
          <Route
            path="/login"
            element={
              <>
                <div>login screen</div>
                <SignOutButton />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function mockFetch() {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: RequestInfo | URL) => {
      calls.push(String(url));
      return Promise.resolve(
        new Response("{}", {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }),
  );
  return calls;
}

// The endpoint has existed since the JSON API was written and the SPA called it
// from nowhere — which is the bug. Pin that it is actually reached.
test("signing out posts to the logout endpoint", async () => {
  const calls = mockFetch();
  wrap();
  const user = userEvent.setup();

  await user.click(screen.getByRole("button", { name: /sign out/i }));
  // The confirm dialog's button, not the corner one.
  const confirm = await screen.findByRole("button", { name: /^sign out$/i });
  await user.click(confirm);

  await waitFor(() =>
    expect(calls.some((u) => u.endsWith("/api/v1/auth/logout"))).toBe(true),
  );
});

// A mis-click in a screen corner costs a re-login AND a master-password
// re-entry, so the first click must never end the session on its own.
test("the corner button confirms before ending the session", async () => {
  const calls = mockFetch();
  wrap();
  const user = userEvent.setup();

  await user.click(screen.getByRole("button", { name: /sign out/i }));
  expect(calls.some((u) => u.endsWith("/api/v1/auth/logout"))).toBe(false);

  await user.click(screen.getByRole("button", { name: /cancel/i }));
  expect(calls.some((u) => u.endsWith("/api/v1/auth/logout"))).toBe(false);
});

// The reported bug. Signing out left the user on the workspace picker.
//
// The component relied on the ["session"] refetch pushing RequireAuth to
// /login, but that only works from a screen UNDER RequireAuth — and neither
// screen mounting this button is. The lock screen is rendered in place BY
// RequireAuth so it happened to work; /workspaces is a top-level route with no
// guard, so nothing re-evaluated and the picker simply re-rendered against a
// now-unauthenticated session.
test("signing out from the workspace picker lands on the login screen", async () => {
  mockFetch();
  wrap("/workspaces");
  const user = userEvent.setup();

  expect(screen.getByText("workspace picker")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /sign out/i }));
  await user.click(await screen.findByRole("button", { name: /^sign out$/i }));

  expect(await screen.findByText("login screen")).toBeInTheDocument();
  expect(screen.queryByText("workspace picker")).not.toBeInTheDocument();
});

// `replace: true` matters: without it the picker stays on the history stack and
// Back returns a signed-out user to a screen that reads as signed in.
test("signing out replaces history rather than pushing onto it", async () => {
  mockFetch();
  wrap("/workspaces");
  const user = userEvent.setup();

  await user.click(screen.getByRole("button", { name: /sign out/i }));
  await user.click(await screen.findByRole("button", { name: /^sign out$/i }));
  await screen.findByText("login screen");

  history.back();

  // The picker must not come back. Give the navigation a chance to settle
  // before asserting, so this fails loudly rather than racing to a pass.
  await waitFor(() =>
    expect(screen.queryByText("workspace picker")).not.toBeInTheDocument(),
  );
});
