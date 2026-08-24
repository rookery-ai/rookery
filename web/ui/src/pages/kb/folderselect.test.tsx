import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { FolderSelect } from "./FolderSelect";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const AGENT_ID = "9f3c1b2a-0000-4000-8000-000000000001";

function mockFolders() {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          folders: [
            { path: "", label: "" },
            { path: "notes", label: "Notes" },
            { path: `agents/${AGENT_ID}`, label: "Agents/Weather Digest" },
            {
              path: `agents/${AGENT_ID}/logs`,
              label: "Agents/Weather Digest/logs",
            },
          ],
        }),
      ),
    ),
  );
}

function wrap(props?: { disabledPaths?: string[] }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <FolderSelect value="" onChange={() => {}} {...props} />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

// The reported bug: picking a location for a new file offered a bare UUID for
// every agent folder.
test("renders the server's label, never the raw agent id", async () => {
  mockFolders();
  wrap();

  expect(
    await screen.findByRole("option", { name: "Agents/Weather Digest" }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: /9f3c1b2a/ })).toBeNull();
});

// The label is presentation only — writes must still use the real vault path,
// or a file lands in a directory named after the agent instead of in it.
test("the option's value stays the real vault path", async () => {
  mockFolders();
  wrap();

  const opt = (await screen.findByRole("option", {
    name: "Agents/Weather Digest/logs",
  })) as HTMLOptionElement;
  expect(opt.value).toBe(`agents/${AGENT_ID}/logs`);
});

// disabledPaths carries real vault paths, so the filter has to run against
// `path`. Matching labels instead would silently stop excluding a folder the
// moment its label diverged from its path — which is precisely what an agent
// folder's label does.
test("disabledPaths excludes by path, including descendants", async () => {
  mockFolders();
  wrap({ disabledPaths: [`agents/${AGENT_ID}`] });

  expect(await screen.findByRole("option", { name: "Notes" })).toBeInTheDocument();
  expect(
    screen.queryByRole("option", { name: "Agents/Weather Digest" }),
  ).toBeNull();
  expect(
    screen.queryByRole("option", { name: "Agents/Weather Digest/logs" }),
  ).toBeNull();
});

// The root is the one option the picker names itself; an empty label from the
// server must not render as a blank line.
test("the vault root renders as / (root)", async () => {
  mockFolders();
  wrap();
  expect(
    await screen.findByRole("option", { name: "/ (root)" }),
  ).toBeInTheDocument();
});
