import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell, useSlideOver } from "./AppShell";

function Page() {
  const { open } = useSlideOver();
  return (
    <button onClick={() => open(<div>PANEL-CONTENT</div>, { title: "Details" })}>
      open panel
    </button>
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          authenticated: true,
          owner: { id: "o1", username: "admin", must_change_password: false },
          workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
          workspaces: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Page />} />
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
