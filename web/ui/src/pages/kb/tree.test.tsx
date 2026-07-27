import { render, screen, waitFor, fireEvent, createEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import FileTree, { NewEntryDialog } from "./FileTree";

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

// FileTree reports move/reorder failures through the shell's toast system, so
// it needs a ToastProvider in scope exactly as it has in the real app.
function renderTree(
  onSelect = vi.fn(),
  onMoved?: (from: string, to: string) => void,
  onImportFiles?: (files: File[], dir?: string) => void,
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <FileTree selectedPath={null} onSelect={onSelect} onMoved={onMoved} onImportFiles={onImportFiles} />
        <ToastHost />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return onSelect;
}

// jsdom gives DragEvent no DataTransfer, and getBoundingClientRect always
// returns zeroes — both are needed to drive the drop-position logic, so they
// are supplied explicitly here.
function dataTransfer() {
  const store = new Map<string, string>();
  return {
    effectAllowed: "",
    dropEffect: "",
    setData: (k: string, v: string) => void store.set(k, v),
    getData: (k: string) => store.get(k) ?? "",
  };
}

// Rows are 20px tall in this harness; `y` picks which band of the row the
// pointer is over (top edge = before, middle = into, bottom edge = after).
function stubRect(el: Element, top = 0, height = 20) {
  vi.spyOn(el, "getBoundingClientRect").mockReturnValue({
    top, height, bottom: top + height, left: 0, right: 100, width: 100, x: 0, y: top, toJSON: () => ({}),
  } as DOMRect);
}

// jsdom has no constructible DragEvent, so testing-library falls back to a
// plain Event for drag types — which silently drops `clientY`, the very thing
// the drop-position logic reads. Build the event and define the coordinate on
// it explicitly. (A missing clientY is now REFUSED by hintFor rather than
// defaulting to a move, so without this every reorder test would just see
// nothing happen.)
function dragEventAt(type: string, el: Element, dt: unknown, clientY: number) {
  const ev = createEvent[type === "drop" ? "drop" : "dragOver"](el, { dataTransfer: dt });
  Object.defineProperty(ev, "clientY", { value: clientY });
  return ev;
}

// The element carrying the drag handlers is the row's draggable ancestor.
// Found by attribute rather than by counting parentElement hops, so a wrapper
// added for layout doesn't silently retarget these to the wrong node.
function rowWrapper(name: string): HTMLElement {
  return screen.getByText(name).closest("[draggable]") as HTMLElement;
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
  expect(onSelect).toHaveBeenCalledWith("notes/a.md", false, "a.md");
});

