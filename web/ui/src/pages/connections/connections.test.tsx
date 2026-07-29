import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route, useSearchParams } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import ConnectionsPage from "./ConnectionsPage";
import type { ConnectorPlatform, ServiceProvider } from "@/lib/connections";

// MemoryRouter keeps its own in-memory history, decoupled from
// window.location — so "the params got cleared" has to be observed through
// react-router's own search-params state, not window.location.search.
function SearchParamsDebug() {
  const [params] = useSearchParams();
  return <div data-testid="search-params-debug">{params.toString()}</div>;
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

let platforms: ConnectorPlatform[];
let providers: ServiceProvider[];
let connectorsStatus = 200;
let servicesStatus = 200;
let searchKeysState: { brave: boolean; tavily: boolean };
let searchKeysStatus = 200;

function resetFixtures() {
  platforms = [
    {
      platform: "telegram",
      label: "Telegram",
      blurb: "",
      setup_steps: [],
      fields: [{ name: "bot_token", label: "Bot token", secret: true }],
      connected: true,
      identity: "@rookie_assistant_bot",
    },
    {
      platform: "slack",
      label: "Slack",
      blurb: "",
      setup_steps: [],
      fields: [],
      connected: false,
      identity: "",
    },
  ];
  providers = [
    {
      name: "gmail",
      label: "Gmail",
      category: "Google",
      kind: "oauth",
      setup_url: "",
      setup_steps: [],
      has_creds: true,
      action_count: 0, redirect_uri: "", preflight: [],
      connect_inputs: [],
      connections: [{ id: "c1", label: "work", identity: "me@gmail.com", status: "ACTIVE" }],
    },
    {
      name: "notion",
      label: "Notion",
      category: "Productivity",
      kind: "oauth",
      setup_url: "",
      setup_steps: [],
      has_creds: false,
      action_count: 0, redirect_uri: "", preflight: [],
      connect_inputs: [],
      connections: [],
    },
    {
      name: "jira",
      label: "Jira",
      category: "Productivity",
      kind: "oauth",
      setup_url: "",
      setup_steps: [],
      has_creds: true,
      action_count: 0, redirect_uri: "", preflight: [],
      connect_inputs: [],
      connections: [{ id: "c2", label: "team", identity: "me@co.com", status: "NEEDS_REAUTH" }],
    },
  ];
  connectorsStatus = 200;
  servicesStatus = 200;
  searchKeysState = { brave: false, tavily: false };
  searchKeysStatus = 200;
}

function mockFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url === "/api/v1/auth/session") return Promise.resolve(jsonResponse(SESSION_FIXTURE));

      if (url === "/api/v1/connectors" && method === "GET") {
        if (connectorsStatus !== 200)
          return Promise.resolve(
            jsonResponse({ error: { code: "internal", message: "could not load chat apps" } }, connectorsStatus),
          );
        return Promise.resolve(jsonResponse({ platforms }));
      }

      if (url === "/api/v1/services" && method === "GET") {
        if (servicesStatus !== 200)
          return Promise.resolve(
            jsonResponse({ error: { code: "internal", message: "could not load services" } }, servicesStatus),
          );
        return Promise.resolve(jsonResponse({ providers }));
      }

      if (url === "/api/v1/search-keys" && method === "GET") {
        if (searchKeysStatus !== 200)
          return Promise.resolve(
            jsonResponse(
              { error: { code: "internal", message: "could not load web search settings" } },
              searchKeysStatus,
            ),
          );
        return Promise.resolve(jsonResponse(searchKeysState));
      }

      if (url === "/api/v1/search-keys" && method === "PUT") {
        const body = JSON.parse(String(init?.body ?? "{}")) as { provider: string; key: string };
        if (body.provider === "brave") searchKeysState.brave = true;
        if (body.provider === "tavily") searchKeysState.tavily = true;
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      if (url.startsWith("/api/v1/search-keys/") && method === "DELETE") {
        const provider = url.split("/").pop();
        if (provider === "brave") searchKeysState.brave = false;
        if (provider === "tavily") searchKeysState.tavily = false;
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap(initialPath = "/") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route
              path="/"
              element={
                <>
                  <ConnectionsPage />
                  <SearchParamsDebug />
                </>
              }
            />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetFixtures();
});

test("renders chat-app cards: connected shows identity + Manage, not-connected shows Connect", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Telegram")).toBeInTheDocument();
  expect(screen.getByText("@rookie_assistant_bot")).toBeInTheDocument();
  expect(screen.getByText("Connected")).toBeInTheDocument();
  const telegramCard = screen.getByText("Telegram").closest("div.rounded-lg")!;
  expect(telegramCard.textContent).toContain("Manage");

  expect(screen.getByText("Slack")).toBeInTheDocument();
  expect(screen.getByText("Not connected")).toBeInTheDocument();
  const slackCard = screen.getByText("Slack").closest("div.rounded-lg")!;
  expect(slackCard.textContent).toContain("Connect");
});

