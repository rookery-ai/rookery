import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import NoteEditor from "./NoteEditor";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const PATH = "notes/ci.md";

// `served` is what GET /kb/note currently returns — a test flips it to
// simulate the chat coder rewriting the file on disk, exactly as
// ChatWindow.sendTurn's ["kb-note"] invalidation would then surface.
let served = "";
let puts: string[] = [];

function mockFetch() {
  served = "The pipeline runs on merge.";
  puts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.startsWith("/api/v1/kb/note") && method === "GET") {
        return Promise.resolve(
          jsonResponse({ path: PATH, content: served, html: "", backlinks: [] }),
        );
      }
      if (url.startsWith("/api/v1/kb/note") && method === "PUT") {
        const body = JSON.parse(String(init?.body));
        puts.push(body.content);
        // The server keeps what it was sent; a later GET must therefore return
        // it, or the editor would read its own save as an external change.
        served = body.content;
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function renderEditor(qc: QueryClient) {
  return render(
    <MemoryRouter initialEntries={[`/?path=${encodeURIComponent(PATH)}`]}>
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <TooltipProvider>
            <NoteEditor path={PATH} />
            {/* ToastProvider only supplies the context; AppShell mounts the
                host in the real app. Without it the toast is dispatched and
                rendered nowhere. */}
            <ToastHost />
          </TooltipProvider>
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

async function enterRaw(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "Raw" }));
  return (await screen.findByRole("textbox", {
    name: "Raw markdown",
  })) as HTMLTextAreaElement;
}

// Mounting the rich-text editor normalises the document, which TipTap reports
// as an update — so a freshly-opened note briefly reads as dirty and autosaves
// once. Every test here turns on the clean/dirty distinction, so each waits for
// that to settle before simulating anything.
async function settle() {
  await waitFor(() => expect(screen.queryByText("Unsaved")).not.toBeInTheDocument());
  await waitFor(() => expect(screen.queryByText("Saving…")).not.toBeInTheDocument());
}

beforeEach(mockFetch);
afterEach(() => vi.unstubAllGlobals());

// The reported bug: "Edit with AI" rewrites the note on disk and the open
// editor keeps showing the old text until the page is reloaded.
test("a clean editor adopts a change made on disk, with no prompt", async () => {
  const qc = newClient();
  renderEditor(qc);
  const user = userEvent.setup();
  const box = await enterRaw(user);
  expect(box.value).toContain("The pipeline runs on merge.");
  await settle();

  served = "The pipeline runs on every merge to main.";
  await qc.invalidateQueries({ queryKey: ["kb-note"] });

  await waitFor(() =>
    expect(
      (screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement).value,
    ).toContain("every merge to main"),
  );
  expect(screen.queryAllByText(/changed by chat/)).toHaveLength(0);
});

// This file has a recorded data-loss history around dirtyRef. An
// unconditional swap would throw away work the user has not saved to apply a
// change they may not have asked for yet.
test("a dirty editor is not overwritten — it offers Reload instead", async () => {
  const qc = newClient();
  renderEditor(qc);
  const user = userEvent.setup();
  const box = await enterRaw(user);
  await settle();
  await user.type(box, "\n\nMy own unsaved sentence.");

  served = "Rewritten by the chat coder.";
  await qc.invalidateQueries({ queryKey: ["kb-note"] });

  expect((await screen.findAllByText(/changed by chat/)).length).toBeGreaterThan(0);
  const live = screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement;
  expect(live.value).toContain("My own unsaved sentence.");
  expect(live.value).not.toContain("Rewritten by the chat coder.");

  await user.click(screen.getByRole("button", { name: "Reload" }));
  await waitFor(() =>
    expect(
      (screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement).value,
    ).toContain("Rewritten by the chat coder."),
  );
});

// The regression a naive implementation ships. useSaveNote invalidates
// ["kb-note", path] on success, so EVERY autosave already causes a refetch —
// comparing against anything other than the bytes we last synced would fire on
// every keystroke pause, and would toast the user about their own typing.
test("the editor's own save round-trip is not mistaken for an external change", async () => {
  const qc = newClient();
  renderEditor(qc);
  const user = userEvent.setup();
  const box = await enterRaw(user);
  await settle();
  puts = [];
  await user.type(box, "\n\nA sentence I typed.");

  // Ctrl+S flushes immediately rather than waiting out the debounce.
  await user.keyboard("{Control>}s{/Control}");
  await waitFor(() => expect(puts.length).toBeGreaterThan(0));
  await qc.invalidateQueries({ queryKey: ["kb-note"] });

  await waitFor(() =>
    expect(
      (screen.getByRole("textbox", { name: "Raw markdown" }) as HTMLTextAreaElement).value,
    ).toContain("A sentence I typed."),
  );
  expect(screen.queryAllByText(/changed by chat/)).toHaveLength(0);
});
