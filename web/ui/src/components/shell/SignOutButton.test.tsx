import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SignOutButton } from "./SignOutButton";

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <SignOutButton />
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
