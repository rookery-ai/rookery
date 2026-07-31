import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell } from "@/components/shell/AppShell";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import SearchBox from "./SearchBox";
import KBPage from "./KBPage";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

function renderSearch(onSelect = vi.fn()) {
  const qc = new QueryClient();
  render(
    <QueryClientProvider client={qc}>
      <SearchBox onSelect={onSelect}>
        <div>tree placeholder</div>
      </SearchBox>
    </QueryClientProvider>,
  );
  return onSelect;
}

test("does not search below 2 characters, and debounces before firing at 2+", () => {
  vi.useFakeTimers();
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      calls.push(String(input));
      return Promise.resolve(jsonResponse({ hits: [] }));
    }),
  );
  renderSearch();

  const input = screen.getByPlaceholderText(/search/i);
  fireEvent.change(input, { target: { value: "a" } });
  act(() => {
    vi.advanceTimersByTime(500);
  });
  expect(calls).toHaveLength(0);

  fireEvent.change(input, { target: { value: "ab" } });
  // Not yet — debounce window hasn't elapsed.
  act(() => {
    vi.advanceTimersByTime(100);
  });
  expect(calls).toHaveLength(0);

  act(() => {
    vi.advanceTimersByTime(250);
  });
  expect(calls).toHaveLength(1);
  expect(calls[0]).toBe("/api/v1/kb/search?q=ab");
  vi.useRealTimers();
});

test("renders hits with the match highlighted, and clicking a hit selects its path", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          hits: [{ path: "notes/trip.md", line: 4, snippet: "pack sunscreen for the Trip" }],
        }),
      ),
    ),
  );
  const user = userEvent.setup();
  const onSelect = renderSearch();

  const input = screen.getByPlaceholderText(/search/i);
  await user.type(input, "trip");

  const hit = await screen.findByText("notes/trip.md");
  const mark = await screen.findByText("Trip", { selector: "mark" });
  expect(mark).toBeInTheDocument();

  await user.click(hit);
  expect(onSelect).toHaveBeenCalledWith("notes/trip.md");
});

test("shows a no-results empty state", async () => {
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(jsonResponse({ hits: [] }))));
  const user = userEvent.setup();
  renderSearch();

  await user.type(screen.getByPlaceholderText(/search/i), "zz");
  expect(await screen.findByText(/no results/i)).toBeInTheDocument();
});

test("Escape clears the query and restores the tree (search results disappear)", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(jsonResponse({ hits: [{ path: "notes/trip.md", line: 1, snippet: "trip" }] }))),
  );
  const user = userEvent.setup();
  renderSearch();

  const input = screen.getByPlaceholderText(/search/i) as HTMLInputElement;
  await user.type(input, "trip");
  await screen.findByText("notes/trip.md");

  await user.keyboard("{Escape}");
  expect(input.value).toBe("");
  await waitFor(() => expect(screen.queryByText("notes/trip.md")).not.toBeInTheDocument());
});

// ── Integration: does clicking a hit actually OPEN the note? ────────────────
//
// SearchBox's own click test above proves onSelect fires. That leaves the
// integration — KBPage's onSelect={(p) => openPath(p, false)} — as the only
// place a "results are not clickable" report can come from, so it is worth a
// test of its own rather than trusting that the two halves compose.

test("clicking a search hit navigates KBPage to that note", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/auth/session") {
        return Promise.resolve(
          jsonResponse({
            authenticated: true,
            owner: { id: "o1", username: "admin", must_change_password: false },
            workspace: { id: "w1", name: "ws1", about: "", needs_setup: false, created_at: "2026-01-01T00:00:00Z" },
            workspaces: [],
          }),
        );
      }
      if (url.startsWith("/api/v1/kb/search")) {
        return Promise.resolve(
          jsonResponse({
            hits: [{ path: "notes/dentist.md", title: "Dentist", line: 3, snippet: "call the dentist" }],
          }),
        );
      }
      if (url.startsWith("/api/v1/kb/tree")) {
        return Promise.resolve(jsonResponse({ path: "", nodes: [], order: [] }));
      }
      if (url.startsWith("/api/v1/kb/note")) {
        return Promise.resolve(
          jsonResponse({ path: "notes/dentist.md", kind: "markdown", content: "# Dentist\n", frontmatter: {} }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MemoryRouter initialEntries={["/kb"]}>
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/kb" element={<KBPage />} />
            </Route>
          </Routes>
          <ToastHost />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );

  const input = await screen.findByPlaceholderText(/search notes/i);
  await userEvent.type(input, "dentist");

  const hit = await screen.findByRole("button", { name: /Dentist/i });
  await userEvent.click(hit);

  // The note must actually open — the whole point of clicking a result.
  await waitFor(() => {
    expect(screen.getByRole("textbox", { name: /note/i })).toBeInTheDocument();
  });
});

test("a search result presents itself as clickable", async () => {
  // ROOT CAUSE of "search finds the pages but they are not clickable".
  //
  // A <button> does NOT get cursor:pointer from the browser, and this build's
  // Tailwind preflight does not add one either — verified by grepping the
  // emitted CSS, which contained only two cursor:pointer rules, neither of them
  // a button rule. FileTree's rows opt in explicitly (cursor-pointer), so in
  // the SAME pane the tree felt clickable and the results did not: a plain
  // arrow cursor over three lines of identical grey text reads as inert
  // output, not as a row you can activate.
  //
  // The click always worked. The affordance never did.
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          hits: [{ path: "notes/a.md", title: "Note A", line: 1, snippet: "hello world" }],
        }),
      ),
    ),
  );
  renderSearch();

  await userEvent.type(screen.getByPlaceholderText(/search/i), "hello");
  const hit = await screen.findByRole("button", { name: /Note A/ });

  expect(hit.className).toMatch(/cursor-pointer/);
});
