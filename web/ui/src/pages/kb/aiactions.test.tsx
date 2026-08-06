import type { ReactNode } from "react";
import { render, screen, waitFor, fireEvent, renderHook, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Editor } from "@tiptap/core";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
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

function makeEditor(content = "<p>the pipeline runs on merge</p>") {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content,
  });
  return editor;
}

// For the single-paragraph docs every test in this file uses, a plain-text
// character offset is the ProseMirror position minus 1 (position 1 sits
// right after the paragraph's opening boundary). Selecting by NEEDLE rather
// than a hand-counted position keeps the tests readable and resistant to
// off-by-one mistakes in the fixture content.
function selectSubstring(editor: Editor, needle: string) {
  const text = editor.state.doc.textContent;
  const idx = text.indexOf(needle);
  if (idx < 0) throw new Error(`"${needle}" not found in "${text}"`);
  const from = idx + 1;
  const to = from + needle.length;
  editor.commands.setTextSelection({ from, to });
  return { from, to };
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockAssistFetch(body: unknown, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse(body, status));
}

// A fetch mock whose response only resolves once `release()` is called —
// for exercising the case where the request is genuinely still IN FLIGHT
// (not just already resolved) at the moment the panel is hidden.
function deferredAssistFetch(body: unknown, status = 200) {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async () => {
    await gate;
    return jsonResponse(body, status);
  });
  return { fetchMock, release };
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

// useAIActions calls useToast(), which throws outside a ToastProvider — so
// every render in this file goes through this wrapper, not just the tests
// that assert on a toast firing.
function withProviders(node: ReactNode, client: QueryClient) {
  return (
    <ToastProvider>
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
      <ToastHost />
    </ToastProvider>
  );
}

// Harness: mirrors production shape — useAIActions is owned by a component
// that stays mounted (WysiwygEditor in production), AIActions is a
// presentational child fed the resulting controlled state.
function Harness({ editor, path }: { editor: Editor; path: string }) {
  const state = useAIActions(editor, path);
  return <AIActions state={state} />;
}

function renderPanel(editor: Editor, path = "notes/ci.md") {
  return render(withProviders(<Harness editor={editor} path={path} />, queryClient()));
}

afterEach(() => {
  vi.restoreAllMocks();
  // vi.stubGlobal("navigator", ...) (used by the Explain-copy-fallback tests
  // below) isn't undone by restoreAllMocks — without this, a stubbed
  // navigator missing userAgent leaks into every later test in this file and
  // breaks tiptap's isiOS() check inside Editor construction.
  vi.unstubAllGlobals();
  mockOpen.mockClear();
});

test("a rewrite action shows the result with Accept and Discard", async () => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "The pipeline runs on every merge." });
  const editor = makeEditor();
  editor.commands.selectAll();
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
  editor.commands.selectAll();
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
  editor.commands.selectAll();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await screen.findByText("REWRITTEN");
  editor.commands.setTextSelection(1); // collapse the selection mid-flight
  await user.click(screen.getByRole("button", { name: /Accept/ }));
  // toContain would pass even if the rewrite were spliced in at the wrong
  // place (e.g. appended after the live, now-collapsed selection instead of
  // replacing the captured range) — selectAll() captured the WHOLE document,
  // so an exact match proves the captured range's content, not just its
  // presence somewhere, is what got replaced.
  await waitFor(() => expect(editor.getText().trim()).toBe("REWRITTEN"));
});

test("Explain offers Copy and never Accept", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ action: "explain", result: "It means X." });
  const editor = makeEditor();
  editor.commands.selectAll();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Explain/ }));
  expect(await screen.findByText("It means X.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Copy/ })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Accept/ })).not.toBeInTheDocument();
});

