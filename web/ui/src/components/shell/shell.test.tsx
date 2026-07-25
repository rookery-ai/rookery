import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { useState } from "react";
import { AppShell, ContextPane, useSlideOver } from "./AppShell";

function Page() {
  const { open } = useSlideOver();
  return (
    <button onClick={() => open(<div>PANEL-CONTENT</div>, { title: "Details" })}>
      open panel
    </button>
  );
}

function PaneToggle() {
  const [shown, setShown] = useState(true);
  return (
    <div>
      <button onClick={() => setShown(false)}>hide pane</button>
      {shown && (
        <ContextPane>
          <div>PANE</div>
        </ContextPane>
      )}
    </div>
  );
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

// unread: what /api/v1/inbox/poll reports (defaults to 0, matching every
// pre-existing test's expectation of no badge). Every other URL — session
// included — falls back to the session fixture, same as the original
// blanket mock this replaces.
function wrap(page = <Page />, unread = 0) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/inbox/poll") return Promise.resolve(jsonResponse({ unread, recent: [] }));
      return Promise.resolve(jsonResponse(SESSION_FIXTURE));
    }),
  );
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={page} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("renders rail items and opens the slide-over", async () => {
  wrap();
  expect(await screen.findByLabelText(/agents/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/knowledge base/i)).toBeInTheDocument();
  await userEvent.click(screen.getByText("open panel"));
  expect(await screen.findByText("PANEL-CONTENT")).toBeInTheDocument();
  expect(screen.getByText("Details")).toBeInTheDocument();
});

test("ContextPane mounts content into the shell and clears it on unmount", async () => {
  wrap(<PaneToggle />);
  expect(await screen.findByText("PANE")).toBeInTheDocument();
  await userEvent.click(screen.getByText("hide pane"));
  expect(screen.queryByText("PANE")).not.toBeInTheDocument();
});

// The badge itself must NOT carry its own aria-label: a nested element's
// aria-label is ignored by the accessible-name algorithm once an ancestor
// (the NavLink) already has an explicit aria-label — so the unread count
// has to be folded into the LINK's own label, not the badge's.
test("Home rail icon shows an unread badge with the poll count in the link's accessible name", async () => {
  wrap(<Page />, 3);
  const link = await screen.findByLabelText("Home (3 unread)");
  expect(link).toHaveTextContent("3");
});

test("Home rail icon shows no badge when the poll count is 0, and the accessible name has no unread suffix", async () => {
  wrap(<Page />, 0);
  const link = await screen.findByLabelText("Home");
  expect(link.textContent).not.toMatch(/unread/i);
  expect(screen.queryByLabelText(/unread/i)).not.toBeInTheDocument();
});

test("Home rail icon caps the visible badge label at '9+' but states the real count in the accessible name", async () => {
  wrap(<Page />, 42);
  const link = await screen.findByLabelText("Home (42 unread)");
  expect(link).toHaveTextContent("9+");
});

// ── Rail: active vs. inactive affordance ───────────────────────────────────

test("the active rail item is visually distinguished from an inactive one", async () => {
  wrap();

  // Home is the active route in this harness.
  const active = await screen.findByLabelText(/^home$/i);
  const inactive = screen.getByLabelText(/agents/i);

  // The old active style was `bg-border` against a `bg-chrome` rail — a
  // near-invisible difference. Active now carries a soft accent surface AND
  // accent text, so the two states differ on more than one channel.
  expect(active.className).toMatch(/bg-accent-soft/);
  expect(active.className).toMatch(/text-accent/);
  expect(inactive.className).not.toMatch(/bg-accent-soft/);

  // An inactive item must still SAY it responds to the pointer — the rail
  // previously had no perceptible hover at all.
  expect(inactive.className).toMatch(/hover:bg-muted-surface/);

  // Colour alone is not the active signal: a 3px bar on the rail's inner edge
  // gives it a shape a colour-blind or low-contrast viewer can still read.
  expect(active.querySelector("[data-testid='rail-active-bar']")).not.toBeNull();
  expect(inactive.querySelector("[data-testid='rail-active-bar']")).toBeNull();
});

// The regression this test really guards: every rail item is a Radix
// TooltipTrigger `asChild`, and Slot merges the child's className into a
// STRING before NavLink can resolve a function form. A function className
// therefore reached the DOM as its own stringified source, and none of the
// rail's styling applied — which is exactly why the rail read as having no
// hover and no current-page indication. A plain string survives the merge.
test("rail items render real class names, not a stringified className function", async () => {
  wrap();
  const home = await screen.findByLabelText(/^home$/i);
  expect(home.className).not.toMatch(/=>/);
  expect(home.className).not.toMatch(/isActive/);
  expect(home.className).toMatch(/^[\w\s:/[\]().,%#-]+$/);
});

test("the settings rail item is a gear, not an avatar monogram", async () => {
  wrap();
  const settings = await screen.findByLabelText(/profile & settings/i);

  // lucide stamps its component name into the class list, which is the only
  // stable handle on WHICH glyph rendered.
  expect(settings.querySelector("svg.lucide-settings")).not.toBeNull();
  // The old avatar rendered the owner's initial as text inside a circle.
  expect(settings.textContent).toBe("");
});

test("the rail offers a Lock control, positioned above Settings", async () => {
  // Lock is an action, not a destination, so it sits with the account-level
  // controls at the bottom of the rail rather than among the nav items.
  wrap();
  const lock = await screen.findByLabelText("Lock");
  const settings = screen.getByLabelText("Profile & Settings");
  expect(lock.tagName).toBe("BUTTON");
  // DOCUMENT_POSITION_FOLLOWING === 4: settings comes after lock in the DOM.
  expect(lock.compareDocumentPosition(settings) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

test("clicking Lock posts to the lock endpoint", async () => {
  wrap();
  const lock = await screen.findByLabelText("Lock");
  await userEvent.click(lock);

  await waitFor(() => {
    const posted = vi.mocked(fetch).mock.calls.some(
      ([url]) => String(url) === "/api/v1/auth/lock",
    );
    expect(posted).toBe(true);
  });
});
