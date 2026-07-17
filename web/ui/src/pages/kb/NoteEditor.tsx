import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { useEditor, EditorContent } from "@tiptap/react";
import { AlertTriangle, Loader2, Link2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { useKBNote, useSaveNote, useRenameNote, useDeleteNote } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import { buildExtensions, toMarkdown, checkFidelity } from "./editor";
import { splitAlias } from "./wikilinks";
import { slashSuggestion } from "./SlashMenu";
import BubbleToolbar from "./BubbleToolbar";
import NoteHeader from "./NoteHeader";
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
  onNavigate,
  registerGetContent,
}: {
  content: string;
  onDirty: () => void;
  onNavigate: (target: string) => void;
  registerGetContent: (fn: () => string) => void;
}) {
  const editor = useEditor({
    // buildExtensions()'s `extra` param exists precisely so UI-only
    // extensions can be appended here without editor.ts knowing about them
    // — checkFidelity's headless round-trip (buildExtensions() with no
    // args) is unaffected by the slash-menu suggestion plugin.
    extensions: buildExtensions([slashSuggestion()]),
    content,
    onUpdate: () => onDirty(),
    // Click-to-navigate for wikilink pills: handled here via editorProps
    // rather than inside the Wikilink node itself (see wikilinks.ts's top
    // comment) — this is the "click handler lives in NoteEditor via editor
    // props" option the task brief called out.
    editorProps: {
      handleClickOn(_view, _pos, node) {
        if (node.type.name !== "wikilink") return false;
        onNavigate(splitAlias(node.attrs.target as string).target);
        return true;
      },
    },
  });

  useEffect(() => {
    if (!editor) return;
    registerGetContent(() => toMarkdown(editor));
  }, [editor, registerGetContent]);

  return (
    <>
      <BubbleToolbar editor={editor} />
      <EditorContent editor={editor} className="note-editor-content" />
    </>
  );
}