// Review finding: Explain's Copy called `navigator.clipboard?.writeText`
// directly instead of going through lib/copyText — "the app's single
// clipboard write", which exists precisely because `navigator.clipboard` is
// undefined over plain HTTP on a LAN (the normal way to reach a self-hosted
// install). Copy is Explain's ONLY affordance (its result can never be
// accepted into the note), so this made Explain entirely unusable there,
// with no feedback — the same failure messagemeta.test.tsx already pins for
// the chat copy button.
test("Explain's Copy falls back to execCommand when the Clipboard API is unavailable", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ action: "explain", result: "It means X." });
  // Editor construction reads navigator.userAgent (tiptap's isiOS check), so
  // it must happen BEFORE the navigator stub below replaces it with a plain
  // object that lacks that property.
  const editor = makeEditor();
  editor.commands.selectAll();
  vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
  const execCommand = vi.fn().mockReturnValue(true);
  document.execCommand = execCommand;

  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Explain/ }));
  await screen.findByText("It means X.");
  await user.click(screen.getByRole("button", { name: /^Copy$/ }));

  expect(execCommand).toHaveBeenCalledWith("copy");
  await waitFor(() => expect(screen.getByRole("button", { name: /Copied/ })).toBeInTheDocument());
});

// A silent no-op is exactly what let the original bug hide unnoticed.
test("Explain's Copy reports a failure when both the Clipboard API and execCommand fail", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ action: "explain", result: "It means X." });
  const editor = makeEditor();
  editor.commands.selectAll();
  vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
  document.execCommand = vi.fn().mockReturnValue(false);

  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Explain/ }));
  await screen.findByText("It means X.");
  await user.click(screen.getByRole("button", { name: /^Copy$/ }));

  await waitFor(() => expect(screen.getByRole("button", { name: /Copy failed/ })).toBeInTheDocument());
});

test("a failed request shows the server's message", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ error: { code: "coder_unavailable", message: "⚠️ out of quota" } }, 503);
  const editor = makeEditor();
  editor.commands.selectAll();
  renderPanel(editor);

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

function editorWithSelection() {
  const editor = makeEditor();
  editor.commands.selectAll();
  return editor;
}

