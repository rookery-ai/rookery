import { useCallback, useEffect, useRef, useState } from "react";
import { useEditor, EditorContent } from "@tiptap/react";
import { Download, FileCode, AlertTriangle, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useKBNote, useSaveNote, rawURL } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import { buildExtensions, toMarkdown, checkFidelity } from "./editor";
import "./editor.css";

export type SaveState = "saved" | "saving" | "dirty" | "error" | "raw";

const AUTOSAVE_MS = 1000;

const RAW_BANNER =
  "Opened in raw markdown to protect formatting this editor can't represent yet.";

// Isolated so `useEditor` is only ever called while WYSIWYG mode is active —
// mounting/unmounting this component is how we avoid running the TipTap
// editor at all over content whose fidelity check failed.
function WysiwygEditor({
  content,
  onDirty,
  registerGetContent,
}: {
  content: string;
  onDirty: () => void;
  registerGetContent: (fn: () => string) => void;
}) {
  const editor = useEditor({
    extensions: buildExtensions(),
    content,
    onUpdate: () => onDirty(),
  });

  useEffect(() => {
    if (!editor) return;
    registerGetContent(() => toMarkdown(editor));
  }, [editor, registerGetContent]);

  return <EditorContent editor={editor} className="note-editor-content" />;
}

export default function NoteEditor({
  path,
  onStateChange,
}: {
  path: string;
  onStateChange?: (state: SaveState) => void;
}) {
  const { data, isLoading, isError } = useKBNote(path);
  const saveNote = useSaveNote();

  const [mode, setMode] = useState<"wysiwyg" | "raw" | null>(null);
  const [fidelityFailed, setFidelityFailed] = useState(false);
  const [overrideAccepted, setOverrideAccepted] = useState(false);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [rawText, setRawText] = useState("");

  const initializedRef = useRef(false);
  const dirtyRef = useRef(false);
  const savingRef = useRef(false);
  const pendingReflushRef = useRef(false);
  const mountedRef = useRef(true);
  const timerRef = useRef<number | undefined>(undefined);
  const rawTextRef = useRef("");
  const getContentRef = useRef<() => string>(() => "");
  const modeRef = useRef<"wysiwyg" | "raw" | null>(null);

  useEffect(() => {
    modeRef.current = mode;
  }, [mode]);

  // mountedRef guards state updates from async save callbacks that resolve
  // after the component (or this note's instance — KBPage keys NoteEditor
  // by path) has already unmounted.
  useEffect(() => {
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Determine WYSIWYG-vs-raw once, the first time the note loads.
  useEffect(() => {
    if (!data || initializedRef.current) return;
    initializedRef.current = true;
    const lossy = !checkFidelity(data.content);
    setFidelityFailed(lossy);
    setMode(lossy ? "raw" : "wysiwyg");
    setRawText(data.content);
    rawTextRef.current = data.content;
    getContentRef.current = () => rawTextRef.current;
    const initial: SaveState = lossy ? "raw" : "saved";
    setSaveState(initial);
    onStateChange?.(initial);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const report = useCallback(
    (s: SaveState) => {
      if (!mountedRef.current) return;
      setSaveState(s);
      onStateChange?.(s);
    },
    [onStateChange],
  );

  const idleState = useCallback((): SaveState => (modeRef.current === "raw" ? "raw" : "saved"), []);

  // Note on the dirty/saving contract (fixes a data-loss bug from an earlier
  // version that cleared dirtyRef unconditionally before the PUT resolved —
  // a failed save left the flag falsely clean, so Ctrl/Cmd+S became a silent
  // no-op and unmount-flush skipped, permanently dropping the edit):
  //  - dirtyRef is cleared ONLY inside onSuccess, and only if nothing else
  //    changed the content while the request was in flight (compared by
  //    value against the snapshot this call sent).
  //  - onError never touches dirtyRef — the edit stays pending so the next
  //    Ctrl/Cmd+S or the unmount-flush effect retries with fresh content.
  //  - savingRef prevents two concurrent PUTs; a flush() call that arrives
  //    while one is already in flight (e.g. Ctrl+S mid-save) doesn't get
  //    dropped — it sets pendingReflushRef, and the in-flight request's
  //    completion handler re-invokes flush() once it's done.
  const flush = useCallback(() => {
    if (savingRef.current) {
      pendingReflushRef.current = true;
      return;
    }
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
      timerRef.current = undefined;
    }
    if (!dirtyRef.current) return;
    savingRef.current = true;
    const content = getContentRef.current();
    report("saving");
    saveNote.mutate(
      { path, content },
      {
        onSuccess: () => {
          savingRef.current = false;
          if (getContentRef.current() === content) {
            dirtyRef.current = false;
            report(idleState());
          } else {
            // A newer edit landed while this save was in flight — stay
            // dirty and let the reflush below (or the debounce timer that
            // edit already rescheduled) pick it up.
            report("dirty");
          }
          if (pendingReflushRef.current) {
            pendingReflushRef.current = false;
            flush();
          }
        },
        onError: (err) => {
          savingRef.current = false;
          if (mountedRef.current) {
            setErrorMessage(err instanceof ApiError ? err.message : "Failed to save");
          }
          report("error");
          if (pendingReflushRef.current) {
            pendingReflushRef.current = false;
            flush();
          }
        },
      },
    );
  }, [path, saveNote, report, idleState]);

  const markDirty = useCallback(() => {
    dirtyRef.current = true;
    setErrorMessage(null);
    report("dirty");
    if (timerRef.current !== undefined) window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(flush, AUTOSAVE_MS);
  }, [flush, report]);

  // Ctrl/Cmd+S flushes immediately instead of waiting for the debounce.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        flush();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [flush]);

  // Flush any pending save on unmount (e.g. navigating to another note)
  // rather than losing it to a cancelled debounce timer. Fire-and-forget:
  // there's no component left to receive onSuccess/onError, and no need to
  // touch dirtyRef here — this instance is going away either way, and if a
  // save is already in flight this fires anyway in case a newer edit landed
  // after that request's content snapshot was taken.
  useEffect(() => {
    return () => {
      if (timerRef.current !== undefined) window.clearTimeout(timerRef.current);
      if (dirtyRef.current) {
        saveNote.mutate({ path, content: getContentRef.current() });
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  const registerGetContent = useCallback((fn: () => string) => {
    getContentRef.current = fn;
  }, []);

  const handleRawChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value;
    rawTextRef.current = value;
    setRawText(value);
    markDirty();
  };

  const switchToRaw = () => {
    if (modeRef.current === "wysiwyg") {
      const latest = getContentRef.current();
      rawTextRef.current = latest;
      setRawText(latest);
    }
    setMode("raw");
    getContentRef.current = () => rawTextRef.current;
    if (!dirtyRef.current) report("raw");
  };

  const switchToWysiwyg = () => {
    setOverrideAccepted(true);
    setMode("wysiwyg");
    if (!dirtyRef.current) report("saved");
  };

  if (isLoading || mode === null) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-muted-2">
        <Loader2 className="size-5 animate-spin" />
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-sm text-danger">
        Couldn't load this note.
      </div>
    );
  }

  const showBanner = mode === "raw" && fidelityFailed && !overrideAccepted;

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-2">
        <div className="min-w-0 truncate text-xs text-muted-2">{path}</div>
        <div className="flex items-center gap-2">
          <StatusIndicator state={saveState} errorMessage={errorMessage} />
          {mode === "wysiwyg" ? (
            <Button variant="ghost" size="sm" onClick={switchToRaw}>
              <FileCode className="size-4" />
              Raw
            </Button>
          ) : (
            <Button variant="ghost" size="sm" onClick={switchToWysiwyg}>
              {fidelityFailed && !overrideAccepted ? "Try WYSIWYG anyway" : "Rich text"}
            </Button>
          )}
          <Button variant="ghost" size="sm" asChild>
            <a href={rawURL(path)} download>
              <Download className="size-4" />
              Download
            </a>
          </Button>
        </div>
      </div>

      {showBanner && (
        <div className="flex items-center gap-2 border-b border-warn-soft bg-warn-soft px-4 py-2 text-sm text-warn">
          <AlertTriangle className="size-4 shrink-0" />
          <span className="flex-1">{RAW_BANNER}</span>
          <Button variant="outline" size="sm" onClick={switchToWysiwyg}>
            Try WYSIWYG anyway
          </Button>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {mode === "wysiwyg" ? (
          <div className="mx-auto max-w-3xl px-6 py-8">
            <WysiwygEditor
              content={rawText}
              onDirty={markDirty}
              registerGetContent={registerGetContent}
            />
          </div>
        ) : (
          <textarea
            className="h-full w-full resize-none bg-background px-6 py-8 font-mono text-sm text-foreground outline-none"
            value={rawText}
            onChange={handleRawChange}
            spellCheck={false}
          />
        )}
      </div>
    </div>
  );
}

function StatusIndicator({
  state,
  errorMessage,
}: {
  state: SaveState;
  errorMessage: string | null;
}) {
  const label: Record<SaveState, string> = {
    saved: "Saved",
    saving: "Saving…",
    dirty: "Unsaved changes",
    error: errorMessage ? `Error: ${errorMessage}` : "Error saving",
    raw: "Raw mode",
  };
  return (
    <span
      className={cn(
        "text-xs",
        state === "error" ? "text-danger" : "text-muted-2",
      )}
    >
      {label[state]}
    </span>
  );
}
