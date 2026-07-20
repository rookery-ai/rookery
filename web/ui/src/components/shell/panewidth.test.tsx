import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import {
  clampPaneWidth,
  PANE_MIN,
  PANE_MAX,
  PANE_DEFAULT,
  readStoredWidth,
  PaneResizeHandle,
} from "./usePaneWidth";
import { AppShell, ContextPane } from "./AppShell";

test("clamps to range", () => {
  expect(clampPaneWidth(50)).toBe(PANE_MIN);
  expect(clampPaneWidth(9999)).toBe(PANE_MAX);
  expect(clampPaneWidth(300)).toBe(300);
});

test("corrupt stored value falls back to default", () => {
  localStorage.setItem("sa.paneWidth", "not-a-number");
  expect(readStoredWidth()).toBe(PANE_DEFAULT);
  localStorage.setItem("sa.paneWidth", "99999");
  expect(readStoredWidth()).toBe(PANE_DEFAULT);
  localStorage.removeItem("sa.paneWidth");
  expect(readStoredWidth()).toBe(PANE_DEFAULT);
});

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_FIXTURE = {
  authenticated: true,
  owner: { id: "o1", username: "admin", must_change_password: false },
  workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
  workspaces: [],
};

function Page() {
  return (
    <ContextPane>
      <div>PANE-CONTENT</div>
    </ContextPane>
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/inbox/poll") return Promise.resolve(jsonResponse({ unread: 0, recent: [] }));
      return Promise.resolve(jsonResponse(SESSION_FIXTURE));
    }),
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

beforeEach(() => {
  localStorage.removeItem("sa.paneWidth");
});

test("arrow keys resize and Home/End jump", async () => {
  wrap();
  const sep = await screen.findByRole("separator", { name: /resize sidebar/i });
  sep.focus();
  await userEvent.keyboard("{ArrowRight}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_DEFAULT + 16));
  await userEvent.keyboard("{Home}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_MIN));
  await userEvent.keyboard("{End}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_MAX));
  await userEvent.keyboard("{ArrowLeft}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_MAX - 16));
});

test("double-click resets to default", async () => {
  wrap();
  const sep = await screen.findByRole("separator", { name: /resize sidebar/i });
  sep.focus();
  await userEvent.keyboard("{ArrowRight}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_DEFAULT + 16));
  await userEvent.dblClick(sep);
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_DEFAULT));
});

test("aside has full accessible separator attributes and applies width via inline style", async () => {
  wrap();
  const sep = await screen.findByRole("separator", { name: /resize sidebar/i });
  expect(sep).toHaveAttribute("aria-orientation", "vertical");
  expect(sep).toHaveAttribute("aria-valuemin", String(PANE_MIN));
  expect(sep).toHaveAttribute("aria-valuemax", String(PANE_MAX));
  expect(sep).toHaveAttribute("tabindex", "0");

  const aside = await screen.findByText("PANE-CONTENT");
  const asideEl = aside.closest("aside");
  expect(asideEl).not.toBeNull();
  expect(asideEl).toHaveStyle({ width: `${PANE_DEFAULT}px` });
});

test("pointer drag resizes via pointer capture", async () => {
  wrap();
  const sep = await screen.findByRole("separator", { name: /resize sidebar/i });
  const capture = vi.fn();
  const release = vi.fn();
  sep.setPointerCapture = capture;
  sep.releasePointerCapture = release;

  fireEvent.pointerDown(sep, { pointerId: 1, clientX: 100 });
  expect(capture).toHaveBeenCalledWith(1);
  expect(document.body.style.userSelect).toBe("none");

  fireEvent.pointerMove(sep, { pointerId: 1, clientX: 150 });
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_DEFAULT + 50));

  fireEvent.pointerUp(sep, { pointerId: 1, clientX: 150 });
  expect(release).toHaveBeenCalledWith(1);
  expect(document.body.style.userSelect).toBe("");
});

test("restores user-select if the handle unmounts mid-drag", () => {
  const { unmount } = render(
    <PaneResizeHandle width={PANE_DEFAULT} setWidth={() => {}} reset={() => {}} />,
  );
  const sep = screen.getByRole("separator", { name: /resize sidebar/i });

  fireEvent.pointerDown(sep, { pointerId: 1, clientX: 100 });
  expect(document.body.style.userSelect).toBe("none");

  // No pointerup/pointercancel — the handle is torn out from under the
  // pointer (e.g. a route change collapses the context pane mid-drag).
  unmount();

  expect(document.body.style.userSelect).toBe("");
});
