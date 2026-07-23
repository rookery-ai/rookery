import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import FileTree from "./FileTree";
import { filterEmojis } from "./emojiData";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

// A flat root with three user files so range/toggle selection has something to
// act on. Icons carried on one node to exercise emoji rendering.
function mockFetch(record?: (url: string, init?: RequestInit) => void) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      record?.(url, init);
      if (url === "/api/v1/kb/tree?path=") {
        return Promise.resolve(
          jsonResponse({
            path: "",
            nodes: [
              { name: "a.md", display_name: "a.md", path: "notes/a.md", is_dir: false, system: false },
              { name: "b.md", display_name: "b.md", path: "notes/b.md", is_dir: false, system: false, icon: "⭐" },
              { name: "c.md", display_name: "c.md", path: "notes/c.md", is_dir: false, system: false },
            ],
          }),
        );
      }
      return Promise.resolve(jsonResponse({ path: url, nodes: [] }));
    }),
  );
}

function renderTree() {
  const onSelect = vi.fn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <FileTree selectedPath={null} onSelect={onSelect} />
        <ToastHost />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return onSelect;
}

test("filterEmojis matches on keywords and dedupes", () => {
  expect(filterEmojis("rocket").some((e) => e.emoji === "🚀")).toBe(true);
  expect(filterEmojis("folder").length).toBeGreaterThan(0);
  expect(filterEmojis("")).toEqual([]);
  // A keyword shared by multiple entries returns unique glyphs only.
  const stars = filterEmojis("star");
  expect(new Set(stars.map((e) => e.emoji)).size).toBe(stars.length);
});

test("a custom emoji renders in place of the default file icon", async () => {
  mockFetch();
  renderTree();
  // b.md carries icon ⭐.
  expect(await screen.findByText("⭐")).toBeInTheDocument();
});

test("ctrl-click toggles multi-selection and reveals the action bar at 2 items", async () => {
  mockFetch();
  const onSelect = renderTree();
  const a = await screen.findByText("a.md");
  const c = await screen.findByText("c.md");

  // Ctrl-click two rows — neither should open (onSelect not called).
  fireEvent.click(a, { ctrlKey: true });
  fireEvent.click(c, { ctrlKey: true });

  expect(await screen.findByText("2 selected")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /move/i })).toBeInTheDocument();
  expect(onSelect).not.toHaveBeenCalled();
});

test("shift-click selects a contiguous range over visible rows", async () => {
  mockFetch();
  renderTree();
  const a = await screen.findByText("a.md");
  const c = await screen.findByText("c.md");

  // Plain click a (sets anchor + opens), then shift-click c → range a..c = 3.
  fireEvent.click(a);
  fireEvent.click(c, { shiftKey: true });

  expect(await screen.findByText("3 selected")).toBeInTheDocument();
});

test("clearing the selection hides the action bar", async () => {
  mockFetch();
  renderTree();
  const a = await screen.findByText("a.md");
  const b = await screen.findByText("b.md");
  fireEvent.click(a, { ctrlKey: true });
  fireEvent.click(b, { ctrlKey: true });

  await screen.findByText("2 selected");
  await userEvent.click(screen.getByRole("button", { name: /clear selection/i }));
  await waitFor(() => expect(screen.queryByText("2 selected")).not.toBeInTheDocument());
});

test("Change icon… sends a PUT to /kb/icon with the chosen emoji", async () => {
  const calls: { url: string; body: string }[] = [];
  mockFetch((url, init) => {
    if (url === "/api/v1/kb/icon") calls.push({ url, body: String(init?.body ?? "") });
  });
  renderTree();

  await screen.findByText("a.md");
  // Open the row's ⋯ menu, choose Change icon…, pick an emoji.
  await userEvent.click(screen.getByLabelText(/actions for a\.md/i));
  await userEvent.click(await screen.findByText("Change icon…"));
  await userEvent.click(await screen.findByLabelText("Set icon 🚀"));

  await waitFor(() => expect(calls.length).toBe(1));
  expect(calls[0].body).toContain("notes/a.md");
  expect(calls[0].body).toContain("🚀");
});