test("renders service tiles: account count, empty Connect state, and amber reconnect hint", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Gmail")).toBeInTheDocument();
  expect(screen.getByText("● 1 account")).toBeInTheDocument();

  expect(screen.getByText("Notion")).toBeInTheDocument();
  const notionTile = screen.getByText("Notion").closest("button")!;
  expect(notionTile.textContent).toContain("Connect");

  expect(screen.getByText("Jira")).toBeInTheDocument();
  expect(screen.getByText("reconnect needed")).toBeInTheDocument();
});

test("category rows show correct counts: chat apps total, services connected-of-total", async () => {
  mockFetch();
  wrap();

  await screen.findByText("Telegram");
  const chatAppsRow = screen.getByRole("button", { name: /chat apps/i });
  expect(chatAppsRow.textContent).toContain("2");

  const servicesRow = screen.getByRole("button", { name: /^services/i });
  // gmail + jira both have >=1 connection → 2 of 3
  expect(servicesRow.textContent).toContain("2 of 3");
});

test("clicking a category row scrolls to its section", async () => {
  const scrollSpy = vi.fn();
  window.HTMLElement.prototype.scrollIntoView = scrollSpy;
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Telegram");
  await user.click(screen.getByRole("button", { name: /chat apps/i }));
  expect(scrollSpy).toHaveBeenCalledTimes(1);

  await user.click(screen.getByRole("button", { name: /^services/i }));
  expect(scrollSpy).toHaveBeenCalledTimes(2);
});

test("explainer card shows the mockup copy", async () => {
  mockFetch();
  wrap();
  await screen.findByText("Telegram");
  expect(screen.getByText(/are where you talk to your assistant/)).toBeInTheDocument();
  expect(screen.getByText(/are the accounts your agents can act on/)).toBeInTheDocument();
});

test("search debounces before filtering, then filters both galleries by name/label", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  expect(await screen.findByText("Slack")).toBeInTheDocument();
  expect(screen.getByText("Notion")).toBeInTheDocument();

  const input = screen.getByPlaceholderText("Search providers…");
  await user.type(input, "gmail");

  // Immediately after typing, the debounce window (150ms) hasn't elapsed —
  // stale results are still showing.
  expect(screen.getByText("Slack")).toBeInTheDocument();

  // After the debounce settles, both galleries are filtered: only items
  // matching "gmail" (by platform/provider name or label) remain.
  await waitFor(() => expect(screen.queryByText("Slack")).not.toBeInTheDocument());
  expect(screen.queryByText("Notion")).not.toBeInTheDocument();
  expect(screen.getByText("Gmail")).toBeInTheDocument();
});

test("clicking Connect on a chat-app card opens the connect wizard for the platform", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Slack");
  const slackCard = screen.getByText("Slack").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(slackCard).getByRole("button", { name: "Connect" }));

  expect(await screen.findByText("Connect Slack")).toBeInTheDocument();
  // ChatAppWizard's not-connected flow opens on the "Setup" step.
  expect(screen.getByText("Setup")).toBeInTheDocument();
  expect(screen.getByText("Credentials")).toBeInTheDocument();
});

test("clicking Manage on a connected chat-app card opens the manage wizard", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Telegram");
  const telegramCard = screen.getByText("Telegram").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(telegramCard).getByRole("button", { name: "Manage" }));

  expect(await screen.findByText("Manage Telegram")).toBeInTheDocument();
  // ChatAppWizard's connected (Manage) flow shows the saved identity inside
  // the slide-over panel (the source card behind it shows the same text).
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("@rookie_assistant_bot")).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: /test connection/i })).toBeInTheDocument();
});

test("clicking a connected service tile opens the Manage-titled wizard for that provider", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  const gmailTile = await screen.findByText("Gmail");
  await user.click(gmailTile.closest("button")!);

  // Gmail already has a connection in the fixture, so the wizard opens in
  // "Manage" mode (mirrors the chat-app wizard's connected/not-connected
  // title split) and shows the connected account.
  expect(await screen.findByText("Manage Gmail")).toBeInTheDocument();
  expect(screen.getByText("me@gmail.com")).toBeInTheDocument();
});

test("clicking a not-yet-connected service tile opens the Connect-titled wizard", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  const notionTile = await screen.findByText("Notion");
  await user.click(notionTile.closest("button")!);

  expect(await screen.findByText("Connect Notion")).toBeInTheDocument();
});

test("shows an error banner when the connectors list fails to load", async () => {
  connectorsStatus = 500;
  mockFetch();
  wrap();

  expect(await screen.findByText("could not load chat apps")).toBeInTheDocument();
});

test("shows an error banner when the services list fails to load", async () => {
  servicesStatus = 500;
  mockFetch();
  wrap();

  expect(await screen.findByText("could not load services")).toBeInTheDocument();
});

