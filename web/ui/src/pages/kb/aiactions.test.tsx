import type { ReactNode } from "react";
import { render, screen, waitFor, fireEvent, renderHook, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Editor } from "@tiptap/core";
import AIActions, { useAIActions } from "./AIActions";
import { buildExtensions } from "./editor";
import { selectionChatPrompt } from "./ChatAboutFileButton";

// useAIActions calls useSlideOver — mocked so "Edit with AI" activations can
// be pinned by call count without needing a real ShellCtx.Provider (and
// without actually mounting GlobalChatPanel, which would need its own
// /api/v1/chats fetch mocking unrelated to this file's concern).
const { mockOpen } = vi.hoisted(() => ({ mockOpen: vi.fn() }));
vi.mock("@/components/shell/AppShell", () => ({
  useSlideOver: () => ({ open: mockOpen, close: vi.fn() }),
}));

function makeEditor() {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p>the pipeline runs on merge</p>",
  });
  editor.commands.selectAll();
  return editor;
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockAssistFetch(body: unknown, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse(body, status));
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

// Harness: mirrors production shape — useAIActions is owned by a component
// that stays mounted (WysiwygEditor in production), AIActions is a
// presentational child fed the resulting controlled state.
function Harness({ editor, path }: { editor: Editor; path: string }) {
  const state = useAIActions(editor, path);
  return <AIActions state={state} />;
}

function renderPanel(editor: Editor, path = "notes/ci.md") {
  return render(
    <QueryClientProvider client={queryClient()}>
      <Harness editor={editor} path={path} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  mockOpen.mockClear();
});

test("a rewrite action shows the result with Accept and Discard", async () => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "The pipeline runs on every merge." });
  const editor = makeEditor();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(await screen.findByText("The pipeline runs on every merge.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Accept/ })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Discard/ })).toBeInTheDocument();
  // A mouse click must fire the action exactly once — this is a paid coder
  // call, not a free idempotent editor command, so a duplicated
  // mousedown+click handler pair would double the request on every click.
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("Discard leaves the document untouched", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ action: "improve", result: "REWRITTEN" });
  const editor = makeEditor();
  const before = editor.getHTML();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await screen.findByText("REWRITTEN");
  await user.click(screen.getByRole("button", { name: /Discard/ }));
  expect(editor.getHTML()).toBe(before);
});

test("Accept replaces the captured range, not the live selection", async () => {
  // The range is captured at CLICK time and applied on Accept. If Accept used
  // the live selection, anything that moved the caret during the round trip
  // would paste the rewrite in the wrong place.
  const user = userEvent.setup();
  mockAssistFetch({ action: "improve", result: "REWRITTEN" });
  const editor = makeEditor();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await screen.findByText("REWRITTEN");
  editor.commands.setTextSelection(1); // collapse the selection mid-flight
  await user.click(screen.getByRole("button", { name: /Accept/ }));
  await waitFor(() => expect(editor.getText()).toContain("REWRITTEN"));
});

test("Explain offers Copy and never Accept", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ action: "explain", result: "It means X." });
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: /Explain/ }));
  expect(await screen.findByText("It means X.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Copy/ })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Accept/ })).not.toBeInTheDocument();
});

test("a failed request shows the server's message", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ error: { code: "coder_unavailable", message: "⚠️ out of quota" } }, 503);
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(await screen.findByText(/out of quota/)).toBeInTheDocument();
});

test("the selection chat prompt names the file and quotes the passage", () => {
  const p = selectionChatPrompt("notes/ci.md", "the pipeline runs on merge");
  expect(p).toContain("notes/ci.md");
  expect(p).toContain("the pipeline runs on merge");
});

// --- Finding 2: every money-spending control fires exactly once per
// activation, on every input path (mouse click, Enter, Space). ---

const REWRITE_LABELS = ["Improve", "Proofread", "Explain", "Reformat"] as const;

