import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import { useListNav } from "./useKeyboardNav";

// AppShell's rail-shortcut hook calls react-router's useNavigate() — spy on
// it directly rather than asserting on location state, per the brief's own
// test sketch (`expect(navigateSpy).toHaveBeenCalledWith(...)`).
const navigateSpy = vi.fn();
vi.mock("react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router")>();
  return { ...actual, useNavigate: () => navigateSpy };
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

// A page with a plain text input, mounted inside AppShell — exercises the
// suppression guard against a real <input> the way the note editor / chat
// composer / designer conversation would.
function AppShellWithInput() {
  return <input aria-label="scratch" />;
}

function wrap(page = <AppShellWithInput />) {
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
            <Route path="/" element={page} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  navigateSpy.mockClear();
});

test("single-key shortcuts do not fire while typing", async () => {
  wrap();
  const input = await screen.findByRole("textbox");
  input.focus();
  fireEvent.keyDown(input, { key: "j" });
  expect(navigateSpy).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "?" });
  expect(screen.queryByRole("dialog", { name: /shortcuts/i })).not.toBeInTheDocument();
});

test("cmd+1..7 navigate to the rail destinations", async () => {
  wrap();
  await screen.findByRole("textbox");
  fireEvent.keyDown(document.body, { key: "1", metaKey: true });
  expect(navigateSpy).toHaveBeenCalledWith("/");
  fireEvent.keyDown(document.body, { key: "3", metaKey: true });
  expect(navigateSpy).toHaveBeenCalledWith("/agents");
  fireEvent.keyDown(document.body, { key: "7", metaKey: true });
  expect(navigateSpy).toHaveBeenCalledWith("/secrets");
});

// Modifier shortcuts stay active even when the focus is inside an input —
// only the bare-key shortcuts (j/k/?) are suppressed there.
test("cmd+1..7 still navigate while an input is focused", async () => {
  wrap();
  const input = await screen.findByRole("textbox");
  input.focus();
  fireEvent.keyDown(input, { key: "2", metaKey: true });
  expect(navigateSpy).toHaveBeenCalledWith("/kb");
});

test("? opens the shortcuts overlay and Esc closes it", async () => {
  wrap();
  await screen.findByRole("textbox");
  fireEvent.keyDown(document.body, { key: "?" });
  const dialog = await screen.findByRole("dialog", { name: /shortcuts/i });
  expect(dialog).toBeInTheDocument();
  // Lists the pre-existing shortcuts alongside the new ones.
  expect(screen.getByText(/⌘K/)).toBeInTheDocument();
  expect(screen.getByText(/⌘J/)).toBeInTheDocument();
  expect(screen.getByText(/⌘S/)).toBeInTheDocument();
  fireEvent.keyDown(dialog, { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: /shortcuts/i })).not.toBeInTheDocument();
});

type Row = { id: string; label: string };

function ListFixture({ rows, onOpen }: { rows: Row[]; onOpen: (r: Row) => void }) {
  const { highlightedIndex } = useListNav(rows, onOpen);
  return (
    <ul>
      {rows.map((r, i) => (
        <li key={r.id} data-highlighted={i === highlightedIndex}>
          {r.label}
        </li>
      ))}
    </ul>
  );
}

test("j/k move the highlight and Enter opens", () => {
  const rows: Row[] = [{ id: "a", label: "Alpha" }, { id: "b", label: "Bravo" }, { id: "c", label: "Charlie" }];
  const onOpen = vi.fn();
  render(<ListFixture rows={rows} onOpen={onOpen} />);

  expect(screen.getByText("Alpha").closest("li")).toHaveAttribute("data-highlighted", "true");

  fireEvent.keyDown(document.body, { key: "j" });
  expect(screen.getByText("Bravo").closest("li")).toHaveAttribute("data-highlighted", "true");

  fireEvent.keyDown(document.body, { key: "j" });
  expect(screen.getByText("Charlie").closest("li")).toHaveAttribute("data-highlighted", "true");

  // Clamped at the end — a third "j" does not run off the list.
  fireEvent.keyDown(document.body, { key: "j" });
  expect(screen.getByText("Charlie").closest("li")).toHaveAttribute("data-highlighted", "true");

  fireEvent.keyDown(document.body, { key: "k" });
  expect(screen.getByText("Bravo").closest("li")).toHaveAttribute("data-highlighted", "true");

  fireEvent.keyDown(document.body, { key: "Enter" });
  expect(onOpen).toHaveBeenCalledWith(rows[1]);
});

test("j/k do not move the highlight while typing in an input", () => {
  const rows: Row[] = [{ id: "a", label: "Alpha" }, { id: "b", label: "Bravo" }];
  const onOpen = vi.fn();
  render(
    <div>
      <input aria-label="scratch" />
      <ListFixture rows={rows} onOpen={onOpen} />
    </div>,
  );
  const input = screen.getByRole("textbox");
  input.focus();
  fireEvent.keyDown(input, { key: "j" });
  expect(screen.getByText("Alpha").closest("li")).toHaveAttribute("data-highlighted", "true");
  fireEvent.keyDown(input, { key: "Enter" });
  expect(onOpen).not.toHaveBeenCalled();
});