// ── Landing banner (after the OAuth callback's full-page redirect) ────────

test("?connected=<provider> shows a success banner resolving the label, clears the param, and can be dismissed", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap("/?connected=gmail");

  expect(await screen.findByText("Gmail connected ✓")).toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByTestId("search-params-debug").textContent).not.toContain("connected"),
  );

  await user.click(screen.getByRole("button", { name: "Dismiss" }));
  expect(screen.queryByText("Gmail connected ✓")).not.toBeInTheDocument();
});

test("?connected=<unknown-slug> falls back to the raw slug when no matching provider label is found", async () => {
  mockFetch();
  wrap("/?connected=some-unlisted-provider");

  expect(await screen.findByText("some-unlisted-provider connected ✓")).toBeInTheDocument();
});

test("?error=<msg> shows an error banner prefixed with 'Connection failed: ' and clears the param", async () => {
  mockFetch();
  wrap("/?error=" + encodeURIComponent("Authorization was denied: access_denied"));

  expect(
    await screen.findByText("Connection failed: Authorization was denied: access_denied"),
  ).toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByTestId("search-params-debug").textContent).not.toContain("error"),
  );
});

test("?error=<huge msg> caps the displayed length instead of blowing out the banner", async () => {
  mockFetch();
  const huge = "x".repeat(500);
  wrap("/?error=" + encodeURIComponent(huge));

  const banner = await screen.findByText(/^Connection failed: x+…$/);
  expect(banner).toBeInTheDocument();
  // "Connection failed: " (20 chars) + 200 capped chars + the ellipsis.
  expect(banner.textContent!.length).toBeLessThanOrEqual(20 + 200 + 1);
});

// ── Web search ─────────────────────────────────────────────────────────────

test("renders both search key providers with Not configured state from the API", async () => {
  mockFetch();
  wrap();

  expect(await screen.findByText("Brave Search")).toBeInTheDocument();
  expect(screen.getByText("Tavily")).toBeInTheDocument();
  expect(screen.getAllByText("Not configured")).toHaveLength(2);
  expect(screen.getAllByRole("button", { name: "Add key" })).toHaveLength(2);

  const searchRow = screen.getByRole("button", { name: /web search/i });
  expect(searchRow.textContent).toContain("0 of 2");
});

test("shows Configured state and a Clear action when a key is already set", async () => {
  searchKeysState = { brave: true, tavily: false };
  mockFetch();
  wrap();

  await screen.findByText("Brave Search");
  const braveRow = screen.getByText("Brave Search").closest("div.rounded-lg") as HTMLElement;
  expect(within(braveRow).getByText("Configured")).toBeInTheDocument();
  expect(within(braveRow).getByRole("button", { name: "Replace" })).toBeInTheDocument();
  expect(within(braveRow).getByRole("button", { name: "Clear" })).toBeInTheDocument();

  const tavilyRow = screen.getByText("Tavily").closest("div.rounded-lg") as HTMLElement;
  expect(within(tavilyRow).getByText("Not configured")).toBeInTheDocument();

  const searchRow = screen.getByRole("button", { name: /web search/i });
  expect(searchRow.textContent).toContain("1 of 2");
});

test("saving a key calls PUT /api/v1/search-keys and flips the row to Configured", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Brave Search");
  const braveRow = screen.getByText("Brave Search").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(braveRow).getByRole("button", { name: "Add key" }));

  const input = within(braveRow).getByPlaceholderText("Brave Search API key");
  await user.type(input, "sekrit-brave-value");
  await user.click(within(braveRow).getByRole("button", { name: "Save" }));

  await waitFor(() => expect(within(braveRow).getByText("Configured")).toBeInTheDocument());
  expect(searchKeysState.brave).toBe(true);
  // The pasted value never lingers in the DOM after save.
  expect(screen.queryByDisplayValue("sekrit-brave-value")).not.toBeInTheDocument();
});

test("clearing a configured key calls DELETE /api/v1/search-keys/:provider and flips back to Not configured", async () => {
  searchKeysState = { brave: true, tavily: false };
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Brave Search");
  const braveRow = screen.getByText("Brave Search").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(braveRow).getByRole("button", { name: "Clear" }));

  await waitFor(() => expect(within(braveRow).getByText("Not configured")).toBeInTheDocument());
  expect(searchKeysState.brave).toBe(false);
});

test("web search key input is masked (type=password) so the value is never shown in plain text", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Tavily");
  const tavilyRow = screen.getByText("Tavily").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(tavilyRow).getByRole("button", { name: "Add key" }));
  const input = within(tavilyRow).getByPlaceholderText("Tavily API key") as HTMLInputElement;
  expect(input.type).toBe("password");
});

test("shows an error banner when the search-keys list fails to load", async () => {
  searchKeysStatus = 500;
  mockFetch();
  wrap();

  expect(await screen.findByText("could not load web search settings")).toBeInTheDocument();
});
