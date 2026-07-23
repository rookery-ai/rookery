import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import NoteHeader from "./NoteHeader";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

function renderHeader(formats: { html: boolean; docx: boolean; pdf: boolean }) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/kb/export/formats")) return Promise.resolve(jsonResponse(formats));
      return Promise.resolve(jsonResponse({}));
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <NoteHeader
          path="notes/trip.md"
          state="saved"
          backlinksCount={0}
          onRename={vi.fn()}
          onDelete={vi.fn()}
          rawMode={false}
          onToggleRaw={vi.fn()}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

test("export menu offers HTML/Word/Markdown and links to the export endpoint", async () => {
  renderHeader({ html: true, docx: true, pdf: true });
  await userEvent.click(screen.getByRole("button", { name: /export/i }));
  const html = await screen.findByRole("menuitem", { name: "HTML" });
  expect(html).toHaveAttribute("href", expect.stringContaining("format=html"));
  expect(screen.getByRole("menuitem", { name: /Word/ })).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: /PDF/ })).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: /Markdown/ })).toBeInTheDocument();
});

test("PDF export item is disabled when the host reports no engine", async () => {
  renderHeader({ html: true, docx: true, pdf: false });
  await userEvent.click(screen.getByRole("button", { name: /export/i }));
  const pdf = await screen.findByRole("menuitem", { name: /PDF \(unavailable\)/i });
  expect(pdf).toHaveAttribute("aria-disabled", "true");
});
