import { useEffect, useRef, useState } from "react";
import type { Editor, EditorEvents } from "@tiptap/core";
import type { Fragment } from "@tiptap/pm/model";
import { Sparkles, SpellCheck, Lightbulb, WandSparkles, Loader2, Copy, Check, Undo2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useSlideOver } from "@/components/shell/AppShell";
import { useToast } from "@/components/shell/Toast";
import { GlobalChatPanel } from "@/components/chat/GlobalChatButton";
import { useKBAssist, type KBAssistAction } from "@/lib/kbAssist";
import { copyText } from "@/lib/copyText";
import { selectionEditPrompt } from "./ChatAboutFileButton";

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

export type CopyStatus = "idle" | "copied" | "failed";

export type AIActionsState = {
  action: KBAssistAction | null;
  assist: ReturnType<typeof useKBAssist>;
  run: (id: KBAssistAction) => void;
  reset: () => void;
  accept: () => void;
  openChat: () => void;
  copyStatus: CopyStatus;
  copyResult: () => void;
};

// The range AND the text it covered, captured at CLICK time.
type PendingRange = { from: number; to: number; text: string };

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
  const { toast } = useToast();
  const [action, setAction] = useState<KBAssistAction | null>(null);
  const [copyStatus, setCopyStatus] = useState<CopyStatus>("idle");
  const copyStatusTimerRef = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(copyStatusTimerRef.current), []);
  // A mutable ref, not React state: it must be updated synchronously off the
  // editor's own "transaction" event below — which fires independently of
  // any React render, including while this whole panel is unmounted and
  // nothing is subscribed to re-render on its change — and nothing here
  // needs a re-render; it is read only inside accept().
  const pendingRef = useRef<PendingRange | null>(null);

  // Keep the captured range aligned with the document through every edit
  // made while an action is pending — including edits made while the bubble
  // menu (and this whole panel) is unmounted, since collapsing the
  // selection to type elsewhere is exactly what hides it. Without this,
  // accept() would splice the rewrite into whatever now sits at the
  // ORIGINAL absolute positions, which after an intervening edit is not the
  // text that was rewritten — silent document corruption, not just a
  // wrong-place paste. Subscribed for the life of the component (guarded
  // internally by pendingRef.current being non-null), never per-action, so
  // this can't leak a handler.
  useEffect(() => {
    if (!editor) return;
    const onTransaction = ({ transaction, appendedTransactions }: EditorEvents["transaction"]) => {
      const pending = pendingRef.current;
      if (!pending) return;
      let from = pending.from;
      let to = pending.to;
      for (const tr of [transaction, ...appendedTransactions]) {
        // -1/+1 bias: an insertion exactly AT a boundary lands OUTSIDE the
        // captured range (from stays put, to stays put) rather than being
        // silently absorbed into "the text being rewritten".
        from = tr.mapping.map(from, -1);
        to = tr.mapping.map(to, 1);
      }
      pendingRef.current = { ...pending, from, to };
    };
    editor.on("transaction", onTransaction);
    return () => {
      editor.off("transaction", onTransaction);
    };
  }, [editor]);

  function run(id: KBAssistAction) {
    if (!editor) return;
    const { from, to } = editor.state.selection;
    pendingRef.current = { from, to, text: editor.state.doc.textBetween(from, to, "\n") };
    setAction(id);
    setCopyStatus("idle");
    assist.mutate({ action: id, path, selection: selectionMarkdown(editor) });
  }

  function reset() {
    pendingRef.current = null;
    setAction(null);
    setCopyStatus("idle");
    assist.reset();
  }

  // Explain's Copy button is its ONLY affordance — the result can never be
  // accepted into the note (see accept()'s explain guard below) — so a
  // silent clipboard failure leaves the user with no way to use the answer
  // at all. lib/copyText is the app's single clipboard write, precisely
  // because `navigator.clipboard` is undefined over plain HTTP on a LAN,
  // which is the normal way to reach a self-hosted install; a direct
  // `navigator.clipboard?.writeText` call here would silently no-op there,
  // same as MessageMeta's copy button before that fix.
  function copyResult() {
    if (!assist.data) return;
    void copyText(assist.data.result).then((ok) => {
      setCopyStatus(ok ? "copied" : "failed");
      window.clearTimeout(copyStatusTimerRef.current);
      copyStatusTimerRef.current = window.setTimeout(() => setCopyStatus("idle"), 1500);
    });
  }

  function accept() {
    // Explain's result is an answer ABOUT the passage, never a replacement
    // FOR it. This guard is the actual guarantee — the panel simply never
    // rendering an Accept button in that branch is UI, not enforcement.
    if (action === "explain") return;
    const pending = pendingRef.current;
    if (!editor || !pending || !assist.data) return;
    // Mapping (above) keeps the range aligned through ordinary edits, but it
    // cannot express every case — most notably the selected text itself
    // being deleted out from under the mapped range. Verify the live text
    // still matches what was captured at click time before writing
    // anything; refuse rather than risk splicing the rewrite into content
    // it was never generated from.
    const liveText = editor.state.doc.textBetween(pending.from, pending.to, "\n");
    if (liveText !== pending.text) {
      toast({
        message:
          "The selected passage changed since this rewrite was generated, so it wasn't applied. You can still copy the result.",
        variant: "error",
      });
      return;
    }
    const storage = editor.storage.markdown as unknown as MDStorage;
    const html = storage?.parser?.parse(assist.data.result, { inline: true });
    editor
      .chain()
      .focus()
      .deleteRange(pending)
      .insertContentAt(pending.from, html ?? assist.data.result)
      .run();
    reset();
  }

  function openChat() {
    if (!editor) return;
    // autoSend, not a prefill. Selecting a passage and clicking "Edit with AI"
    // IS the request; parking a quoted block in the composer made the user
    // retype what they had already expressed by getting here. ChatWindow owns
    // the once-per-mount / once-per-chat guards, so this cannot double-send.
    open(
      <GlobalChatPanel
        forceNew
        autoSend
        initialText={selectionEditPrompt(path, selectionMarkdown(editor))}
      />,
      { title: "Chat" },
    );
  }

  return { action, assist, run, reset, accept, openChat, copyStatus, copyResult };
}

// Purely presentational: every action reads/writes through `state`, which is
// owned by useAIActions above and lives in a component that survives this
// one's own mount/unmount cycle inside the bubble menu.
export default function AIActions({ state }: { state: AIActionsState }) {
  const { action, assist, run, reset, accept, openChat, copyStatus, copyResult } = state;

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
              // passage, not an edit of it. Copy is its ONLY affordance, so a
              // failure (or success) is surfaced via the label/icon rather
              // than silently no-oping — see copyResult's doc comment.
              <Button
                size="sm"
                variant="outline"
                onMouseDown={(e) => {
                  e.preventDefault();
                  copyResult();
                }}
                onClick={copyResult}
              >
                {copyStatus === "copied" ? (
                  <Check className="size-4" />
                ) : copyStatus === "failed" ? (
                  <X className="size-4" />
                ) : (
                  <Copy className="size-4" />
                )}
                {copyStatus === "copied" ? "Copied" : copyStatus === "failed" ? "Copy failed" : "Copy"}
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
                  // No duplicate onClick here, unlike Discard/Copy above:
                  // accept()'s refusal path (the range's live text no longer
                  // matches what was captured) shows a toast, which is NOT
                  // idempotent to double-fire the way reset()/a clipboard
                  // write is — a live mouse click would otherwise stack two
                  // identical toasts. onKeyDown covers Enter/Space instead,
                  // same as the four action buttons above.
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      accept();
                    }
                  }}
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
