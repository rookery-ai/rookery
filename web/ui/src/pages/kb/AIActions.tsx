import { useState } from "react";
import type { Editor } from "@tiptap/core";
import type { Fragment } from "@tiptap/pm/model";
import { Sparkles, SpellCheck, Lightbulb, WandSparkles, Loader2, Copy, Check, Undo2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useSlideOver } from "@/components/shell/AppShell";
import { GlobalChatPanel } from "@/components/chat/GlobalChatButton";
import { useKBAssist, type KBAssistAction } from "@/lib/kbAssist";
import { selectionChatPrompt } from "./ChatAboutFileButton";

const ACTIONS: { id: KBAssistAction; label: string; icon: typeof Sparkles }[] = [
  { id: "improve", label: "Improve", icon: Sparkles },
  { id: "proofread", label: "Proofread", icon: SpellCheck },
  { id: "explain", label: "Explain", icon: Lightbulb },
  { id: "reformat", label: "Reformat", icon: WandSparkles },
];

// tiptap-markdown's own .d.ts only declares `options`/`getMarkdown()` on
// MarkdownStorage — `parser`/`serializer` are real runtime fields (see
// node_modules/tiptap-markdown/src/Markdown.js's addStorage) that the
// published types simply don't surface. Declared locally rather than
// widening the shared `Storage.markdown` augmentation in editor.ts, since
// nothing else in the app needs these two.
type MDStorage = {
  parser?: { parse: (content: string, opts?: { inline?: boolean }) => string };
  serializer?: { serialize: (content: Fragment) => string };
};

// The markdown of the SELECTED SLICE, not its plain text: Reformat needs to see
// structure, and an accepted result is parsed back through markdown so a
// returned list becomes a real list rather than literal "- " characters.
function selectionMarkdown(editor: Editor): string {
  const { from, to } = editor.state.selection;
  const slice = editor.state.doc.slice(from, to);
  const storage = editor.storage.markdown as unknown as MDStorage;
  if (storage?.serializer) return storage.serializer.serialize(slice.content);
  return editor.state.doc.textBetween(from, to, "\n");
}

export type AIActionsState = {
  action: KBAssistAction | null;
  assist: ReturnType<typeof useKBAssist>;
  run: (id: KBAssistAction) => void;
  reset: () => void;
  accept: () => void;
  openChat: () => void;
};

// Owns everything that must OUTLIVE the bubble menu's mount cycle: the
// captured range, which action is running/done, and the mutation itself.
// AIActions (the panel) is mounted inside TipTap's BubbleMenu, which
// unmounts its children whenever the selection collapses — a click
// elsewhere, a scroll, an arrow key. A rewrite call takes real seconds, and
// the request has already been sent (and billed) by the time that happens,
// so the result must not live in the component that's about to disappear.
// Call this once in a component that stays mounted for the life of the
// editor (WysiwygEditor in NoteEditor.tsx) and pass the returned object
// down as a controlled prop — AIActions itself holds no state of its own.
export function useAIActions(editor: Editor | null, path: string): AIActionsState {
  const assist = useKBAssist();
  const { open } = useSlideOver();
  // Captured at CLICK time. The selection must survive an async round trip and
  // a re-render; applying to the LIVE selection would paste the rewrite
  // wherever the caret happens to be when the response lands.
  const [range, setRange] = useState<{ from: number; to: number } | null>(null);
  const [action, setAction] = useState<KBAssistAction | null>(null);

  function run(id: KBAssistAction) {
    if (!editor) return;
    const { from, to } = editor.state.selection;
    setRange({ from, to });
    setAction(id);
    assist.mutate({ action: id, path, selection: selectionMarkdown(editor) });
  }

  function reset() {
    setRange(null);
    setAction(null);
    assist.reset();
  }

  function accept() {
    // Explain's result is an answer ABOUT the passage, never a replacement
    // FOR it. This guard is the actual guarantee — the panel simply never
    // rendering an Accept button in that branch is UI, not enforcement.
    if (action === "explain") return;
    if (!editor || !range || !assist.data) return;
    const storage = editor.storage.markdown as unknown as MDStorage;
    const html = storage?.parser?.parse(assist.data.result, { inline: true });
    editor
      .chain()
      .focus()
      .deleteRange(range)
      .insertContentAt(range.from, html ?? assist.data.result)
      .run();
    reset();
  }

  function openChat() {
    if (!editor) return;
    open(<GlobalChatPanel forceNew initialText={selectionChatPrompt(path, selectionMarkdown(editor))} />, {
      title: "Chat",
    });
  }

  return { action, assist, run, reset, accept, openChat };
}