export default function NoteEditor({
  path,
  onStateChange,
}: {
  path: string;
  onStateChange?: (state: SaveState) => void;
}) {
  const { data, isLoading, isError, error } = useKBNote(path);
  const saveNote = useSaveNote();
  const renameNote = useRenameNote();
  const deleteNote = useDeleteNote();
  // KBPage already owns the "path" search param and keys NoteEditor by it
  // (key={path}), so a navigation from inside this component can just write
  // the same param directly — no prop needs threading down from KBPage for
  // this; setSearchParams here and setParams there touch the same router
  // state and KBPage remounts a fresh NoteEditor instance either way.
  const [, setSearchParams] = useSearchParams();

  const [mode, setMode] = useState<"wysiwyg" | "raw" | null>(null);
  const [fidelityFailed, setFidelityFailed] = useState(false);
  const [overrideAccepted, setOverrideAccepted] = useState(false);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [rawText, setRawText] = useState("");
  const [notFoundHint, setNotFoundHint] = useState<string | null>(null);
  const [renameError, setRenameError] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  // The note we had loaded successfully is now gone — most likely deleted
  // from the FileTree row while this editor was still open (SP3 final
  // review: the header's own delete disarms correctly, but a tree-delete of
  // the currently-open note bypassed that machinery entirely).
  const [vanished, setVanished] = useState(false);
  // Whatever unsaved text existed at the moment we disarmed — shown
  // read-only in the vanished notice so it isn't just silently dropped
  // (delta-review nicety).
  const [vanishedContent, setVanishedContent] = useState<string | null>(null);

  const initializedRef = useRef(false);
  const dirtyRef = useRef(false);
  const savingRef = useRef(false);
  const pendingReflushRef = useRef(false);
  const mountedRef = useRef(true);
  const timerRef = useRef<number | undefined>(undefined);
  const rawTextRef = useRef("");
  const getContentRef = useRef<() => string>(() => "");
  const modeRef = useRef<"wysiwyg" | "raw" | null>(null);
  // The currently in-flight save's completion, set by `flush()` (the
  // debounce/Ctrl+S path) and awaited by `flushForHandoff()` — without this,
  // a rename/delete that starts while a debounce PUT is already in flight
  // would fire a SECOND concurrent PUT, and the earlier one could land
  // server-side AFTER the rename's POST, resurrecting the old path (upsert)
  // (SP3 final review).
  const inFlightSaveRef = useRef<Promise<void> | null>(null);
  // Set right before a rename/delete mutation fires, checked by the
  // unmount-flush cleanup below. Without this, a dirty edit + rename/delete
  // races the debounce/unmount machinery: setSearchParams (on success)
  // remounts NoteEditor at the new path/away entirely, and the OLD
  // instance's unmount cleanup would fire an unmount-flush PUT to the OLD
  // path — an upsert, so it silently resurrects a file a delete just
  // removed, or writes stray content back to the path a rename just moved
  // away from (review fix — see task-6-report.md).
  const intentionallyInvalidatedRef = useRef(false);

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

  // The note loaded successfully once (initializedRef true) and the query
  // has now transitioned to error — but ONLY a 404 means it actually
  // disappeared (e.g. deleted via its FileTree row); any other error is a
  // TRANSIENT failure (a server restart mid-edit, a network blip — the note
  // query refetches after every successful autosave too, since the save
  // mutation invalidates it). Delta review: gating on bare `isError`
  // falsely fired "deleted elsewhere" on a transient error AND discarded
  // the edit — worse than doing nothing, since pre-fix a transient error at
  // least left dirtyRef/the debounce timer untouched for the unmount-flush
  // to eventually pick up. A first-load failure (initializedRef still
  // false) is unaffected either way and keeps showing the plain "Couldn't
  // load this note."
  useEffect(() => {
    if (!isError || !initializedRef.current || vanished) return;
    const isNotFound = error instanceof ApiError && error.status === 404;
    if (!isNotFound) return;
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
      timerRef.current = undefined;
    }
    if (dirtyRef.current) setVanishedContent(getContentRef.current());
    dirtyRef.current = false;
    intentionallyInvalidatedRef.current = true;
    setVanished(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isError, error]);

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
  //  - inFlightSaveRef mirrors that same in-flight window as an awaitable
  //    promise (settles on success OR failure, never rejects) so
  //    flushForHandoff (rename/delete) can wait for THIS save to finish
  //    before deciding whether it needs to fire its own — otherwise it
  //    would race a second concurrent PUT against this one.
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
    let resolveInFlight!: () => void;
    inFlightSaveRef.current = new Promise<void>((resolve) => {
      resolveInFlight = resolve;
    });
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
          inFlightSaveRef.current = null;
          resolveInFlight();
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
          inFlightSaveRef.current = null;
          resolveInFlight();
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
      if (dirtyRef.current && !intentionallyInvalidatedRef.current) {
        saveNote.mutate({ path, content: getContentRef.current() });
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  const registerGetContent = useCallback((fn: () => string) => {
    getContentRef.current = fn;
  }, []);

  // Auto-dismiss the "note not found" hint so a stale warning doesn't linger
  // once the user has moved on.
  useEffect(() => {
    if (!notFoundHint) return;
    const t = window.setTimeout(() => setNotFoundHint(null), 4000);
    return () => window.clearTimeout(t);
  }, [notFoundHint]);

  // Backlinks are already resolved vault paths (data.backlinks, from
  // vault.Backlinks — see web/api_kb.go) — navigate straight there, no
  // resolve round-trip needed.
  const navigateToPath = useCallback(
    (p: string) => {
      setNotFoundHint(null);
      setSearchParams({ path: p });
    },
    [setSearchParams],
  );

  // Wikilink pills carry raw [[target]] text, which needs the resolve
  // endpoint (internal/vault's LinkIndex, same lookup backlinks/rendering
  // use) to turn into an actual note path.
  const handleNavigate = useCallback(
    (target: string) => {
      setNotFoundHint(null);
      api
        .get<{ path: string }>(`/api/v1/kb/resolve?link=${encodeURIComponent(target)}`)
        .then(({ path: resolved }) => setSearchParams({ path: resolved }))
        .catch((err) => {
          if (err instanceof ApiError && err.status === 404) {
            setNotFoundHint(target);
          }
        });
    },
    [setSearchParams],
  );

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

  const handleToggleRaw = () => {
    if (modeRef.current === "wysiwyg") switchToRaw();
    else switchToWysiwyg();
  };

  // Used by rename right before it relocates the note: cancels the debounce
  // timer, waits out any save ALREADY in flight (see inFlightSaveRef above
  // — otherwise this would race a second concurrent PUT against it), then,
  // if still dirty, performs the PUT to the CURRENT (soon-to-be-old) path
  // directly — bypassing the debounce so the edit is committed to disk
  // before the rename moves that file. Returns whether it's now SAFE to
  // proceed (nothing was dirty, or the PUT succeeded) vs. must ABORT (the
  // PUT failed — proceeding would move stale content and silently drop the
  // edit, the last live data-loss path in this flow per re-review). Never
  // throws, so the caller never needs a try/catch either way.
  const flushForHandoff = useCallback(async (): Promise<boolean> => {
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
      timerRef.current = undefined;
    }
    if (inFlightSaveRef.current) {
      await inFlightSaveRef.current;
    }
    // dirtyRef may have just been cleared BY that in-flight save (its
    // content snapshot already covered everything up to this point) — only
    // fire our own PUT if there's still something it didn't cover.
    if (!dirtyRef.current) return true;
    savingRef.current = true;
    const content = getContentRef.current();
    report("saving");
    try {
      await saveNote.mutateAsync({ path, content });
      savingRef.current = false;
      if (getContentRef.current() === content) {
        dirtyRef.current = false;
        report(idleState());
      }
      // else: a newer edit landed mid-flight — leave dirtyRef true, but the
      // PUT itself succeeded, so it's still safe for the caller to proceed;
      // the newer edit will simply need saving again post-rename.
      return true;
    } catch (err) {
      savingRef.current = false;
      if (mountedRef.current) {
        setErrorMessage(err instanceof ApiError ? err.message : "Failed to save");
      }
      report("error");
      return false;
    }
  }, [path, saveNote, report, idleState]);

  const handleRename = async (to: string) => {
    setRenameError(null);
    setDeleteError(null);
    // Content must move WITH the file: flush any pending edit to the old
    // path first, then rename (review fix — previously a dirty edit could
    // be silently dropped, or resurrect the old path after the rename via
    // the unmount-flush effect below).
    const flushed = await flushForHandoff();
    if (!flushed) {
      // The pending edit couldn't be saved — ABORT the rename rather than
      // moving stale content (re-review ruling: proceeding here was the
      // last live data-loss path, since the "Save failed" signal would die
      // with this instance once the rename's navigation unmounted it).
      // dirtyRef is still true and intentionallyInvalidatedRef is still
      // false, so Ctrl+S or the next debounce tick retries normally.
      setRenameError("Couldn't save your latest edit — rename cancelled. Try again.");
      return;
    }
    intentionallyInvalidatedRef.current = true;
    renameNote.mutate(
      { from: path, to },
      {
        onSuccess: () => setSearchParams({ path: to }),
        onError: (err) => {
          // The rename didn't happen — this instance isn't going anywhere,
          // so restore normal unmount-flush behavior for future edits.
          intentionallyInvalidatedRef.current = false;
          setRenameError(err instanceof ApiError ? err.message : "Rename failed");
        },
      },
    );
  };

  const handleDelete = () => {
    setRenameError(null);
    setDeleteError(null);
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
      timerRef.current = undefined;
    }
    const hadPendingEdit = dirtyRef.current;
    // The user confirmed destruction — discard any pending edit rather than
    // persisting it; a PUT here (via debounce or unmount-flush) would
    // silently resurrect the note the delete is about to remove (review
    // fix). No PUT may fire in this sequence.
    dirtyRef.current = false;
    intentionallyInvalidatedRef.current = true;
    deleteNote.mutate(
      { path },
      {
        onSuccess: () => {
          const parent = path.split("/").slice(0, -1).join("/");
          setSearchParams(parent ? { path: parent } : {});
        },
        onError: (err) => {
          // The delete didn't happen — this instance stays mounted at the
          // same path, so restore normal unmount-flush behavior.
          intentionallyInvalidatedRef.current = false;
          // Re-arm: the edit we discarded above is still sitting unsaved in
          // the editor (the delete that would have removed it never
          // happened), so the dirty/"Unsaved" contract must reflect that
          // again instead of lying "saved" (re-review minor fix).
          if (hadPendingEdit) {
            dirtyRef.current = true;
            report("dirty");
          }
          setDeleteError(
            err instanceof ApiError ? `Delete failed: ${err.message}` : "Delete failed",
          );
        },
      },
    );
  };

  if (vanished) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center text-sm text-muted-2">
        <p>This note was deleted elsewhere.</p>
        {vanishedContent && (
          <pre className="max-h-64 w-full max-w-2xl overflow-auto rounded border border-border bg-chrome p-3 text-left text-xs whitespace-pre-wrap text-foreground">
            {vanishedContent}
          </pre>
        )}
      </div>
    );
  }

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
      <NoteHeader
        path={path}
        state={saveState}
        backlinksCount={data.backlinks.length}
        onRename={handleRename}
        onDelete={handleDelete}
        rawMode={mode === "raw"}
        onToggleRaw={handleToggleRaw}
        renameError={renameError}
      />

      {/* Suppressed while renameError is shown: flushForHandoff's onError
          sets errorMessage/saveState:"error" right before handleRename sets
          renameError and aborts — without this, a failed pre-rename flush
          rendered both the generic autosave banner and the rename-specific
          one at once. The rename-specific message is more actionable. */}
      {errorMessage && saveState === "error" && !renameError && (
        <div className="border-b border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
          {errorMessage}
        </div>
      )}

      {/* Shared banner slot for rename/delete failures — at most one of
          these is set at a time (both handlers clear the other on start). */}
      {(renameError || deleteError) && (
        <div className="flex items-center gap-2 border-b border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {renameError || deleteError}
        </div>
      )}

      {showBanner && (
        <div className="flex items-center gap-2 border-b border-warn-soft bg-warn-soft px-4 py-2 text-sm text-warn">
          <AlertTriangle className="size-4 shrink-0" />
          <span className="flex-1">{RAW_BANNER}</span>
          <Button variant="outline" size="sm" onClick={switchToWysiwyg}>
            Try WYSIWYG anyway
          </Button>
        </div>
      )}

      {notFoundHint && (
        <div className="flex items-center gap-2 border-b border-warn-soft bg-warn-soft px-4 py-2 text-sm text-warn">
          <AlertTriangle className="size-4 shrink-0" />
          <span>note not found: {notFoundHint}</span>
        </div>
      )}

      <BacklinksStrip backlinks={data.backlinks} onNavigate={navigateToPath} />

      <div className="min-h-0 flex-1 overflow-y-auto">
        {mode === "wysiwyg" ? (
          <div className="mx-auto max-w-3xl px-6 py-8">
            <WysiwygEditor
              content={rawText}
              onDirty={markDirty}
              onNavigate={handleNavigate}
              registerGetContent={registerGetContent}
            />
          </div>
        ) : (
          <textarea
            aria-label="Raw markdown"
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

// BacklinksStrip lists the notes that [[link]] to the one currently open
// (data.backlinks, already computed server-side by apiGetKBNote via
// vault.Backlinks — see web/api_kb.go). Hidden entirely when empty, per the
// task brief.
function BacklinksStrip({
  backlinks,
  onNavigate,
}: {
  backlinks: string[];
  onNavigate: (target: string) => void;
}) {
  if (backlinks.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-border px-4 py-2 text-xs">
      <Link2 className="size-3.5 shrink-0 text-muted-2" />
      <span className="mr-1 text-muted-2">Linked from:</span>
      {backlinks.map((p) => (
        <button
          key={p}
          type="button"
          className="wikilink-pill wikilink-pill-button"
          onClick={() => onNavigate(p)}
        >
          {p}
        </button>
      ))}
    </div>
  );
}

