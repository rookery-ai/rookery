import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import { ThemeProvider } from "@/theme";
import SettingsPage from "./SettingsPage";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "Home Server", about: "Personal assistant", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let settings: Record<string, unknown>;

function baseSettings() {
  return {
    profile: {
      display_name: "Ilija",
      email: "ilija@example.com",
      location: "Skopje",
      timezone: "Europe/Skopje",
      tone: "direct",
      language: "English",
      notes: "",
    },
    workspace: { name: "Home Server", about: "Personal assistant" },
    coder: { kind: "api", bin: "", timeout_s: 120, provider: "openrouter", model: "glm-5.2", base_url: "", api_key_secret: "" },
    detected_coders: [],
    api_providers: [],
    coder_catalog: [],
    secret_names: [],
  };
}

function resetFixtures() {
  settings = baseSettings();
}

type FetchCall = { url: string; method: string; body: unknown };

function mockFetch(overrides: Record<string, (body: unknown) => Response | Promise<Response>> = {}) {
  const calls: FetchCall[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      const key = `${method} ${url}`;
      if (overrides[key]) return Promise.resolve(overrides[key](body));

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));
      if (url === "/api/v1/settings" && method === "GET") return Promise.resolve(jsonResponse(settings));
      if (url === "/api/v1/settings/profile" && method === "PUT") {
        settings = { ...settings, profile: body };
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url === "/api/v1/settings/workspace" && method === "PUT") {
        settings = { ...settings, workspace: body };
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url === "/api/v1/settings/master-password" && method === "PUT") {
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
  return calls;
}

function wrap(initialEntry = "/settings") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/settings" element={<SettingsPage />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
  localStorage.removeItem("sa-theme");
  document.documentElement.classList.remove("dark");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test("Profile round-trip: prefills from GET, edits, PUT body asserted, shows Saved chip", async () => {
  const calls = mockFetch();
  wrap();

  const nameInput = (await screen.findByLabelText("Display name")) as HTMLInputElement;
  await waitFor(() => expect(nameInput.value).toBe("Ilija"));

  const user = userEvent.setup();
  await user.clear(nameInput);
  await user.type(nameInput, "Ilija D.");
  await user.click(screen.getByRole("button", { name: /save profile/i }));

  await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());

  const putCall = calls.find((c) => c.url === "/api/v1/settings/profile" && c.method === "PUT");
  expect(putCall).toBeDefined();
  expect((putCall!.body as { display_name: string }).display_name).toBe("Ilija D.");
});

test("Workspace save invalidates the session query (rail refetches)", async () => {
  mockFetch();
  wrap("/settings?section=workspace");

  const nameInput = (await screen.findByLabelText("Name")) as HTMLInputElement;
  await waitFor(() => expect(nameInput.value).toBe("Home Server"));

  const sessionCallsBefore = vi.mocked(fetch).mock.calls.filter(
    (c) => String(c[0]) === "/api/v1/auth/session",
  ).length;

  const user = userEvent.setup();
  await user.clear(nameInput);
  await user.type(nameInput, "Renamed Workspace");
  await user.click(screen.getByRole("button", { name: /save workspace/i }));

  await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());
  await waitFor(() => {
    const sessionCallsAfter = vi.mocked(fetch).mock.calls.filter(
      (c) => String(c[0]) === "/api/v1/auth/session",
    ).length;
    expect(sessionCallsAfter).toBeGreaterThan(sessionCallsBefore);
  });
});

test("Workspace empty name shows inline error without posting", async () => {
  const calls = mockFetch();
  wrap("/settings?section=workspace");

  const nameInput = (await screen.findByLabelText("Name")) as HTMLInputElement;
  await waitFor(() => expect(nameInput.value).toBe("Home Server"));

  const user = userEvent.setup();
  await user.clear(nameInput);
  await user.click(screen.getByRole("button", { name: /save workspace/i }));

  expect(await screen.findByText(/workspace name is required/i)).toBeInTheDocument();
  expect(calls.find((c) => c.url === "/api/v1/settings/workspace" && c.method === "PUT")).toBeUndefined();
});

test("Master password: mismatch shows inline error without posting", async () => {
  const calls = mockFetch();
  wrap("/settings?section=master-password");

  await screen.findByLabelText(/current master password/i);
  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/current master password/i), "old-pw");
  await user.type(screen.getByLabelText(/^new master password$/i), "new-pw-123");
  await user.type(screen.getByLabelText(/confirm new master password/i), "different-pw");
  await user.click(screen.getByRole("button", { name: /change master password/i }));

  expect(await screen.findByText(/do not match/i)).toBeInTheDocument();
  expect(calls.find((c) => c.url === "/api/v1/settings/master-password")).toBeUndefined();
});

test("Master password: wrong current password shows the 401 envelope message inline", async () => {
  mockFetch({
    "PUT /api/v1/settings/master-password": () =>
      jsonResponse({ error: { code: "wrong_master_password", message: "Old master password is incorrect" } }, 401),
  });
  wrap("/settings?section=master-password");

  await screen.findByLabelText(/current master password/i);
  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/current master password/i), "wrong-pw");
  await user.type(screen.getByLabelText(/^new master password$/i), "new-pw-123");
  await user.type(screen.getByLabelText(/confirm new master password/i), "new-pw-123");
  await user.click(screen.getByRole("button", { name: /change master password/i }));

  expect(await screen.findByText(/old master password is incorrect/i)).toBeInTheDocument();
});

test("Master password: success clears fields and shows Changed chip", async () => {
  mockFetch();
  wrap("/settings?section=master-password");

  const current = (await screen.findByLabelText(/current master password/i)) as HTMLInputElement;
  const next = screen.getByLabelText(/^new master password$/i) as HTMLInputElement;
  const confirm = screen.getByLabelText(/confirm new master password/i) as HTMLInputElement;

  const user = userEvent.setup();
  await user.type(current, "old-pw");
  await user.type(next, "new-pw-123");
  await user.type(confirm, "new-pw-123");
  await user.click(screen.getByRole("button", { name: /change master password/i }));

  await waitFor(() => expect(screen.getByText("Changed")).toBeInTheDocument());
  expect(current.value).toBe("");
  expect(next.value).toBe("");
  expect(confirm.value).toBe("");
});

test("Appearance: selecting Dark toggles the documentElement dark class instantly", async () => {
  mockFetch();
  wrap("/settings?section=appearance");

  await screen.findByRole("radiogroup", { name: /appearance/i });
  expect(document.documentElement.classList.contains("dark")).toBe(false);

  const user = userEvent.setup();
  await user.click(screen.getByRole("radio", { name: /dark/i }));

  await waitFor(() => expect(document.documentElement.classList.contains("dark")).toBe(true));
});

test("Section nav switches the visible section via ?section= param", async () => {
  mockFetch();
  wrap();

  await screen.findByLabelText("Display name");
  expect(screen.getByRole("heading", { name: "Profile" })).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /ai providers/i }));

  expect(await screen.findByRole("heading", { name: "AI Providers" })).toBeInTheDocument();
  expect(screen.getByText(/no providers available/i)).toBeInTheDocument();
});

test("Settings load error shows an inline banner", async () => {
  mockFetch({
    "GET /api/v1/settings": () =>
      jsonResponse({ error: { code: "internal", message: "database unreachable" } }, 500),
  });
  wrap();

  expect(await screen.findByText(/database unreachable/i)).toBeInTheDocument();
});
