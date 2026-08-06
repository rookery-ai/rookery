import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Editor } from "@tiptap/core";
import AIActions from "./AIActions";
import { buildExtensions } from "./editor";
import { selectionChatPrompt } from "./ChatAboutFileButton";

function makeEditor() {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p>the pipeline runs on merge</p>",
  });
  editor.commands.selectAll();
  return editor;
}

function renderPanel(editor: Editor) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AIActions editor={editor} path="notes/ci.md" />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.restoreAllMocks());

test("a rewrite action shows the result with Accept and Discard", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "The pipeline runs on every merge." }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
  const editor = makeEditor();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(await screen.findByText("The pipeline runs on every merge.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Accept/ })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Discard/ })).toBeInTheDocument();
});

test("Discard leaves the document untouched", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "REWRITTEN" }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
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
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "REWRITTEN" }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
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
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "explain", result: "It means X." }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: /Explain/ }));
  expect(await screen.findByText("It means X.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Copy/ })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Accept/ })).not.toBeInTheDocument();
});

test("a failed request shows the server's message", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ error: { code: "coder_unavailable", message: "⚠️ out of quota" } }),
      { status: 503, headers: { "Content-Type": "application/json" } }),
  );
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(await screen.findByText(/out of quota/)).toBeInTheDocument();
});

test("the selection chat prompt names the file and quotes the passage", () => {
  const p = selectionChatPrompt("notes/ci.md", "the pipeline runs on merge");
  expect(p).toContain("notes/ci.md");
  expect(p).toContain("the pipeline runs on merge");
});
