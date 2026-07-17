import { render, screen } from "@testing-library/react";
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

test("Home rail icon shows an unread badge with the poll count", async () => {
  wrap(<Page />, 3);
  expect(await screen.findByLabelText("3 unread")).toHaveTextContent("3");
});

test("Home rail icon shows no badge when the poll count is 0", async () => {
  wrap(<Page />, 0);
  await screen.findByLabelText(/agents/i); // wait for the rail to settle
  expect(screen.queryByLabelText(/unread/i)).not.toBeInTheDocument();
});

test("Home rail icon caps the badge label at '9+' for large counts", async () => {
  wrap(<Page />, 42);
  expect(await screen.findByLabelText("42 unread")).toHaveTextContent("9+");
});