// Spec §6: "user notes and memory/ first". The backend marks memory/
// system:true (web/api_kb.go's kbSystemDirs — it's DB-reflected/system-
// managed alongside agents/chats/skills), but memory/ is actually the
// user's own editable knowledge and should sort/style WITH user content,
// not muted alongside agents/chats/skills. FileTree overrides the flag by
// name for exactly this one directory.
test("memory/ sorts before muted system dirs and is NOT styled muted, despite the backend marking it system:true", async () => {
  mockFetch((url) => {
    if (url === "/api/v1/kb/tree?path=") {
      return jsonResponse({
        path: "",
        nodes: [
          { name: "chats", display_name: "Chats", path: "chats", is_dir: true, system: true },
          { name: "memory", display_name: "Memory", path: "memory", is_dir: true, system: true },
          { name: "notes", display_name: "Notes", path: "notes", is_dir: true, system: false },
        ],
      });
    }
    return undefined;
  });
  renderTree();

  const memoryRow = (await screen.findByText("Memory")).closest("div");
  const notesRow = screen.getByText("Notes").closest("div");
  const chatsRow = screen.getByText("Chats").closest("div");

  // memory/ is NOT muted — it keeps its Brain icon (unchanged) but drops the
  // system-styling class, same as an ordinary (never-system) user dir.
  expect(memoryRow?.className).not.toMatch(/text-muted-2/);
  expect(notesRow?.className).not.toMatch(/text-muted-2/);
  // chats/ is untouched — still muted, as a real system dir should be.
  expect(chatsRow?.className).toMatch(/text-muted-2/);

  // Root order is the fixed default-folder ranking: memory, agents, chats,
  // skills, then anything else, with notes/ deliberately last.
  const rows = screen.getAllByRole("button").map((el) => el.textContent);
  const memoryIndex = rows.findIndex((t) => t?.includes("Memory"));
  const notesIndex = rows.findIndex((t) => t?.includes("Notes"));
  const chatsIndex = rows.findIndex((t) => t?.includes("Chats"));
  expect(memoryIndex).toBeLessThan(chatsIndex);
  expect(chatsIndex).toBeLessThan(notesIndex);
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
  expect(onSelect).toHaveBeenCalledWith("README.md", false, "README.md");
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

test("Space activates a row the same as Enter (a11y)", async () => {
  mockFetch();
  const onSelect = renderTree();

  await screen.findByText("README.md");
  const row = screen.getByText("README.md").closest('[role="button"]')!;
  (row as HTMLElement).focus();
  fireEvent.keyDown(row, { key: " " });

  expect(onSelect).toHaveBeenCalledWith("README.md", false, "README.md");
});

test("the dropdown trigger is NOT nested inside the row's role=button element", async () => {
  mockFetch();
  renderTree();

  await screen.findByText("README.md");
  const row = screen.getByText("README.md").closest('[role="button"]')!;
  const trigger = screen.getByLabelText("Actions for README.md");

  expect(row.contains(trigger)).toBe(false);
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

// ── Drag to move / reorder ───────────────────────────────────────────────────

// Drops the row named `from` onto the row named `to`, at the vertical band
// given by `band` (0 = top edge, 0.5 = middle, 1 = bottom edge).
async function dragOnto(from: string, to: string, band: number) {
  const src = rowWrapper(from);
  const dst = rowWrapper(to);
  stubRect(dst);
  const dt = dataTransfer();
  fireEvent.dragStart(src, { dataTransfer: dt });
  fireEvent(dst, dragEventAt("dragover", dst, dt, band * 20));
  fireEvent(dst, dragEventAt("drop", dst, dt, band * 20));
  await waitFor(() => {});
}

test("dropping a file in the middle of a folder row moves it into that folder", async () => {
  const calls: Array<{ url: string; method: string; body: unknown }> = [];
  mockFetch((url, init) => {
    calls.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (url === "/api/v1/kb/rename") return jsonResponse({ ok: true });
    return undefined;
  });
  const onMoved = vi.fn();
  renderTree(vi.fn(), onMoved);
  await screen.findByText("README.md");

  await dragOnto("README.md", "Notes", 0.5);

  const rename = calls.find((c) => c.url === "/api/v1/kb/rename");
  expect(rename?.body).toEqual({ from: "README.md", to: "notes/README.md" });
  // The open note has to follow its file, or the editor points at a path that
  // no longer exists and reads as a deletion.
  await waitFor(() => expect(onMoved).toHaveBeenCalledWith("README.md", "notes/README.md"));
});

test("dropping on the edge of a sibling reorders and persists the new order", async () => {
  const calls: Array<{ url: string; method: string; body: unknown }> = [];
  mockFetch((url, init) => {
    calls.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (url === "/api/v1/kb/order") return jsonResponse({ ok: true });
    return undefined;
  });
  renderTree();
  await screen.findByText("README.md");

  // README.md dropped on the TOP edge of the Notes row → before it.
  await dragOnto("README.md", "Notes", 0.05);

  const order = calls.find((c) => c.url === "/api/v1/kb/order");
  expect(order?.method).toBe("PUT");
  // chats/ is a system dir and is deliberately left out of the saved order —
  // see the dedicated regression test below.
  expect(order?.body).toEqual({ dir: "", names: ["README.md", "notes"] });
});

test("dropping a file onto another file does nothing at all", async () => {
  const calls: string[] = [];
  mockFetch((url, init) => {
    if (init?.method && init.method !== "GET") calls.push(url);
    return undefined;
  });
  renderTree();
  await screen.findByText("README.md");

  // Expand notes/ so a second FILE row exists to aim at.
  await userEvent.click(screen.getByText("Notes"));
  await screen.findByText("a.md");

  // a.md is in notes/, README.md is at the root — different parents, so
  // neither a reorder (edge) nor a move-into (it's a file) is legal.
  await dragOnto("README.md", "a.md", 0.5);
  await dragOnto("README.md", "a.md", 0.05);

  expect(calls).toEqual([]);
});

test("a folder can't be dropped into itself", async () => {
  const calls: string[] = [];
  mockFetch((url, init) => {
    if (init?.method && init.method !== "GET") calls.push(url);
    return undefined;
  });
  renderTree();
  await screen.findByText("Notes");

  await dragOnto("Notes", "Notes", 0.5);
  expect(calls).toEqual([]);
});

test("a failed move surfaces the server message as a toast and doesn't move anything", async () => {
  mockFetch((url) => {
    if (url === "/api/v1/kb/rename") {
      return new Response(
        JSON.stringify({ error: { code: "already_exists", message: "“notes/README.md” already exists" } }),
        { status: 409, headers: { "Content-Type": "application/json" } },
      );
    }
    return undefined;
  });
  const onMoved = vi.fn();
  renderTree(vi.fn(), onMoved);
  await screen.findByText("README.md");

  await dragOnto("README.md", "Notes", 0.5);

  // findAll: the toast host renders the message twice (visible + its
  // aria-live announcement).
  expect((await screen.findAllByText(/already exists/)).length).toBeGreaterThan(0);
  expect(onMoved).not.toHaveBeenCalled();
});

test("a stored order wins over the derived sort, and unlisted nodes fall to the end", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/kb/tree?path=") {
        return Promise.resolve(
          jsonResponse({
            path: "",
            // chats is system:true, so the derived sort would put it LAST;
            // the stored order pins it first. z.md is not listed at all and
            // must land after the ordered block, not be silently pinned.
            order: ["chats", "README.md"],
            nodes: [
              { name: "notes", display_name: "Notes", path: "notes", is_dir: true, system: false },
              { name: "README.md", display_name: "README.md", path: "README.md", is_dir: false, system: false },
              { name: "chats", display_name: "Chats", path: "chats", is_dir: true, system: true },
              { name: "z.md", display_name: "z.md", path: "z.md", is_dir: false, system: false },
            ],
          }),
        );
      }
      return Promise.resolve(jsonResponse({ path: url, nodes: [], order: [] }));
    }),
  );
  renderTree();
  await screen.findByText("Chats");

  const labels = screen.getAllByRole("button")
    .filter((el) => el.getAttribute("role") === "button")
    .map((el) => el.textContent?.trim());
  expect(labels).toEqual(["Chats", "README.md", "Notes", "z.md"]);
});