test.each(REWRITE_LABELS)("%s fires the assist request exactly once on mouse click", async (label) => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "X" });
  renderPanel(editorWithSelection());

  await user.click(screen.getByRole("button", { name: label }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test.each(REWRITE_LABELS)("%s fires the assist request exactly once on Enter", async (label) => {
  const fetchMock = mockAssistFetch({ action: "improve", result: "X" });
  renderPanel(editorWithSelection());

  fireEvent.keyDown(screen.getByRole("button", { name: label }), { key: "Enter" });
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test.each(REWRITE_LABELS)("%s fires the assist request exactly once on Space", async (label) => {
  const fetchMock = mockAssistFetch({ action: "improve", result: "X" });
  renderPanel(editorWithSelection());

  fireEvent.keyDown(screen.getByRole("button", { name: label }), { key: " " });
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("Edit with AI opens the chat panel exactly once on mouse click", async () => {
  const user = userEvent.setup();
  renderPanel(editorWithSelection());

  await user.click(screen.getByRole("button", { name: "Edit with AI" }));
  expect(mockOpen).toHaveBeenCalledTimes(1);
});

test("Edit with AI opens the chat panel exactly once on Enter", () => {
  renderPanel(editorWithSelection());

  fireEvent.keyDown(screen.getByRole("button", { name: "Edit with AI" }), { key: "Enter" });
  expect(mockOpen).toHaveBeenCalledTimes(1);
});

test("Edit with AI opens the chat panel exactly once on Space", () => {
  renderPanel(editorWithSelection());

  fireEvent.keyDown(screen.getByRole("button", { name: "Edit with AI" }), { key: " " });
  expect(mockOpen).toHaveBeenCalledTimes(1);
});

// --- Finding 1: a result already fetched (and billed) must survive the
// panel unmounting, since AIActions lives inside BubbleMenu, which unmounts
// its children the instant the selection collapses (click away, scroll,
// arrow key) — which can easily happen mid-request or right after it lands. ---

// The hidden marker carries the hook's own live state as data attributes so
// tests can observe what useAIActions is doing WITHOUT the panel being on
// screen — the whole point being proved is that state changes even while
// nothing is rendering it.
function ToggleHarness({ editor, path, showPanel }: { editor: Editor; path: string; showPanel: boolean }) {
  // useAIActions is called unconditionally here, in a component that does
  // NOT unmount when showPanel flips — exactly mirroring how WysiwygEditor
  // stays mounted while BubbleToolbar/AIActions come and go underneath it.
  const state = useAIActions(editor, path);
  return showPanel ? (
    <AIActions state={state} />
  ) : (
    <div
      data-testid="panel-hidden"
      data-pending={state.assist.isPending}
      data-result={state.assist.data?.result ?? ""}
    />
  );
}

function toggleView(editor: Editor, path: string, showPanel: boolean, client: QueryClient) {
  return withProviders(<ToggleHarness editor={editor} path={path} showPanel={showPanel} />, client);
}

test("a result fetched while the panel is unmounted is not lost — it reappears when the panel remounts", async () => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "SURVIVED" });
  const editor = editorWithSelection();
  const client = queryClient();

  const { rerender } = render(toggleView(editor, "notes/ci.md", true, client));

  await user.click(screen.getByRole("button", { name: /Improve/ }));

  // Simulate the bubble menu hiding — the user clicked elsewhere, or
  // scrolled, while the request was in flight or had just landed. The
  // mutation lives in ToggleHarness (mirroring WysiwygEditor), not in
  // AIActions, so hiding the panel must not lose it.
  rerender(toggleView(editor, "notes/ci.md", false, client));
  expect(screen.getByTestId("panel-hidden")).toBeInTheDocument();
  expect(screen.queryByText("SURVIVED")).not.toBeInTheDocument();

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

  // The bubble reappears (the user selects text again) — the result that
  // was fetched while the panel was gone must still be there, not reset
  // back to the bare action row.
  rerender(toggleView(editor, "notes/ci.md", true, client));
  expect(await screen.findByText("SURVIVED")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Accept/ })).toBeInTheDocument();
});

test("a result still IN FLIGHT when the panel is hidden lands correctly and is not lost", async () => {
  const user = userEvent.setup();
  const { fetchMock, release } = deferredAssistFetch({ action: "improve", result: "SURVIVED" });
  const editor = editorWithSelection();
  const client = queryClient();

  const { rerender } = render(toggleView(editor, "notes/ci.md", true, client));

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(fetchMock).toHaveBeenCalledTimes(1);

  // Hide the panel WHILE the request is still pending — not after it
  // resolved, which is what the previous test (and the prior round's fix)
  // covered. The mutation must keep running underneath the hidden panel.
  rerender(toggleView(editor, "notes/ci.md", false, client));
  expect(screen.getByTestId("panel-hidden")).toHaveAttribute("data-pending", "true");
  expect(screen.getByTestId("panel-hidden")).toHaveAttribute("data-result", "");

  release();
  await waitFor(() => expect(screen.getByTestId("panel-hidden")).toHaveAttribute("data-result", "SURVIVED"));

  rerender(toggleView(editor, "notes/ci.md", true, client));
  expect(await screen.findByText("SURVIVED")).toBeInTheDocument();
});

// --- Reviewer-reported bug: the captured range was never remapped through
// edits made while the panel was hidden, so Accept spliced the rewrite into
// stale absolute positions — corrupting unrelated text. Fixed by mapping the
// range through every "transaction" event while an action is pending, with
// a live-text verification in accept() as the backstop for what mapping
// cannot express (the selected text itself being deleted). ---

test("Accept maps the range through an edit made BEFORE it while the panel was hidden (reviewer repro)", async () => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "BETA" });
  const editor = makeEditor("<p>the pipeline ALPHA runs on merge</p>");
  selectSubstring(editor, "ALPHA");
  const client = queryClient();

  const { rerender } = render(toggleView(editor, "notes/ci.md", true, client));

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

  rerender(toggleView(editor, "notes/ci.md", false, client));

  // Exactly the reviewer's repro: insert "PREFIX " at position 1, shifting
  // everything (including the captured range) forward by 7.
  editor.chain().insertContentAt(1, "PREFIX ").run();

  rerender(toggleView(editor, "notes/ci.md", true, client));
  await user.click(screen.getByRole("button", { name: /Accept/ }));

  // Correct outcome: ALPHA (now shifted) is replaced by BETA, and nothing
  // else in the document is touched. The buggy behaviour the reviewer
  // reproduced was "PREFIX the piBETAe ALPHA runs on merge" — spliced into
  // the middle of "pipeline", ALPHA left untouched.
  await waitFor(() => expect(editor.getText()).toBe("PREFIX the pipeline BETA runs on merge"));
});

