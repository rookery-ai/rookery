import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import AgentEditPage from "./AgentEditPage";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// Minimal stub — the edit page never drives an SSE stream in these tests, it
// just must not blow up if DesignerSurface constructs one.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener() {}
  close() {}
}

let posts: string[];

function mockFetch() {
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "POST") posts.push(url);
      if (url === "/api/v1/agents" && method === "GET") {
        return Promise.resolve(jsonResponse({ agents: [], draft: null }));
      }
      if (url === "/api/v1/agents/a1" && method === "GET") {
        return Promise.resolve(jsonResponse({ agent: { id: "a1", name: "Inbox Triager" } }));
      }
      if (url === "/api/v1/agents/design/state") return Promise.resolve(jsonResponse({ active: false }));
      if (url === "/api/v1/agents/a1/edit/start") {
        return Promise.resolve(
          jsonResponse({ response: "The schedule row says hourly.", done: false, state: "designing" }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/agents/a1/edit"]}>
        <Routes>
          <Route path="/agents/:id/edit" element={<AgentEditPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  mockFetch();
});

afterEach(() => vi.unstubAllGlobals());

// The bug: the edit page used to open in its own full-width chrome and swap to
// the 10%-gutter DesignerSurface only after the first reply landed. One surface
// now owns it from the first paint — the designer's stepper is the tell, since
// the deleted pre-screen had no stepper at all.
test("the edit chat opens in the designer chrome, with no pre-screen", async () => {
  wrap();
  expect(await screen.findByText("Diagnose")).toBeInTheDocument();
  expect(screen.queryByPlaceholderText("Describe the change…")).not.toBeInTheDocument();
});

// The first message used to vanish into a disabled composer for the length of a
// whole coder round-trip. It must appear as a bubble immediately.
test("the first message is echoed as a bubble and routed to edit/start", async () => {
  wrap();
  await screen.findByText("Diagnose");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "run it once a day");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByText("run it once a day")).toBeInTheDocument();
  expect(await screen.findByText("The schedule row says hourly.")).toBeInTheDocument();
  await waitFor(() => expect(posts).toContain("/api/v1/agents/a1/edit/start"));
  expect(posts).not.toContain("/api/v1/agents/design");
});

// With no remount there is nothing left to fetch the FSM state, so the `state`
// the edit-start response now carries is the only thing that reveals this.
test("the Build button appears after the first reply", async () => {
  wrap();
  await screen.findByText("Diagnose");

  const box = screen.getByRole("textbox");
  await userEvent.type(box, "run it once a day");
  fireEvent.keyDown(box, { key: "Enter", code: "Enter" });

  expect(await screen.findByRole("button", { name: "🔨 Build it" })).toBeInTheDocument();
});