// Regression: the persisted order used to include system dirs. Ranked names
// sort ahead of unranked ones, so reordering root ONCE pinned chats/agents
// above every note created afterwards — using the feature demoted your own new
// notes below the system block.
test("reordering persists only the user's own rows, not system dirs", async () => {
  const calls: Array<{ url: string; body: unknown }> = [];
  mockFetch((url, init) => {
    calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (url === "/api/v1/kb/order") return jsonResponse({ ok: true });
    return undefined;
  });
  renderTree();
  await screen.findByText("README.md");

  await dragOnto("README.md", "Notes", 0.05);

  const order = calls.find((c) => c.url === "/api/v1/kb/order");
  expect(order?.body).toEqual({ dir: "", names: ["README.md", "notes"] });
});

test("a note created after a reorder is not pinned into the saved order", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      const body =
        url === "/api/v1/kb/tree?path="
          ? {
              path: "",
              // What a reorder now saves: user rows only.
              order: ["README.md", "notes"],
              nodes: [
                { name: "notes", display_name: "Notes", path: "notes", is_dir: true, system: false },
                { name: "README.md", display_name: "README.md", path: "README.md", is_dir: false, system: false },
                { name: "chats", display_name: "Chats", path: "chats", is_dir: true, system: true },
                // Created after the drag, so it isn't in `order`.
                { name: "brand-new.md", display_name: "brand-new.md", path: "brand-new.md", is_dir: false, system: false },
              ],
            }
          : { path: url, nodes: [], order: [] };
      return Promise.resolve(jsonResponse(body));
    }),
  );
  renderTree();
  await screen.findByText("Chats");

  // README.md and notes/ are pinned by the saved drag order. brand-new.md was
  // created after that drag, so it is NOT given a slot inside the pinned block
  // — it falls through to the derived rules, where dirs precede files.
  const labels = screen.getAllByRole("button").map((el) => el.textContent?.trim()).filter(Boolean);
  expect(labels).toEqual(["README.md", "Notes", "Chats", "brand-new.md"]);
});

// System dirs are excluded from the saved order, so a reorder aimed at one
// could never be persisted — the affordance must not be offered at all rather
// than appearing to work and doing nothing.
test("a system dir is not a reorder target", async () => {
  const calls: string[] = [];
  mockFetch((url, init) => {
    if (init?.method && init.method !== "GET") calls.push(url);
    return undefined;
  });
  renderTree();
  await screen.findByText("Chats");

  await dragOnto("README.md", "Chats", 0.05);
  await dragOnto("README.md", "Chats", 0.95);
  expect(calls).toEqual([]);
});

