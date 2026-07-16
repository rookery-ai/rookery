import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import FileTree from "./FileTree";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

// `intercept` lets a test short-circuit specific requests (e.g. a DELETE
// that should error) while every other call still hits the tree fixtures.
function mockFetch(intercept?: (url: string, init?: RequestInit) => Response | undefined) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const custom = intercept?.(url, init);
      if (custom) return Promise.resolve(custom);
      if (url === "/api/v1/kb/tree?path=") {
        return Promise.resolve(
          jsonResponse({
            path: "",
            nodes: [
              { name: "notes", display_name: "Notes", path: "notes", is_dir: true, system: false },
              { name: "README.md", display_name: "README.md", path: "README.md", is_dir: false, system: false },
              { name: "chats", display_name: "Chats", path: "chats", is_dir: true, system: true },
            ],
          }),
        );
      }
      if (url === "/api/v1/kb/tree?path=notes") {
        return Promise.resolve(
          jsonResponse({
            path: "notes",
            nodes: [
              { name: "a.md", display_name: "a.md", path: "notes/a.md", is_dir: false, system: false },
            ],
          }),
        );
      }
      return Promise.resolve(jsonResponse({ path: url, nodes: [] }));
    }),
  );
}

function renderTree(onSelect = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <FileTree selectedPath={null} onSelect={onSelect} />
    </QueryClientProvider>,
  );
  return onSelect;
}

test("renders root nodes with muted system rows, lazy-loads a directory, and selects a file", async () => {
  mockFetch();
  const onSelect = renderTree();

  expect(await screen.findByText("README.md")).toBeInTheDocument();

  const chatsRow = screen.getByText("Chats").closest("div");
  expect(chatsRow?.className).toMatch(/text-muted-2/);

  // notes/a.md hasn't been fetched yet (lazy per-dir loading)
  expect(screen.queryByText("a.md")).not.toBeInTheDocument();

  await userEvent.click(screen.getByText("Notes"));
  expect(await screen.findByText("a.md")).toBeInTheDocument();
  expect(vi.mocked(fetch).mock.calls.some((c) => String(c[0]) === "/api/v1/kb/tree?path=notes")).toBe(
    true,
  );

  await userEvent.click(screen.getByText("a.md"));
  expect(onSelect).toHaveBeenCalledWith("notes/a.md", false);
});

test("row dropdown opens a dialog (rename) and the tree stays interactive after closing it", async () => {
  mockFetch();
  const onSelect = renderTree();

  await screen.findByText("README.md");
  await userEvent.click(screen.getByLabelText("Actions for README.md"));

  const renameItem = await screen.findByText("Rename…");
  // Files don't get New note/New folder actions.
  expect(screen.queryByText("New note…")).not.toBeInTheDocument();
  await userEvent.click(renameItem);

  expect(await screen.findByRole("heading", { name: "Rename" })).toBeInTheDocument();
  expect(screen.getByLabelText("Path")).toHaveValue("README.md");

  await userEvent.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByRole("heading", { name: "Rename" })).not.toBeInTheDocument(),
  );

  // The row is still clickable — body isn't stuck with pointer-events:none.
  await userEvent.click(screen.getByText("README.md"));
  expect(onSelect).toHaveBeenCalledWith("README.md", false);
});

test("New note… on a directory opens the dialog and creates the entry", async () => {
  mockFetch();
  renderTree();

  await userEvent.click(await screen.findByLabelText("Actions for Notes"));
  await userEvent.click(await screen.findByText("New note…"));

  expect(await screen.findByText("New note")).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText("Name"), "b");
  await userEvent.click(screen.getByRole("button", { name: "Create" }));

  await waitFor(() =>
    expect(
      vi.mocked(fetch).mock.calls.some((c) => String(c[0]) === "/api/v1/kb/new"),
    ).toBe(true),
  );
  const call = vi.mocked(fetch).mock.calls.find((c) => String(c[0]) === "/api/v1/kb/new")!;
  expect(JSON.parse(String((call[1] as RequestInit).body))).toEqual({ path: "notes/b.md", is_dir: false });
});

test("Delete… on a file shows the path, confirms, DELETEs, and closes on success", async () => {
  mockFetch();
  renderTree();

  await userEvent.click(await screen.findByLabelText("Actions for README.md"));
  await userEvent.click(await screen.findByText("Delete…"));

  const heading = await screen.findByRole("heading", { name: /^Delete\s/ });
  expect(heading.textContent).toContain("README.md");

  await userEvent.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() =>
    expect(
      vi.mocked(fetch).mock.calls.some(
        (c) =>
          String(c[0]) === "/api/v1/kb/note?path=README.md" &&
          (c[1] as RequestInit | undefined)?.method === "DELETE",
      ),
    ).toBe(true),
  );
  await waitFor(() =>
    expect(screen.queryByRole("heading", { name: /^Delete\s/ })).not.toBeInTheDocument(),
  );
});

test("Delete… surfaces a 400 error inline and keeps the dialog open", async () => {
  mockFetch((url, init) => {
    if (init?.method === "DELETE" && url.startsWith("/api/v1/kb/note")) {
      return new Response(
        JSON.stringify({ error: { code: "invalid_path", message: "cannot delete this" } }),
        { status: 400, headers: { "Content-Type": "application/json" } },
      );
    }
    return undefined;
  });
  renderTree();

  await userEvent.click(await screen.findByLabelText("Actions for README.md"));
  await userEvent.click(await screen.findByText("Delete…"));
  await screen.findByRole("heading", { name: /^Delete\s/ });

  await userEvent.click(screen.getByRole("button", { name: "Delete" }));

  expect(await screen.findByText("cannot delete this")).toBeInTheDocument();
  // Error keeps the dialog open — the confirm didn't silently succeed.
  expect(screen.getByRole("heading", { name: /^Delete\s/ })).toBeInTheDocument();
});