test.each(REWRITE_LABELS)("%s fires the assist request exactly once on mouse click", async (label) => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "X" });
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: label }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test.each(REWRITE_LABELS)("%s fires the assist request exactly once on Enter", async (label) => {
  const fetchMock = mockAssistFetch({ action: "improve", result: "X" });
  renderPanel(makeEditor());

  fireEvent.keyDown(screen.getByRole("button", { name: label }), { key: "Enter" });
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test.each(REWRITE_LABELS)("%s fires the assist request exactly once on Space", async (label) => {
  const fetchMock = mockAssistFetch({ action: "improve", result: "X" });
  renderPanel(makeEditor());

  fireEvent.keyDown(screen.getByRole("button", { name: label }), { key: " " });
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("Edit with AI opens the chat panel exactly once on mouse click", async () => {
  const user = userEvent.setup();
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: "Edit with AI" }));
  expect(mockOpen).toHaveBeenCalledTimes(1);
});

test("Edit with AI opens the chat panel exactly once on Enter", () => {
  renderPanel(makeEditor());

  fireEvent.keyDown(screen.getByRole("button", { name: "Edit with AI" }), { key: "Enter" });
  expect(mockOpen).toHaveBeenCalledTimes(1);
});

test("Edit with AI opens the chat panel exactly once on Space", () => {
  renderPanel(makeEditor());

  fireEvent.keyDown(screen.getByRole("button", { name: "Edit with AI" }), { key: " " });
  expect(mockOpen).toHaveBeenCalledTimes(1);
});

// --- Finding 1: a result already fetched (and billed) must survive the
// panel unmounting, since AIActions lives inside BubbleMenu, which unmounts
// its children the instant the selection collapses (click away, scroll,
// arrow key) — which can easily happen mid-request or right after it lands. ---

function ToggleHarness({ editor, path, showPanel }: { editor: Editor; path: string; showPanel: boolean }) {
  // useAIActions is called unconditionally here, in a component that does
  // NOT unmount when showPanel flips — exactly mirroring how WysiwygEditor
  // stays mounted while BubbleToolbar/AIActions come and go underneath it.
  const state = useAIActions(editor, path);
  return showPanel ? <AIActions state={state} /> : <div data-testid="panel-hidden" />;
}

test("a result fetched while the panel is unmounted is not lost — it reappears when the panel remounts", async () => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "SURVIVED" });
  const editor = makeEditor();
  const client = queryClient();

  const { rerender } = render(
    <QueryClientProvider client={client}>
      <ToggleHarness editor={editor} path="notes/ci.md" showPanel />
    </QueryClientProvider>,
  );

  await user.click(screen.getByRole("button", { name: /Improve/ }));

  // Simulate the bubble menu hiding — the user clicked elsewhere, or
  // scrolled, while the request was in flight or had just landed. The
  // mutation lives in ToggleHarness (mirroring WysiwygEditor), not in
  // AIActions, so hiding the panel must not lose it.
  rerender(
    <QueryClientProvider client={client}>
      <ToggleHarness editor={editor} path="notes/ci.md" showPanel={false} />
    </QueryClientProvider>,
  );
  expect(screen.getByTestId("panel-hidden")).toBeInTheDocument();
  expect(screen.queryByText("SURVIVED")).not.toBeInTheDocument();

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

  // The bubble reappears (the user selects text again) — the result that
  // was fetched while the panel was gone must still be there, not reset
  // back to the bare action row.
  rerender(
    <QueryClientProvider client={client}>
      <ToggleHarness editor={editor} path="notes/ci.md" showPanel />
    </QueryClientProvider>,
  );
  expect(await screen.findByText("SURVIVED")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Accept/ })).toBeInTheDocument();
});

// --- Finding 3: accept() must refuse to write to the note for Explain even
// if invoked directly, not merely because the UI never renders an Accept
// button in that branch. ---

test("accept() is a no-op for Explain even when called directly, bypassing the UI", async () => {
  mockAssistFetch({ action: "explain", result: "It means X." });
  const editor = makeEditor();
  const before = editor.getHTML();
  const client = queryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );

  const { result } = renderHook(() => useAIActions(editor, "notes/ci.md"), { wrapper });

  act(() => result.current.run("explain"));
  await waitFor(() => expect(result.current.assist.data?.result).toBe("It means X."));

  act(() => result.current.accept());
  expect(editor.getHTML()).toBe(before);
});
