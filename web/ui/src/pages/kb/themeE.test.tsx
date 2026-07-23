import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useSearchParams } from "react-router";
import { ToastProvider } from "@/components/shell/Toast";
import NoteEditor from "./NoteEditor";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

function PathBoundEditor() {
  const [params] = useSearchParams();
  const path = params.get("path");
  return path ? <NoteEditor path={path} key={path} /> : null;
}

function mockNote(path: string, backlinks: string[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(jsonResponse({ path, content: "# Note\n\ntext", html: "", backlinks, kind: "markdown" }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[`/?path=${encodeURIComponent(path)}`]}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ToastProvider>
          <PathBoundEditor />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

test("a user note shows the Linked-from strip", async () => {
  mockNote("notes/target.md", ["notes/author.md"]);
  renderAt("notes/target.md");
  expect(await screen.findByText(/linked from/i)).toBeInTheDocument();
});

test("an agent run-log note hides the Linked-from strip", async () => {
  mockNote("agents/abc/logs/run_1.md", ["notes/author.md"]);
  renderAt("agents/abc/logs/run_1.md");
  // Wait for the editor to load, then assert the strip is absent.
  await screen.findByLabelText("Note title");
  expect(screen.queryByText(/linked from/i)).not.toBeInTheDocument();
});

test("an inbox note hides the Linked-from strip", async () => {
  mockNote("inbox/msg1.md", ["notes/author.md"]);
  renderAt("inbox/msg1.md");
  await screen.findByLabelText("Note title");
  expect(screen.queryByText(/linked from/i)).not.toBeInTheDocument();
});
