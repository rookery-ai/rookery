import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import SecretsPage from "./SecretsPage";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let secrets: { name: string }[];

function resetFixtures() {
  secrets = [{ name: "OPENAI_API_KEY" }, { name: "GITHUB_TOKEN" }];
}

function mockFetch(opts: { deleteStatus?: number; deleteError?: { code: string; message: string } } = {}) {
  const calls: Array<{ url: string; method: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/secrets" && method === "GET") {
        return Promise.resolve(jsonResponse({ secrets }));
      }
      if (url === "/api/v1/secrets" && method === "POST") {
        const b = body as { name: string; value: string };
        secrets = [...secrets, { name: b.name }];
        return Promise.resolve(jsonResponse({ ok: true }, 201));
      }
      if (url.startsWith("/api/v1/secrets/") && method === "DELETE") {
        const name = decodeURIComponent(url.split("/").pop()!);
        if (opts.deleteStatus && opts.deleteStatus !== 200) {
          return Promise.resolve(
            jsonResponse({ error: opts.deleteError ?? { code: "internal", message: "failed" } }, opts.deleteStatus),
          );
        }
        secrets = secrets.filter((s) => s.name !== name);
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<SecretsPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
});

test("renders existing secrets by name (no values anywhere)", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("OPENAI_API_KEY")).toBeInTheDocument();
  expect(screen.getByText("GITHUB_TOKEN")).toBeInTheDocument();
});

test("empty state shown when there are no secrets", async () => {
  secrets = [];
  mockFetch();
  wrap();

  expect(await screen.findByText(/No secrets yet/)).toBeInTheDocument();
  expect(screen.getByText(/agents use these for API keys and tokens/)).toBeInTheDocument();
});

test("search filters the list by name, client-side", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  expect(await screen.findByText("OPENAI_API_KEY")).toBeInTheDocument();
  expect(screen.getByText("GITHUB_TOKEN")).toBeInTheDocument();

  await user.type(screen.getByPlaceholderText("Search secrets…"), "openai");

  expect(screen.getByText("OPENAI_API_KEY")).toBeInTheDocument();
  expect(screen.queryByText("GITHUB_TOKEN")).not.toBeInTheDocument();
});

test("add secret: submits name+value, clears the form, shows a transient Saved chip, and never renders the value", async () => {
  const calls = mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("OPENAI_API_KEY");

  const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
  const valueInput = screen.getByLabelText("Value") as HTMLInputElement;
  expect(valueInput).toHaveAttribute("type", "password");

  await user.type(nameInput, "STRIPE_SECRET_KEY");
  await user.type(valueInput, "sk_live_supersecret");
  await user.click(screen.getByRole("button", { name: /^Add$/ }));

  await waitFor(() =>
    expect(calls.some((c) => c.url === "/api/v1/secrets" && c.method === "POST")).toBe(true),
  );
  const postCall = calls.find((c) => c.url === "/api/v1/secrets" && c.method === "POST")!;
  expect(postCall.body).toEqual({ name: "STRIPE_SECRET_KEY", value: "sk_live_supersecret" });

  await waitFor(() => expect(nameInput.value).toBe(""));
  expect(valueInput.value).toBe("");
  expect(await screen.findByText("Saved ✓")).toBeInTheDocument();

  // The just-added secret's value must never appear anywhere in the DOM.
  expect(screen.queryByText("sk_live_supersecret")).not.toBeInTheDocument();
  expect(document.body.textContent).not.toContain("sk_live_supersecret");

  // The new secret shows up in the list (name only).
  expect(await screen.findByText("STRIPE_SECRET_KEY")).toBeInTheDocument();
});

test("add button is disabled until both name and value are filled", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("OPENAI_API_KEY");
  const addButton = screen.getByRole("button", { name: /^Add$/ });
  expect(addButton).toBeDisabled();

  await user.type(screen.getByLabelText("Name"), "FOO");
  expect(addButton).toBeDisabled();

  await user.type(screen.getByLabelText("Value"), "bar");
  expect(addButton).not.toBeDisabled();
});