// Purely presentational: every action reads/writes through `state`, which is
// owned by useAIActions above and lives in a component that survives this
// one's own mount/unmount cycle inside the bubble menu.
export default function AIActions({ state }: { state: AIActionsState }) {
  const { action, assist, run, reset, accept, openChat } = state;

  if (!action) {
    return (
      <div className="flex items-center gap-0.5 border-t border-border p-1">
        {ACTIONS.map(({ id, label, icon: Icon }) => (
          <button
            key={id}
            type="button"
            aria-label={label}
            title={label}
            // Mousedown, not click — a click collapses the selection first.
            // These actions spend a real LLM call, so — unlike the idempotent
            // colour swatches in BubbleToolbar — onClick is deliberately NOT
            // also wired (that would double-fire on every mouse click, i.e.
            // 2x the request). onKeyDown covers Enter/Space keyboard
            // activation instead, which never fires alongside a mouse click.
            onMouseDown={(e) => {
              e.preventDefault();
              run(id);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                run(id);
              }
            }}
            className="inline-flex items-center gap-1 rounded-sm px-1.5 py-1 text-xs text-foreground hover:bg-accent"
          >
            <Icon className="size-3.5" />
            {label}
          </button>
        ))}
        <button
          type="button"
          aria-label="Edit with AI"
          title="Edit with AI"
          // Same reasoning as the action buttons above: opening the chat
          // panel is not free to double-fire (forceNew creates a new chat),
          // so keyboard access goes through onKeyDown, not a duplicate
          // onClick.
          onMouseDown={(e) => {
            e.preventDefault();
            openChat();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              openChat();
            }
          }}
          className="inline-flex items-center gap-1 rounded-sm px-1.5 py-1 text-xs text-accent hover:bg-accent-soft"
        >
          Edit with AI
        </button>
      </div>
    );
  }

  const label = ACTIONS.find((a) => a.id === action)?.label ?? "";
  return (
    <div className="w-80 border-t border-border p-2 text-sm">
      <div className="mb-1 flex items-center justify-between text-xs text-muted-2">
        <span>{label}</span>
        <button
          type="button"
          aria-label="Close"
          onMouseDown={(e) => {
            e.preventDefault();
            reset();
          }}
          onClick={reset}
        >
          <X className="size-3.5" />
        </button>
      </div>

      {assist.isPending && (
        <div className="flex items-center gap-2 py-3 text-muted-2">
          <Loader2 className="size-4 animate-spin" /> Working…
        </div>
      )}

      {assist.isError && (
        <div className="py-2 text-danger">
          {assist.error instanceof Error ? assist.error.message : "That didn't work."}
        </div>
      )}

      {assist.data && (
        <>
          <div className="max-h-56 overflow-y-auto whitespace-pre-wrap rounded-sm bg-chrome p-2">
            {assist.data.result}
          </div>
          <div className="mt-2 flex items-center justify-end gap-2">
            {action === "explain" ? (
              // Explain never writes to the note — it is a question about the
              // passage, not an edit of it.
              <Button
                size="sm"
                variant="outline"
                onMouseDown={(e) => {
                  e.preventDefault();
                  void navigator.clipboard?.writeText(assist.data!.result);
                }}
                onClick={() => void navigator.clipboard?.writeText(assist.data!.result)}
              >
                <Copy className="size-4" />
                Copy
              </Button>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="ghost"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    reset();
                  }}
                  onClick={reset}
                >
                  <Undo2 className="size-4" />
                  Discard
                </Button>
                <Button
                  size="sm"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    accept();
                  }}
                  onClick={accept}
                >
                  <Check className="size-4" />
                  Accept
                </Button>
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}
