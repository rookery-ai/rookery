import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import LockScreen from "./LockScreen";

const SESSION = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "Home Server", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
  locked: true,
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <LockScreen />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

test("names the still-entered workspace, since locking does not leave it", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(SESSION)));
  wrap();
  expect(await screen.findByText("Home Server")).toBeInTheDocument();
  expect(screen.getByLabelText(/master password/i)).toBeInTheDocument();
});

test("submitting posts the master password to the unlock endpoint", async () => {
  const calls: Array<{ url: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const u = String(url);
      if (u.endsWith("/auth/session")) return Promise.resolve(jsonResponse(SESSION));
      calls.push({ url: u, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      return Promise.resolve(jsonResponse({ ok: true }));
    }),
  );
  wrap();

  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(/master password/i), "master-pw-1");
  await user.click(screen.getByRole("button", { name: /unlock/i }));

  await waitFor(() => {
    expect(calls.find((c) => c.url.endsWith("/api/v1/auth/unlock"))).toBeDefined();
  });
  const post = calls.find((c) => c.url.endsWith("/api/v1/auth/unlock"))!;
  expect(post.body).toEqual({ master_password: "master-pw-1" });
});

test("a rejected password shows a specific message and keeps the form up", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: RequestInfo | URL) => {
      const u = String(url);
      if (u.endsWith("/auth/session")) return Promise.resolve(jsonResponse(SESSION));
      return Promise.resolve(
        jsonResponse({ code: "invalid_master_password", message: "wrong master password" }, 401),
      );
    }),
  );
  wrap();

  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(/master password/i), "nope");
  await user.click(screen.getByRole("button", { name: /unlock/i }));

  expect(await screen.findByText(/wrong master password/i)).toBeInTheDocument();
  // Still locked, still asking — a failed unlock must not fall through.
  expect(screen.getByLabelText(/master password/i)).toBeInTheDocument();
});
