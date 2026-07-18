import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SearchBox from "./SearchBox";

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
