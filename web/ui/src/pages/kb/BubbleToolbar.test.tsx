import { useEffect, useRef } from "react";
import { render, screen, act } from "@testing-library/react";
import { useEditor, EditorContent, type Editor } from "@tiptap/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import BubbleToolbar from "./BubbleToolbar";
import { useAIActions } from "./AIActions";
import { buildExtensions } from "./editor";

// Review finding: BubbleToolbar's shouldShow fully replaced TipTap's default
// (which includes an `editor.isEditable` check), so the whole toolbar —
// underline, both colour swatch grids, and the billable AI actions row —
// stayed live over a read-only note. A selection there could fire a paid
// coder call via Improve/Explain/Reformat and then autosave the result. The
// fix restores the `editor.isEditable` term. These tests drive a REAL
// BubbleMenu (not a shallow render) because the plugin removes its DOM
// element from the document entirely when hidden (see
// @tiptap/extension-bubble-menu's BubbleMenuView.hide(), which calls
// `this.element.remove()`) — so `screen.queryByRole` reliably reflects
// shouldShow's live verdict.

// The bubble menu debounces its update (default 250ms) whenever the
// selection is non-empty (@tiptap/extension-bubble-menu's
// BubbleMenuView.update -> handleDebouncedUpdate) before it decides whether
// to show/hide, so tests must wait past that window with real timers rather
// than asserting synchronously.
const PAST_DEBOUNCE_MS = 400;

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// Mirrors production wiring: useAIActions is called in a component that
// stays mounted for the editor's life (WysiwygEditor in NoteEditor.tsx), and
// BubbleToolbar receives it as a controlled prop. `editor` is handed back via
// `onGetEditor` so the test can drive selection with the exact instance
// BubbleToolbar itself renders against.
function Harness({ editable, onGetEditor }: { editable: boolean; onGetEditor: (editor: Editor) => void }) {
  const editor = useEditor({
    editable,
    extensions: buildExtensions(),
    content: "<p>the pipeline runs on merge</p>",
  });
  const aiActions = useAIActions(editor, "notes/ci.md");
  const reported = useRef(false);

  useEffect(() => {
    if (editor && !reported.current) {
      reported.current = true;
      onGetEditor(editor);
    }
  }, [editor, onGetEditor]);

  if (!editor) return null;
  return (
    <div>
      <EditorContent editor={editor} />
      <BubbleToolbar editor={editor} aiActions={aiActions} />
    </div>
  );
}

function renderHarness(editable: boolean) {
  let editor: Editor | null = null;
  const qc = new QueryClient();
  const utils = render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <Harness
          editable={editable}
          onGetEditor={(e) => {
            editor = e;
          }}
        />
        <ToastHost />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { ...utils, getEditor: () => editor };
}

// Every frontend test must mock fetch: /api/v1/kb/assist is billable (a real
// coder call) and a `claude` CLI can be live on PATH in some environments.
// This file renders the Improve/Explain/etc. row (via AIActions, inside the
// toolbar) but never clicks it — no test here should ever call kbAssist's
// mutation — this stub exists purely so an accidental future click (or a
// stray effect) can never reach a real network call.
beforeEach(() => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "unused" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
});

afterEach(() => {
  vi.restoreAllMocks();
});

test("a non-empty selection on an editable note shows the toolbar, including Bold and Improve", async () => {
  const { getEditor } = renderHarness(true);
  const editor = getEditor()!;

  act(() => {
    editor.commands.setTextSelection({ from: 1, to: 5 });
  });
  await act(() => sleep(PAST_DEBOUNCE_MS));

  expect(screen.getByRole("button", { name: "Bold" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Improve/ })).toBeInTheDocument();
});

test("a non-empty selection on a non-editable (read-only) note shows neither Bold nor Improve", async () => {
  const { getEditor } = renderHarness(false);
  const editor = getEditor()!;

  act(() => {
    editor.commands.setTextSelection({ from: 1, to: 5 });
  });
  await act(() => sleep(PAST_DEBOUNCE_MS));

  expect(screen.queryByRole("button", { name: "Bold" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Improve/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Edit with AI" })).not.toBeInTheDocument();
});