// Regression: a row's dragover/drop handlers used to stopPropagation
// unconditionally, for ANY drag — including a native OS file drag, where
// DragCtx's `dragged` is null for the whole gesture (it's only ever set by a
// row's own onDragStart, driving the tree's internal reorder/move). That
// meant a file dropped directly ON a row — the vast majority of the tree's
// surface — never reached the root wrapper's onImportFiles handler; only a
// drop in the blank gap below the last row worked.
//
// Dropping on a FOLDER row must import it — and, per the "drop onto a folder
// files it there" fix, must carry that folder as the destination, not the
// caller's default. "Notes" is a folder here, so the expected dir is "notes".
test("dropping an OS file onto a folder row imports it into that folder", async () => {
  mockFetch();
  const onImportFiles = vi.fn();
  renderTree(vi.fn(), undefined, onImportFiles);

  const row = await screen.findByText("Notes");
  const file = new File(["a,b\n1,2\n"], "sample.csv", { type: "text/csv" });
  // jsdom has no constructible DataTransfer with real file-drag semantics; a
  // plain object with `types`/`files` is enough to drive both the root
  // wrapper's `types.includes("Files")` check and the row's own guard
  // (DragCtx's `dragged`, which stays null here since nothing fired an
  // internal onDragStart).
  const dt = { types: ["Files"], files: [file], dropEffect: "" };

  fireEvent.dragOver(row, { dataTransfer: dt });
  fireEvent.drop(row, { dataTransfer: dt });

  expect(onImportFiles).toHaveBeenCalledTimes(1);
  expect(onImportFiles).toHaveBeenCalledWith([file], "notes");
});

// A drop onto a FILE row can't mean "into" that file — there's nothing
// sensible that could mean — so it targets the file's own parent folder
// instead. README.md is at the vault root, so its parent is "" (root);
// exercise a nested file so the parent is a non-trivial, non-empty folder.
test("dropping an OS file onto a file row imports it into that file's parent folder", async () => {
  mockFetch();
  const onImportFiles = vi.fn();
  renderTree(vi.fn(), undefined, onImportFiles);

  await userEvent.click(await screen.findByText("Notes"));
  const row = await screen.findByText("a.md");
  const file = new File(["x,y\n1,2\n"], "sample.csv", { type: "text/csv" });
  const dt = { types: ["Files"], files: [file], dropEffect: "" };

  fireEvent.dragOver(row, { dataTransfer: dt });
  fireEvent.drop(row, { dataTransfer: dt });

  expect(onImportFiles).toHaveBeenCalledTimes(1);
  expect(onImportFiles).toHaveBeenCalledWith([file], "notes");
});

// Dropping in the blank gap below the last row never lands on any row's own
// handler, so it still bubbles to FileTree's root wrapper — which calls
// onImportFiles with no dir at all, keeping today's default (notes/).
test("dropping an OS file on the blank gap (not any row) omits a dir", async () => {
  mockFetch();
  const onImportFiles = vi.fn();
  const { container } = render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastProvider>
        <FileTree selectedPath={null} onSelect={vi.fn()} onImportFiles={onImportFiles} />
        <ToastHost />
      </ToastProvider>
    </QueryClientProvider>,
  );
  await screen.findByText("README.md");

  const gap = container.firstElementChild as HTMLElement; // FileTree's own root wrapper div
  const file = new File(["a,b\n1,2\n"], "sample.csv", { type: "text/csv" });
  const dt = { types: ["Files"], files: [file], dropEffect: "" };

  fireEvent.dragOver(gap, { dataTransfer: dt });
  fireEvent.drop(gap, { dataTransfer: dt });

  expect(onImportFiles).toHaveBeenCalledTimes(1);
  expect(onImportFiles).toHaveBeenCalledWith([file]);
});

// The tree's OWN internal reorder/move drag must still be untouched by the
// file-import wrapper — it carries no "Files" data-transfer type, so
// onImportFiles must never fire for it even when provided.
test("an internal reorder drag never triggers onImportFiles", async () => {
  mockFetch();
  const onImportFiles = vi.fn();
  renderTree(vi.fn(), undefined, onImportFiles);

  await screen.findByText("Chats");
  await dragOnto("README.md", "Notes", 0.5); // "into" Notes — an internal move

  expect(onImportFiles).not.toHaveBeenCalled();
});

// The reset effect used to be keyed on [open, dirPath], and dirPath is derived
// from the open note's path — which changes underneath an already-open dialog.
// The user's typed name was cleared mid-typing and Create then silently no-oped
// on `if (!n) return`.
test("NewEntryDialog keeps the typed name when dirPath changes while it is open", async () => {
  mockFetch();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { rerender } = render(
    <QueryClientProvider client={qc}>
      <NewEntryDialog dirPath="" kind="note" open onOpenChange={() => {}} pickLocation />
    </QueryClientProvider>,
  );
  await userEvent.type(await screen.findByLabelText("Name"), "ideas");

  rerender(
    <QueryClientProvider client={qc}>
      <NewEntryDialog dirPath="notes" kind="note" open onOpenChange={() => {}} pickLocation />
    </QueryClientProvider>,
  );

  expect(screen.getByLabelText("Name")).toHaveValue("ideas");
});
