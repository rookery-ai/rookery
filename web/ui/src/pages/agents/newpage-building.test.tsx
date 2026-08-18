import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route, useLocation } from "react-router";
import AgentNewPage from "./AgentNewPage";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function stubDesignState(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/design/state")) return Promise.resolve(jsonResponse(body));
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

// Renders the exact route shape "Open it" navigates within (both the notice
// and its target are the SAME /agents/new route, distinguished only by the
// resume query param — see AgentNewPage's own comment on `buildInProgress`).
// LocationProbe is a sibling so a click's effect on the URL is observable
// without a second stub page masking what actually changed.
function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}{location.search}</div>;
}

function renderNewPage(initialEntry = "/agents/new") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route
            path="/agents/new"
            element={
              <>
                <LocationProbe />
                <AgentNewPage />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} } as never);
});
afterEach(() => vi.unstubAllGlobals());

// The design session is a per-workspace SINGLETON, so opening New Agent in a second
// tab while a build runs adopts the in-flight session rather than starting fresh.
// Presenting an apparently-blank form is what made that read as broken: the user
// filled it in and nothing they typed could start anything.
test("New Agent says a build is already running instead of offering a fresh form", async () => {
  stubDesignState({ active: true, generating: true, name: "drive checker" });

  renderNewPage();

  await waitFor(() => {
    expect(screen.getByText(/already building/i)).toBeInTheDocument();
  });
  expect(screen.getByRole("button", { name: /open|view/i })).toBeInTheDocument();
});

// A session can be `active` (a draft exists) without a build actually
// running — that's the ordinary "resume this draft" case, not the one this
// page needs to intercept. Only `generating` gates the notice; this pins
// that distinction against a fixture that would still pass if the check
// were mistakenly keyed on `active` instead.
test("an active-but-not-generating session does not show the notice, and the normal form renders", async () => {
  stubDesignState({ active: true, generating: false });

  renderNewPage();

  await waitFor(() => {
    expect(screen.getByText(/create an agent/i)).toBeInTheDocument();
  });
  expect(screen.queryByText(/already building/i)).not.toBeInTheDocument();
});

// The notice exists to stop a SECOND, unaware attempt to start a new build —
// an explicit resume (?resume=1, e.g. from the notice's own "Open it" link,
// or a draft's Resume action) must never be blocked by the very state it's
// trying to reach. Without this test, a future refactor could make the
// button navigate into a notice that immediately re-shows itself.
test("an explicit resume (?resume=1) is not blocked by its own generating state", async () => {
  stubDesignState({ active: true, generating: true, name: "drive checker" });

  renderNewPage("/agents/new?resume=1");

  await waitFor(() => {
    expect(screen.queryByText(/already building/i)).not.toBeInTheDocument();
  });
});

test("clicking Open it navigates to /agents/new?resume=1", async () => {
  stubDesignState({ active: true, generating: true, name: "drive checker" });

  renderNewPage();

  const openButton = await screen.findByRole("button", { name: /open|view/i });
  await userEvent.click(openButton);

  await waitFor(() => {
    expect(screen.getByTestId("location")).toHaveTextContent("/agents/new?resume=1");
  });
});
