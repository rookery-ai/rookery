import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import FileViewer from "./FileViewer";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderViewer(path: string) {
  const qc = new QueryClient();
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <FileViewer path={path} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

test("a code file renders its content in a read-only <pre>, with no save affordance", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "agents/demo/tools/script.py",
            content: "print('hello')\n",
            html: "",
            backlinks: [],
            kind: "code",
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderViewer("agents/demo/tools/script.py");

  const pre = await screen.findByText((_, el) => el?.tagName === "PRE" && el.textContent === "print('hello')\n");
  expect(pre).toBeInTheDocument();

  // No save affordance at all — no button/textarea that implies editing.
  expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

test("a binary file shows a Binary file panel with a Download link to rawURL(path), never the raw bytes", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "agents/demo/data.bin",
            content: "",
            html: "",
            backlinks: [],
            kind: "binary",
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  renderViewer("agents/demo/data.bin");

  expect(await screen.findByText(/binary file/i)).toBeInTheDocument();
  const link = screen.getByRole("link", { name: /download/i });
  expect(link).toHaveAttribute("href", "/api/v1/kb/raw?path=agents%2Fdemo%2Fdata.bin");
});

test("breadcrumb and Download are present for a code file, and Delete removes it", async () => {
  const deleteCalls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "DELETE") {
        deleteCalls.push(url);
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({
            path: "agents/demo/tools/script.py",
            content: "print('hi')\n",
            html: "",
            backlinks: [],
            kind: "code",
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const user = userEvent.setup();
  renderViewer("agents/demo/tools/script.py");

  await screen.findByText("script.py");
  expect(screen.getByText("agents")).toBeInTheDocument();
  expect(screen.getByText("demo")).toBeInTheDocument();
  expect(screen.getByText("tools")).toBeInTheDocument();

  const downloadLink = screen.getByRole("link", { name: /download/i });
  expect(downloadLink).toHaveAttribute("href", "/api/v1/kb/raw?path=agents%2Fdemo%2Ftools%2Fscript.py");

  await user.click(screen.getByLabelText(/file actions/i));
  await user.click(await screen.findByText("Delete…"));
  await user.click(screen.getByRole("button", { name: "Delete" }));

  await waitFor(() => expect(deleteCalls).toHaveLength(1));
  expect(deleteCalls[0]).toBe("/api/v1/kb/note?path=agents%2Fdemo%2Ftools%2Fscript.py");
});
