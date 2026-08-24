import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route, useSearchParams } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import ConnectionsPage, { isServiceBlocked } from "./ConnectionsPage";
import type { ConnectorPlatform, PreflightProblem, ServiceProvider } from "@/lib/connections";

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
// Last platform PUT /api/v1/connectors/:platform/primary was called with —
// lets tests assert the primary-radio's onChange actually reaches the API,
// not just that the radio renders checked.
let lastPrimaryPut: string | null = null;
// Response the PUT mock returns. Mirrors apiPutSearchKeyResponse: the save now
// reports whether the key was actually proven against the live provider.
let searchKeyPutResponse: { ok: boolean; verified?: boolean; note?: string };

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
      linked: true,
      linked_identity: "123456789",
      primary: true,
      dm_url: "",
      invite_url: "",
      bot_online: true,
    },
    {
      platform: "slack",
      label: "Slack",
      blurb: "",
      setup_steps: [],
      fields: [],
      connected: false,
      identity: "",
      linked: false,
      linked_identity: "",
      primary: false,
      dm_url: "",
      invite_url: "",
      bot_online: false,
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
  searchKeyPutResponse = { ok: true, verified: true };
  searchKeysStatus = 200;
  lastPrimaryPut = null;
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
        return Promise.resolve(jsonResponse(searchKeyPutResponse));
      }

      if (url.startsWith("/api/v1/search-keys/") && method === "DELETE") {
        const provider = url.split("/").pop();
        if (provider === "brave") searchKeysState.brave = false;
        if (provider === "tavily") searchKeysState.tavily = false;
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      const primaryMatch = url.match(/^\/api\/v1\/connectors\/([^/]+)\/primary$/);
      if (primaryMatch && method === "PUT") {
        lastPrimaryPut = primaryMatch[1];
        platforms = platforms.map((p) => ({ ...p, primary: p.platform === primaryMatch[1] }));
        return Promise.resolve(jsonResponse({ ok: true }));
      }

      return Promise.resolve(jsonResponse({}));
    }),
  );
}

// Every ConnectorPlatform field, for tests that only care about a couple of
// them — spread over it and override. `primary: false` by default: a test
// that needs a primary app sets it explicitly, so an unlinked entry spread
// over this fixture never silently describes the impossible
// `linked: false, primary: true` combination.
const CHAT_APP_FIXTURE: ConnectorPlatform = {
  platform: "telegram",
  label: "Telegram",
  blurb: "",
  setup_steps: [],
  fields: [],
  connected: true,
  identity: "@bot",
  linked: true,
  linked_identity: "123",
  primary: false,
  dm_url: "",
  invite_url: "",
  bot_online: true,
};

