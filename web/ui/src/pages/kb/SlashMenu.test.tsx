import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useEditor, EditorContent } from "@tiptap/react";
import { buildExtensions } from "./editor";
import { slashSuggestion } from "./SlashMenu";

// Mounts the real `useEditor`/`EditorContent` pair (the same machinery
// NoteEditor.tsx's WysiwygEditor uses) rather than a headless
// `new Editor(...)` — the slash popup is a `ReactRenderer` (SlashMenu.tsx)
// whose element only gets portal-rendered via the editor's
// `contentComponent`, which is set up by `EditorContent`. A headless/detached
// editor never wires that up, so the popup element exists in the DOM but
// stays permanently empty — genuinely not drivable that way. Mounted through
// `EditorContent`, "/" typed by a real user event opens a fully-rendered
// popup, proving the suggestion plumbing (and this fix) end-to-end.
function Harness() {
  const editor = useEditor({
    extensions: buildExtensions([slashSuggestion()]),
    content: "",
  });
  return <EditorContent editor={editor} />;
}

function renderHarness() {
  render(<Harness />);
  return screen.getByRole("textbox");
}

test("typing '/' opens the slash popup with the block-type items", async () => {
  const user = userEvent.setup();
  const contentEditable = renderHarness();

  await user.click(contentEditable);
  await user.type(contentEditable, "/");

  expect(await screen.findByText("Heading 1")).toBeInTheDocument();
});

// Regression test: the extension's onKeyDown returned `true` for Escape
// (correctly swallowing the keystroke so it doesn't leak into the editor as
// literal text) but never actually told the popup to close — so it stayed
// open until the user typed something else or clicked away.
test("Escape closes the slash popup instead of leaving it open", async () => {
  const user = userEvent.setup();
  const contentEditable = renderHarness();

  await user.click(contentEditable);
  await user.type(contentEditable, "/");
  await screen.findByText("Heading 1");

  await user.keyboard("{Escape}");

  await waitFor(() => expect(screen.queryByText("Heading 1")).not.toBeInTheDocument());
});
