import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
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

// A page with a contenteditable region, mounted inside AppShell — exercises
// the guard against the shape the WYSIWYG note editor actually takes: a
// SINGLE explicitly-contenteditable root (ProseMirror sets
// `element.contentEditable = "true"` once, on its own root view element —
// see prosemirror-view/dist/index.js) with plain, non-contenteditable child
// nodes (<p>, <span>, …) that inherit editability per the DOM spec rather
// than each declaring it themselves. The keydown target we assert against
// is the nested child, not the root, precisely because that inheritance is
// the thing under test — a check that only recognised the root itself would
// still pass a same-element test but miss this one.
//
// Note: jsdom does not implement `HTMLElement.isContentEditable` — the
// property getter always returns `undefined`, confirmed by probing both a
// bare DOM contenteditable div and a real mounted TipTap/ProseMirror editor
// in this same environment. So `isTypingTarget`'s attribute-based
// `.closest('[contenteditable="true"], [contenteditable=""]')` fallback is
// not a redundant belt-and-braces check — in this test suite, it is the
// ONLY thing that can make this test (and thus this hazard) pass or fail.
// Relying on `el.isContentEditable` alone left this exact branch with zero
// regression coverage: deleting it left all 6 pre-existing tests green.
function AppShellWithContentEditable() {
  return (
    <div contentEditable="true" data-testid="editable-root">
      <p data-testid="editable-child">Some note text the user is composing.</p>
    </div>
  );
}

// A page with a real <select>, mounted inside AppShell — a bare "j"/"k" on a
// focused <select> also drives the browser's native type-ahead (jumps to
// the next option starting with that letter), so it needs the same
// suppression as input/textarea/contenteditable.
function AppShellWithSelect() {
  return (
    <select aria-label="scratch-select">
      <option value="a">A</option>
      <option value="b">B</option>
    </select>
  );
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

test("? does not open the shortcuts overlay while typing in the WYSIWYG note editor's contenteditable region", async () => {
  wrap(<AppShellWithContentEditable />);
  const child = await screen.findByTestId("editable-child");
  // Target the CHILD <p>, not the contenteditable root — this is the node
  // ProseMirror's own DOM actually nests text in; only inherited
  // editability (not a tag/attribute check on this exact element) can
  // catch it.
  fireEvent.keyDown(child, { key: "?" });
  expect(screen.queryByRole("dialog", { name: /shortcuts/i })).not.toBeInTheDocument();
});

test("? does not open the shortcuts overlay while a <select> is focused", async () => {
  wrap(<AppShellWithSelect />);
  const select = await screen.findByRole("combobox");
  select.focus();
  fireEvent.keyDown(select, { key: "?" });
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

test("j/k do not move the highlight while typing in a contenteditable region (WYSIWYG note editor)", () => {
  const rows: Row[] = [{ id: "a", label: "Alpha" }, { id: "b", label: "Bravo" }];
  const onOpen = vi.fn();
  render(
    <div>
      <div contentEditable="true" data-testid="editable-root">
        <p data-testid="editable-child">Some note text the user is composing.</p>
      </div>
      <ListFixture rows={rows} onOpen={onOpen} />
    </div>,
  );
  // Same rationale as the overlay test above: target the nested child, the
  // shape a real ProseMirror document actually has, not the contenteditable
  // root itself.
  const child = screen.getByTestId("editable-child");
  fireEvent.keyDown(child, { key: "j" });
  expect(screen.getByText("Alpha").closest("li")).toHaveAttribute("data-highlighted", "true");
  fireEvent.keyDown(child, { key: "Enter" });
  expect(onOpen).not.toHaveBeenCalled();
});

test("j/k do not move the highlight while a <select> is focused", () => {
  const rows: Row[] = [{ id: "a", label: "Alpha" }, { id: "b", label: "Bravo" }];
  const onOpen = vi.fn();
  render(
    <div>
      <select aria-label="scratch-select">
        <option value="a">A</option>
      </select>
      <ListFixture rows={rows} onOpen={onOpen} />
    </div>,
  );
  const select = screen.getByRole("combobox");
  select.focus();
  fireEvent.keyDown(select, { key: "j" });
  expect(screen.getByText("Alpha").closest("li")).toHaveAttribute("data-highlighted", "true");
  fireEvent.keyDown(select, { key: "Enter" });
  expect(onOpen).not.toHaveBeenCalled();
});

// The listener is window-level and unscoped, so an Enter keydown while
// focus sits on a real <button> (e.g. Home's "Mark all read") would
// otherwise ALSO call onActivate on top of that button's own native click —
// neither <button> nor document.body is a typing target, so isTypingTarget
// alone doesn't catch this.
test("Enter does not double-fire onActivate when focus is on a real button", () => {
  const rows: Row[] = [{ id: "a", label: "Alpha" }];
  const onOpen = vi.fn();
  render(
    <div>
      <button type="button">Mark all read</button>
      <ListFixture rows={rows} onOpen={onOpen} />
    </div>,
  );
  const button = screen.getByRole("button", { name: "Mark all read" });
  button.focus();
  fireEvent.keyDown(button, { key: "Enter" });
  expect(onOpen).not.toHaveBeenCalled();
});

// A Dialog/Sheet sitting on top of the list (⌘K palette, "?" overlay, a
// slide-over) should own j/k/Enter while it's open — verified against the
// REAL Radix Dialog primitive (not a hand-rolled role="dialog" stand-in),
// since that's what actually determines whether the guard's DOM query
// matches in production.
test("j/k/Enter do nothing while a real Dialog is open over the list", () => {
  const rows: Row[] = [{ id: "a", label: "Alpha" }, { id: "b", label: "Bravo" }];
  const onOpen = vi.fn();
  render(
    <div>
      <Dialog open>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Some modal</DialogTitle>
          </DialogHeader>
        </DialogContent>
      </Dialog>
      <ListFixture rows={rows} onOpen={onOpen} />
    </div>,
  );
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  fireEvent.keyDown(document.body, { key: "j" });
  expect(screen.getByText("Alpha").closest("li")).toHaveAttribute("data-highlighted", "true");
  fireEvent.keyDown(document.body, { key: "Enter" });
  expect(onOpen).not.toHaveBeenCalled();
});