// Stubs GET /api/v1/connectors with exactly the given platforms (overriding
// the shared `platforms` fixture used elsewhere in this file) and renders the
// page.
function renderConnections(platformsList: ConnectorPlatform[]) {
  platforms = platformsList;
  mockFetch();
  return wrap();
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

test("renders chat-app cards: linked shows identity + Manage, not-connected shows Connect", async () => {
  mockFetch();
  wrap();

  // "Telegram" also appears in the primary-delivery chooser below (it's the
  // only linked app), so scope the card lookup to the first match.
  expect((await screen.findAllByText("Telegram"))[0]).toBeInTheDocument();
  expect(screen.getByText("@rookie_assistant_bot")).toBeInTheDocument();
  // The Telegram fixture is connected AND linked, so the card must show the
  // linked-identity status, never the bare "Connected" claim.
  expect(screen.getByText(/linked as 123456789/i)).toBeInTheDocument();
  const telegramCard = screen.getAllByText("Telegram")[0].closest("div.rounded-lg")!;
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

  await screen.findAllByText("Telegram");
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

  await screen.findAllByText("Telegram");
  await user.click(screen.getByRole("button", { name: /chat apps/i }));
  expect(scrollSpy).toHaveBeenCalledTimes(1);

  await user.click(screen.getByRole("button", { name: /^services/i }));
  expect(scrollSpy).toHaveBeenCalledTimes(2);
});

test("explainer card shows the mockup copy", async () => {
  mockFetch();
  wrap();
  await screen.findAllByText("Telegram");
  expect(screen.getByText(/are where you talk to your assistant/)).toBeInTheDocument();
  expect(screen.getByText(/are the accounts your agents can act on/)).toBeInTheDocument();
});

// The explainer described three of the four sections its own nav lists, so MCP
// servers read as a lesser thing rather than the fourth peer. Asserted as
// "every nav section has a paragraph" rather than by quoting the new sentence,
// so adding a fifth section fails here too instead of silently shipping
// another undescribed one.
test("explainer card describes every section the nav lists", async () => {
  mockFetch();
  wrap();
  await screen.findAllByText("Telegram");
  for (const section of [
    /^Chat apps$/,
    /^Services$/,
    /^MCP servers$/,
    /^Web search$/,
  ]) {
    expect(screen.getByText(section, { selector: "b" })).toBeInTheDocument();
  }
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

  await screen.findAllByText("Telegram");
  const telegramCard = screen.getAllByText("Telegram")[0].closest("div.rounded-lg") as HTMLElement;
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

// ── Blocked service tiles (preflight) ───────────────────────────────────────
// Preflight already ships on every provider and the wizard already disables
// Connect on a hard problem; these pin the tile-level signal that lets a user
// learn about it before picking the service at all.

const HARD_PROBLEM: PreflightProblem = {
  severity: "hard",
  code: "scheme_not_https",
  message: "Plain http is rejected by this provider.",
  fix: "Serve the instance over https.",
};
const SOFT_PROBLEM: PreflightProblem = {
  severity: "soft",
  code: "unverified_host",
  message: "This host has not been verified.",
  fix: "",
};

test("a hard preflight problem blocks a provider with no connections", () => {
  expect(
    isServiceBlocked({ preflight: [HARD_PROBLEM], connections: [] } as never),
  ).toBe(true);
});

test("a soft preflight problem never blocks", () => {
  // unverified_host is soft precisely so a stale policy cannot lock anyone out.
  expect(
    isServiceBlocked({ preflight: [SOFT_PROBLEM], connections: [] } as never),
  ).toBe(false);
});

test("a provider with existing connections is never blocked", () => {
  // Those connections still work; the wizard is the only way to inspect or
  // delete them, so the tile has to stay reachable.
  expect(
    isServiceBlocked({
      preflight: [HARD_PROBLEM],
      connections: [{ id: "c1" }],
    } as never),
  ).toBe(false);
});

test("a blocked tile explains itself instead of opening the wizard", async () => {
  const user = userEvent.setup();
  providers = [
    {
      name: "google",
      label: "Google",
      // A category distinct from the label itself — with category and label
      // both "Google", findByText(/Google/) below would match the category
      // heading too, not just the tile and the dialog title.
      category: "Other",
      kind: "oauth",
      setup_url: "",
      setup_steps: [],
      has_creds: false,
      action_count: 0,
      redirect_uri: "",
      preflight: [HARD_PROBLEM],
      connect_inputs: [],
      connections: [],
    },
  ];
  mockFetch();
  wrap();

  const tile = await screen.findByRole("button", { name: /Google/ });
  expect(tile).toHaveAttribute("aria-disabled", "true");
  await user.click(tile);

  // The dialog quotes the API's own strings — one wording, not two.
  expect(await screen.findByText(/Plain http is rejected/)).toBeInTheDocument();
  expect(screen.getByText(/Serve the instance over https/)).toBeInTheDocument();
  const remedyLink = screen.getByRole("link", { name: /Change the instance URL/ });
  expect(remedyLink).toBeInTheDocument();
  // The instance URL setting lives at the owner-instance-url settings
  // section, not the default Profile section — landing on Profile would
  // defeat the whole point of this button.
  expect(remedyLink).toHaveAttribute("href", "/settings?section=owner-instance-url");
});

test("Open anyway reaches the wizard from a blocked tile", async () => {
  // The hard block predicts a third party's rules rather than an invariant we
  // own, so a stale redirect_policy entry must never become a lockout.
  const user = userEvent.setup();
  providers = [
    {
      name: "google",
      label: "Google",
      // A category distinct from the label itself — with category and label
      // both "Google", findByText(/Google/) below would match the category
      // heading too, not just the tile and the dialog title.
      category: "Other",
      kind: "oauth",
      setup_url: "",
      setup_steps: [],
      has_creds: false,
      action_count: 0,
      redirect_uri: "",
      preflight: [HARD_PROBLEM],
      connect_inputs: [],
      connections: [],
    },
  ];
  mockFetch();
  wrap();

  await user.click(await screen.findByRole("button", { name: /Google/ }));
  await user.click(await screen.findByRole("button", { name: /Open anyway/ }));
  // Exact text, not a substring/alternation: the blocked tile itself stays
  // mounted behind the slide-over (it's a panel, not a route swap), so a
  // bare /Google/ match would also hit the tile's own label.
  expect(await screen.findByText("Connect Google")).toBeInTheDocument();
});

test("an unblocked tile opens the wizard directly", async () => {
  const user = userEvent.setup();
  providers = [
    {
      name: "github",
      label: "GitHub",
      category: "Developer",
      kind: "oauth",
      setup_url: "",
      setup_steps: [],
      has_creds: true,
      action_count: 0,
      redirect_uri: "",
      preflight: [],
      connect_inputs: [],
      connections: [],
    },
  ];
  mockFetch();
  wrap();

  await user.click(await screen.findByRole("button", { name: /GitHub/ }));
  // Positive assertion: the wizard actually opened, not merely that the
  // blocked-tile dialog didn't.
  // Match the sheet's heading specifically: the wizard's submit button now
  // carries the same words (its trailing "→" was removed, since the button
  // already renders a lucide icon), so a bare text query matches both.
  expect(
    await screen.findByRole("heading", { name: "Connect GitHub" }),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /Open anyway/ }),
  ).not.toBeInTheDocument();
});

test("shows an error banner when the connectors list fails to load", async () => {
  connectorsStatus = 500;
  mockFetch();
  wrap();

  expect(await screen.findByText("could not load chat apps")).toBeInTheDocument();
});

// ── Link state + primary chooser ────────────────────────────────────────────

test("a connected but unlinked app is not shown as ready", async () => {
  renderConnections([
    { ...CHAT_APP_FIXTURE, platform: "discord", label: "Discord", connected: true, linked: false },
  ]);

  expect(await screen.findByText(/not linked yet/i)).toBeInTheDocument();
  expect(screen.queryByText(/^connected$/i)).not.toBeInTheDocument();
});

// Also proves the radio list is filtered to truly linked apps: Discord here
// is connected but not linked, so only Telegram counts — leaving exactly one
// linked app, which per the finding below must render the sentence alone.
test("exactly one linked app: shows the delivery line without a picker", async () => {
  renderConnections([
    {
      ...CHAT_APP_FIXTURE,
      platform: "telegram",
      label: "Telegram",
      connected: true,
      linked: true,
      linked_identity: "100000001",
      primary: true,
    },
    { ...CHAT_APP_FIXTURE, platform: "discord", label: "Discord", connected: true, linked: false },
  ]);

  // With a single linked app there's nothing to choose between — the heading
  // and radio are noise, so only the plain sentence renders.
  expect(await screen.findByText(/delivered to Telegram/i)).toBeInTheDocument();
  expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  expect(
    screen.queryByText(/where should agent runs and reminders go/i),
  ).not.toBeInTheDocument();
});

test("two or more linked apps: shows the picker with one radio per linked app", async () => {
  renderConnections([
    {
      ...CHAT_APP_FIXTURE,
      platform: "telegram",
      label: "Telegram",
      connected: true,
      linked: true,
      linked_identity: "100000001",
      primary: true,
    },
    {
      ...CHAT_APP_FIXTURE,
      platform: "discord",
      label: "Discord",
      connected: true,
      linked: true,
      linked_identity: "ilija#4821",
      primary: false,
    },
    // Connected but unlinked — must not count toward the 2+ gate nor appear
    // as a radio option.
    { ...CHAT_APP_FIXTURE, platform: "slack", label: "Slack", connected: true, linked: false },
  ]);

  expect(
    await screen.findByText(/where should agent runs and reminders go/i),
  ).toBeInTheDocument();
  const radios = screen.getAllByRole("radio");
  expect(radios).toHaveLength(2);
  expect(radios[0]).toBeChecked();
  expect(screen.getByText(/delivered to Telegram/i)).toBeInTheDocument();
});

test("clicking a non-primary linked app's radio calls PUT .../primary for that platform", async () => {
  const user = userEvent.setup();
  renderConnections([
    {
      ...CHAT_APP_FIXTURE,
      platform: "telegram",
      label: "Telegram",
      connected: true,
      linked: true,
      linked_identity: "100000001",
      primary: true,
    },
    {
      ...CHAT_APP_FIXTURE,
      platform: "discord",
      label: "Discord",
      connected: true,
      linked: true,
      linked_identity: "ilija#4821",
      primary: false,
    },
  ]);

  const radios = await screen.findAllByRole("radio");
  expect(radios).toHaveLength(2);
  const discordRadio = screen.getByRole("radio", { name: /discord/i });
  expect(discordRadio).not.toBeChecked();

  await user.click(discordRadio);

  await waitFor(() => expect(lastPrimaryPut).toBe("discord"));
  // The list refetches on mutation success and the radio flips to reflect it.
  await waitFor(() => expect(discordRadio).toBeChecked());
});

test("no linked apps: the primary chooser is omitted entirely", async () => {
  renderConnections([
    { ...CHAT_APP_FIXTURE, platform: "discord", label: "Discord", connected: true, linked: false },
  ]);

  await screen.findByText(/not linked yet/i);
  expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  expect(
    screen.queryByText(/where should agent runs and reminders go/i),
  ).not.toBeInTheDocument();
});

// Disconnect removes the credentials row but not the identity row, so a
// platform can read `linked: true` while `connected: false` — the card shows
// "Not connected" but the identity from a past link is still on file. That
// combination must not be offered as a delivery target: filtering `linked`
// alone would 400 the moment it's picked, since there's no live connection
// left to deliver through.
test("a linked-but-disconnected app (Disconnect removed the credentials) gets no radio", async () => {
  renderConnections([
    {
      ...CHAT_APP_FIXTURE,
      platform: "discord",
      label: "Discord",
      connected: false,
      linked: true,
      linked_identity: "ilija#4821",
      primary: false,
    },
  ]);

  await screen.findByText("Not connected");
  expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  expect(
    screen.queryByText(/where should agent runs and reminders go/i),
  ).not.toBeInTheDocument();
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

  // Matches both "Configured" and "Configured — key verified": this test is
  // about the row flipping state, not about the verification wording.
  await waitFor(() => expect(within(braveRow).getByText(/^Configured/)).toBeInTheDocument());
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

test("a verified save says so, so 'configured' is not mistaken for 'working'", async () => {
  searchKeyPutResponse = { ok: true, verified: true };
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Brave Search");
  const braveRow = screen.getByText("Brave Search").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(braveRow).getByRole("button", { name: "Add key" }));
  await user.type(within(braveRow).getByPlaceholderText("Brave Search API key"), "good-key");
  await user.click(within(braveRow).getByRole("button", { name: "Save" }));

  await waitFor(() =>
    expect(within(braveRow).getByText("Configured — key verified")).toBeInTheDocument(),
  );
});

test("an unverifiable save surfaces the server's note instead of looking like success", async () => {
  // The exact case that made this whole change necessary: the provider host is
  // unreachable, the key is stored anyway, and without this note the row would
  // read "Configured" while search silently fell back to keyless scraping.
  searchKeyPutResponse = {
    ok: true,
    verified: false,
    note: "saved, but brave's API host could not be reached — it resolved into blocked address space, which usually means local DNS filtering.",
  };
  mockFetch();
  const user = userEvent.setup();
  wrap();

  await screen.findByText("Brave Search");
  const braveRow = screen.getByText("Brave Search").closest("div.rounded-lg") as HTMLElement;
  await user.click(within(braveRow).getByRole("button", { name: "Add key" }));
  await user.type(within(braveRow).getByPlaceholderText("Brave Search API key"), "fine-key");
  await user.click(within(braveRow).getByRole("button", { name: "Save" }));

  await waitFor(() =>
    expect(within(braveRow).getByText(/blocked address space/i)).toBeInTheDocument(),
  );
  expect(within(braveRow).queryByText("Configured — key verified")).not.toBeInTheDocument();
});
