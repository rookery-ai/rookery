import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { useRenameNote } from "@/lib/kb";
import NoteHeader from "./NoteHeader";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderHeader(overrides: Partial<React.ComponentProps<typeof NoteHeader>> = {}) {
  const qc = new QueryClient();
  const props: React.ComponentProps<typeof NoteHeader> = {
    path: "notes/trip plan.md",
    state: "saved",
    backlinksCount: 0,
    onRename: vi.fn(),
    onDelete: vi.fn(),
    rawMode: false,
    onToggleRaw: vi.fn(),
    ...overrides,
  };
  render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <NoteHeader {...props} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return props;
}

// Mirrors the real wiring (NoteEditor calls useRenameNote().mutate({from, to})
// inside the onRename callback it hands to NoteHeader) — proves the {to}
// value NoteHeader computes is exactly what the mutation needs.
function RenameHarness({ path }: { path: string }) {
  const renameNote = useRenameNote();
  return (
    <NoteHeader
      path={path}
      state="saved"
      backlinksCount={0}
      onRename={(to) => renameNote.mutate({ from: path, to })}
      onDelete={vi.fn()}
      rawMode={false}
      onToggleRaw={vi.fn()}
    />
  );
}

test("breadcrumb shows the ancestor dir and the title input value is the filename minus .md", () => {
  renderHeader();
  expect(screen.getByText("notes")).toBeInTheDocument();
  expect(screen.getByDisplayValue("trip plan")).toBeInTheDocument();
});

test("typing a new title + Enter fires the rename mutation with {from, to}", async () => {
  const putCalls: Array<{ url: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url === "/api/v1/kb/rename") {
        putCalls.push({ url, body: JSON.parse(String(init.body)) });
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const qc = new QueryClient();
  const user = userEvent.setup();
  render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <RenameHarness path="notes/trip plan.md" />
      </QueryClientProvider>
    </MemoryRouter>,
  );

  const input = screen.getByDisplayValue("trip plan");
  await user.clear(input);
  await user.type(input, "summer");
  await user.keyboard("{Enter}");

  await waitFor(() => expect(putCalls).toHaveLength(1));
  expect(putCalls[0]).toEqual({
    url: "/api/v1/kb/rename",
    body: { from: "notes/trip plan.md", to: "notes/summer.md" },
  });
});

test("Escape reverts the title without firing a rename", async () => {
  const onRename = vi.fn();
  const user = userEvent.setup();
  renderHeader({ onRename });

  const input = screen.getByDisplayValue("trip plan") as HTMLInputElement;
  await user.clear(input);
  await user.type(input, "discarded");
  await user.keyboard("{Escape}");

  expect(input.value).toBe("trip plan");
  expect(onRename).not.toHaveBeenCalled();
});

test("blur with an unchanged value is a no-op", async () => {
  const onRename = vi.fn();
  const user = userEvent.setup();
  renderHeader({ onRename });

  const input = screen.getByDisplayValue("trip plan");
  await user.click(input);
  await user.tab();

  expect(onRename).not.toHaveBeenCalled();
});

test.each([
  ["saved", "Saved ✓"],
  ["saving", "Saving…"],
  ["dirty", "Unsaved"],
  ["error", "Save failed"],
  ["raw", "Raw mode"],
] as const)("save-state chip maps %s -> %s", (state, label) => {
  renderHeader({ state });
  expect(screen.getByText(label)).toBeInTheDocument();
});

test("backlinks count is hidden when zero and shown when present", () => {
  const { unmount } = (() => {
    renderHeader({ backlinksCount: 0 });
    return { unmount: () => {} };
  })();
  expect(screen.queryByText(/backlink/)).not.toBeInTheDocument();
  unmount();

  renderHeader({ backlinksCount: 3 });
  expect(screen.getByText(/3 backlinks/)).toBeInTheDocument();
});

test("raw toggle button calls onToggleRaw", async () => {
  const onToggleRaw = vi.fn();
  const user = userEvent.setup();
  renderHeader({ onToggleRaw, rawMode: false });

  await user.click(screen.getByRole("button", { name: /raw/i }));
  expect(onToggleRaw).toHaveBeenCalled();
});

test("delete menu item opens a confirm dialog and calls onDelete on confirm", async () => {
  const onDelete = vi.fn();
  const user = userEvent.setup();
  renderHeader({ onDelete, path: "notes/trip plan.md" });

  await user.click(screen.getByLabelText(/note actions/i));
  await user.click(await screen.findByText("Delete…"));

  const heading = await screen.findByRole("heading", { name: /^Delete\s/ });
  expect(heading.textContent).toContain("trip plan.md");

  await user.click(screen.getByRole("button", { name: "Delete" }));
  expect(onDelete).toHaveBeenCalled();
});