test("Accept maps the range through an edit made AFTER it while the panel was hidden", async () => {
  const user = userEvent.setup();
  const fetchMock = mockAssistFetch({ action: "improve", result: "BETA" });
  const editor = makeEditor("<p>the pipeline ALPHA runs on merge</p>");
  selectSubstring(editor, "ALPHA");
  const client = queryClient();

  const { rerender } = render(toggleView(editor, "notes/ci.md", true, client));

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

  rerender(toggleView(editor, "notes/ci.md", false, client));

  // Append at the very end of the document — strictly AFTER the captured
  // range, which should not move at all, but the mapping call still runs
  // over every transaction regardless of where the edit lands.
  const end = editor.state.doc.content.size - 1;
  editor.chain().insertContentAt(end, " SUFFIX").run();

  rerender(toggleView(editor, "notes/ci.md", true, client));
  await user.click(screen.getByRole("button", { name: /Accept/ }));

  await waitFor(() => expect(editor.getText()).toBe("the pipeline BETA runs on merge SUFFIX"));
});

test("accept() refuses and tells the user when the selected text was deleted while the panel was hidden", async () => {
  const user = userEvent.setup();
  mockAssistFetch({ action: "improve", result: "BETA" });
  const editor = makeEditor("<p>the pipeline ALPHA runs on merge</p>");
  const { from, to } = selectSubstring(editor, "ALPHA");
  const client = queryClient();

  const { rerender } = render(toggleView(editor, "notes/ci.md", true, client));

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await screen.findByText("BETA");

  rerender(toggleView(editor, "notes/ci.md", false, client));

  // The user deletes exactly the passage that was selected — mapping alone
  // cannot say what "the same text" would even mean here, so this is the
  // backstop's job: refuse to write, and say why.
  editor.chain().deleteRange({ from, to }).run();
  const afterDeletion = editor.getHTML();

  rerender(toggleView(editor, "notes/ci.md", true, client));
  await user.click(screen.getByRole("button", { name: /Accept/ }));

  expect(editor.getHTML()).toBe(afterDeletion);
  // findAll: ToastHost renders the message twice — once in the visible
  // toast, once in its sr-only aria-live mirror (see tree.test.tsx's
  // "a failed move surfaces the server message as a toast" for the same
  // pattern).
  expect((await screen.findAllByText(/passage changed/i)).length).toBeGreaterThan(0);
});

// --- Finding 3: accept() must refuse to write to the note for Explain even
// if invoked directly, not merely because the UI never renders an Accept
// button in that branch. ---

test("accept() is a no-op for Explain even when called directly, bypassing the UI", async () => {
  mockAssistFetch({ action: "explain", result: "It means X." });
  const editor = editorWithSelection();
  const before = editor.getHTML();
  const client = queryClient();
  const wrapper = ({ children }: { children: ReactNode }) => withProviders(children, client);

  const { result } = renderHook(() => useAIActions(editor, "notes/ci.md"), { wrapper });

  act(() => result.current.run("explain"));
  await waitFor(() => expect(result.current.assist.data?.result).toBe("It means X."));

  act(() => result.current.accept());
  expect(editor.getHTML()).toBe(before);
});
