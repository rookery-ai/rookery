import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProviderActions } from "./ProviderActions";
import type { ConnectorAction, ServiceProvider } from "@/lib/connections";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const PROVIDER: ServiceProvider = {
  name: "github",
  label: "GitHub",
  category: "Developer",
  kind: "oauth",
  setup_url: "",
  setup_steps: [],
  has_creds: true,
  action_count: 3,
  connect_inputs: [],
  connections: [],
};

const ACTIONS: ConnectorAction[] = [
  {
    name: "github_search_issues",
    description: "Search issues and pull requests across your repos",
    mutating: false,
    public_write: false,
    params: {
      properties: {
        query: { type: "string", description: "GitHub issue search query" },
        max: { type: "integer", description: "max results" },
      },
      required: ["query"],
    },
  },
  {
    name: "github_create_issue",
    description: "Open a new issue in a repository",
    mutating: true,
    public_write: false,
    params: { properties: { owner: { type: "string" } }, required: ["owner"] },
  },
  {
    name: "github_comment_publicly",
    description: "Post a public comment on an issue",
    mutating: true,
    public_write: true,
    params: {},
  },
];

function mockActions(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(jsonResponse(body, status))),
  );
}

function wrap(onBack = () => {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ProviderActions provider={PROVIDER} onBack={onBack} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("lists each action's description with a capability badge", async () => {
  mockActions({ actions: ACTIONS });
  wrap();

  expect(
    await screen.findByText("Search issues and pull requests across your repos"),
  ).toBeInTheDocument();
  expect(screen.getByText("Open a new issue in a repository")).toBeInTheDocument();

  expect(screen.getByText("read")).toBeInTheDocument();
  expect(screen.getByText("writes")).toBeInTheDocument();
  expect(screen.getByText("posts publicly")).toBeInTheDocument();
});

test("rows start collapsed and expand independently to show tool name and params", async () => {
  const user = userEvent.setup();
  mockActions({ actions: ACTIONS });
  wrap();

  const first = await screen.findByRole("button", {
    name: /Search issues and pull requests/,
  });
  expect(screen.queryByText("github_search_issues")).not.toBeInTheDocument();

  await user.click(first);
  expect(screen.getByText("github_search_issues")).toBeInTheDocument();
  // "max" is the OPTIONAL param: its span is a single text node. "query" is
  // required, so its span renders "query" + "*" and would not match exactly.
  expect(screen.getByText("max")).toBeInTheDocument();
  expect(screen.getByText("— GitHub issue search query")).toBeInTheDocument();

  // A second row stays collapsed — expansion is per-row, not global.
  expect(screen.queryByText("github_create_issue")).not.toBeInTheDocument();
});

test("an action with no parameters says so instead of rendering an empty list", async () => {
  const user = userEvent.setup();
  mockActions({ actions: ACTIONS });
  wrap();

  const publicRow = await screen.findByRole("button", {
    name: /Post a public comment/,
  });
  await user.click(publicRow);
  expect(screen.getByText("No parameters.")).toBeInTheDocument();
});

test("Back invokes onBack", async () => {
  const user = userEvent.setup();
  mockActions({ actions: ACTIONS });
  const onBack = vi.fn();
  wrap(onBack);

  await user.click(await screen.findByRole("button", { name: /Back/ }));
  expect(onBack).toHaveBeenCalledTimes(1);
});

test("a failed fetch shows an error instead of an empty list", async () => {
  mockActions({ error: { message: "boom" } }, 500);
  wrap();

  expect(await screen.findByText(/Couldn't load actions/)).toBeInTheDocument();
});

test("a provider with no actions shows an empty note", async () => {
  mockActions({ actions: [] });
  wrap();

  expect(
    await screen.findByText("This service exposes no actions yet."),
  ).toBeInTheDocument();
});