test("delete: dialog requires a master password (Delete disabled until filled)", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  const row = (await screen.findByText("OPENAI_API_KEY")).closest("li")!;
  await user.click(within(row).getByRole("button", { name: /delete/i }));

  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText(/Deleting.*OPENAI_API_KEY.*master password/)).toBeInTheDocument();
  const confirmDelete = within(dialog).getByRole("button", { name: /^Delete$/ });
  expect(confirmDelete).toBeDisabled();

  await user.type(within(dialog).getByLabelText("Master password"), "hunter2");
  expect(confirmDelete).not.toBeDisabled();
});

test("delete: wrong master password shows an inline 401 error and keeps the dialog open", async () => {
  mockFetch({ deleteStatus: 401, deleteError: { code: "wrong_master_password", message: "wrong master password" } });
  const user = userEvent.setup();
  wrap();

  const row = (await screen.findByText("OPENAI_API_KEY")).closest("li")!;
  await user.click(within(row).getByRole("button", { name: /delete/i }));

  const dialog = screen.getByRole("dialog");
  await user.type(within(dialog).getByLabelText("Master password"), "wrongpw");
  await user.click(within(dialog).getByRole("button", { name: /^Delete$/ }));

  expect(await within(dialog).findByText("wrong master password")).toBeInTheDocument();
  // Dialog stays open, secret is still in the list.
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  expect(screen.getByText("OPENAI_API_KEY")).toBeInTheDocument();
});

test("delete: success removes the row and closes the dialog", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  const row = (await screen.findByText("OPENAI_API_KEY")).closest("li")!;
  await user.click(within(row).getByRole("button", { name: /delete/i }));

  const dialog = screen.getByRole("dialog");
  await user.type(within(dialog).getByLabelText("Master password"), "correcthorse");
  await user.click(within(dialog).getByRole("button", { name: /^Delete$/ }));

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  await waitFor(() => expect(screen.queryByText("OPENAI_API_KEY")).not.toBeInTheDocument());
  expect(screen.getByText("GITHUB_TOKEN")).toBeInTheDocument();
});

// GitHub-Actions-style rotation: a secret's value can be REPLACED without ever
// being shown. The endpoint is the same POST as create (the DB write is an
// upsert), and no master password is asked for — matching create, and matching
// how every other secret store handles a rotation.
test("update: replaces a secret's value without ever revealing it", async () => {
  const calls = mockFetch();
  wrap();
  await screen.findByText("OPENAI_API_KEY");

  const row = screen.getByText("OPENAI_API_KEY").closest("li")!;
  await userEvent.click(within(row).getByRole("button", { name: /update/i }));

  const dialog = await screen.findByRole("dialog");
  // The name is shown but not editable, and no existing value is fetched or
  // pre-filled anywhere.
  expect(within(dialog).getByLabelText("Name")).toHaveValue("OPENAI_API_KEY");
  expect(within(dialog).getByLabelText("Name")).toBeDisabled();
  const valueField = within(dialog).getByLabelText(/new value/i);
  expect(valueField).toHaveValue("");
  expect(within(dialog).queryByLabelText(/master password/i)).not.toBeInTheDocument();

  await userEvent.type(valueField, "sk-rotated");
  await userEvent.click(within(dialog).getByRole("button", { name: "Update" }));

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  const post = calls.filter((c) => c.url === "/api/v1/secrets" && c.method === "POST");
  expect(post).toHaveLength(1);
  expect(post[0]!.body).toEqual({ name: "OPENAI_API_KEY", value: "sk-rotated" });
  // The new value must never appear on the page.
  expect(screen.queryByText("sk-rotated")).not.toBeInTheDocument();
});

// The create endpoint upserts, so an existing name typed into the Add form
// would silently replace a secret the user can't see. Rotating is a deliberate
// act with its own dialog — send them there instead of overwriting by accident.
test("add: refuses a name that already exists and points at Update", async () => {
  const calls = mockFetch();
  wrap();
  await screen.findByText("OPENAI_API_KEY");

  await userEvent.type(screen.getByLabelText("Name"), "OPENAI_API_KEY");
  await userEvent.type(screen.getByLabelText("Value"), "clobber");

  expect(screen.getByText(/already exists/i)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
});
