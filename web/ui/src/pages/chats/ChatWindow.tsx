import { useEffect, useRef, useState, type DragEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, AlertTriangle, Paperclip } from "lucide-react";
import { cn, formatShortDate } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useChatDetail, useChatAction, useRenameChat, sendChatMessage, type Chat, type ChatMessage } from "@/lib/chats";
import { useUploadKBFile } from "@/lib/kb";
import { useToast } from "@/components/shell/Toast";
import { ChatScroll } from "@/components/chat/ChatScroll";
import { ChatMessageBubble, TypingIndicator } from "@/components/chat/Bubbles";
import { Composer } from "@/components/chat/Composer";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

// Mirrors web/api_kb.go's maxUploadBytes exactly — a client-side pre-check so
// an obviously-oversized file gets an immediate, friendly message instead of
// a round trip, but the SERVER remains the only authoritative guard (this
// constant can drift and nothing breaks; a stale/looser client value just
// means the 413 the server returns is the one the user actually sees).
const MAX_ATTACH_BYTES = 25 * 1024 * 1024;

// A single drag/pick gesture fires one real coder turn per file, serially —
// unbounded that's a footgun (a 40-file drop = 40 back-to-back turns off one
// gesture). Reject the whole batch up front with a clear message rather than
// silently truncating or firing them all.
const MAX_ATTACH_FILES = 10;

// attachErrorMessage turns an upload failure into the three distinct,
// readable outcomes the brief calls for: 413 (too large), 422 (unreadable
// format), anything else (generic). Kept separate from the general chat-send
// error handling in handleSend below because those are different failure
// domains with different causes a user can act on.
function attachErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 413) return "That file is too large (max 25 MB).";
    if (err.status === 422) return "We can't read this kind of file.";
    return err.message || "Something went wrong uploading that file.";
  }
  return "Something went wrong uploading that file.";
}

// Count-based reconciliation: consumes one fetched-message match per pending
// entry instead of an existence check. An existence-based filter
// (`fresh.some(fm => fm.role===m.role && fm.content===m.content)`) would drop
// BOTH copies of a legitimately-repeated pending message when only one of
// the two has actually landed server-side yet. Exported for direct unit
// testing (see reconcile.test.ts) — the scenario it guards is awkward to
// reproduce end-to-end.
export function reconcilePending(pending: ChatMessage[], freshMessages: ChatMessage[]): ChatMessage[] {
  const remaining = new Map<string, number>();
  for (const fm of freshMessages) {
    const key = `${fm.role}::${fm.content}`;
    remaining.set(key, (remaining.get(key) ?? 0) + 1);
  }
  return pending.filter((m) => {
    const key = `${m.role}::${m.content}`;
    const count = remaining.get(key) ?? 0;
    if (count > 0) {
      remaining.set(key, count - 1);
      return false; // this copy is accounted for in the fetched history
    }
    return true; // no unconsumed match — keep it pending
  });
}

function StatusChip({ active }: { active: boolean }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full px-2 py-0.5 text-xs font-medium",
        active ? "bg-ok-soft text-ok" : "bg-muted-surface text-foreground",
      )}
    >
      {active ? "Active" : "Stopped"}
    </span>
  );
}

// Container-agnostic: no page margins/max-width here — Task 3's slide-over
// mounts this directly, KBPage-style embedding would double up chrome.
// initialText: optional prefill forwarded to the Composer — the ⌘K palette's
// "Ask assistant" action passes the search query through GlobalChatPanel.
// autoFocus: put the caret in the composer as soon as it mounts. Opt-in rather
// than automatic because the two surfaces differ: ChatsPage passes it for every
// selection (opening a chat there IS "I want to type"), while GlobalChatPanel
// withholds it when the slide-over merely re-opens on the last conversation,
// which is not a typing gesture and would pop the on-screen keyboard on a touch
// device.
export function ChatWindow({
  chatId,
  initialText,
  autoFocus,
}: { chatId: string; initialText?: string; autoFocus?: boolean }) {
  const { data, isLoading } = useChatDetail(chatId);
  const qc = useQueryClient();
  const action = useChatAction();
  // Optimistic-append pattern: pending holds messages not yet reflected in
  // the query cache. Merged after the fetched history on render, filtered
  // out once invalidateQueries confirms the server has them (its promise
  // resolves after the refetch completes for an active query).
  const [pending, setPending] = useState<ChatMessage[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const upload = useUploadKBFile();
  const { toast } = useToast();
  const [attaching, setAttaching] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Opening a stopped chat resumes it — the "Stopped" chip is presentational
  // (handleChatMessage never checks chat.active before running a turn), so
  // making the user find and press Resume before typing was pure friction.
  //
  // The ref latches on the FIRST load of this mount — before the active check,
  // not after it. That ordering is what makes this once-per-OPEN rather than a
  // standing policy that the chat must be active: a chat opened active and then
  // Stopped by the user has already spent its decision, so Stop sticks instead
  // of being instantly undone. ChatWindow is keyed by chatId at every call
  // site, so the decision resets only when a DIFFERENT chat is opened.
  // GlobalChatPanel is unaffected — it only ever mounts this for a chat it
  // already filtered as active, or one it just created.
  const autoResumeDecidedRef = useRef(false);
  useEffect(() => {
    if (autoResumeDecidedRef.current || !data) return;
    autoResumeDecidedRef.current = true;
    if (data.chat.active) return;
    action.mutate({ id: chatId, action: "resume" });
    // `action` is a stable mutation object and the ref above is the real guard;
    // depending on its identity would only add a chance of a second fire.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, chatId]);

  // sendTurn is the shared low-level "post one message, wait for the
  // assistant's reply" primitive. It never throws — every caller gets a
  // typed result back and decides for itself how to surface a failure.
  // (handleSend below shows it in the shared banner; attachFiles shows a
  // per-file toast instead, precisely because a shared banner is the wrong
  // vehicle when a batch import can have several independent outcomes.)
  async function sendTurn(text: string): Promise<{ ok: true } | { ok: false; message: string }> {
    // Stamped client-side so a just-sent bubble shows its time immediately
    // instead of blank until the refetch lands. reconcilePending keys on
    // role::content only, so this never disturbs the dedupe.
    setPending((p) => [...p, { role: "user", content: text, created_at: new Date().toISOString() }]);
    setBusy(true);
    try {
      const response = await sendChatMessage(chatId, text);
      setPending((p) => [...p, { role: "assistant", content: response, created_at: new Date().toISOString() }]);
      await qc.invalidateQueries({ queryKey: ["chat", chatId] });
      // Also refresh the session list so its updated_at/ordering doesn't go
      // stale after a send (list is a separate query keyed by ["chats"]).
      qc.invalidateQueries({ queryKey: ["chats"] });
      // Dedupe rather than blindly clear: reconcile against the freshly-
      // fetched history instead of setPending([]) unconditionally — closes
      // a transient window where a blind clear could drop a message that
      // hadn't actually landed in the cache yet.
      const fresh = qc.getQueryData<{ chat: Chat; messages: ChatMessage[] }>(["chat", chatId]);
      setPending((p) => reconcilePending(p, fresh?.messages ?? []));
      return { ok: true };
    } catch (err) {
      // The user bubble already pushed above stays visible — the failure
      // is on the assistant's turn, not the user's message.
      return { ok: false, message: err instanceof ApiError ? err.message : "Something went wrong" };
    } finally {
      setBusy(false);
    }
  }

  async function handleSend(text: string) {
    setError(null);
    const result = await sendTurn(text);
    if (!result.ok) setError(result.message);
  }

  function handleDelete() {
    setDeleteOpen(false);
    action.mutate({ id: chatId, action: "delete" });
  }

  // attachFiles uploads each file to the shared KB-import endpoint (same
  // path the KB page's own attach button uses), one at a time — a deliberate
  // choice, not an oversight: uploads land on ImportFile's per-workspace
  // mutex anyway (see internal/vault's per-workspace import lock), and each
  // successful import posts its own confirmation as a REAL chat turn (see
  // below), so importing serially keeps those turns in file order instead of
  // racing.
  //
  // Design choice — WHY this posts through handleSend (a real coder turn)
  // instead of a client-only system line: the adapters (Telegram/Discord)
  // reply with a canned acknowledgement at the ROUTER level, never invoking
  // the coder. Web chat has no equivalent "router reply" surface — the only
  // way to persist a message into this chat's history (so it survives a
  // reload and the assistant can reference the note on a later turn) is the
  // existing send endpoint, which always runs the coder. Inventing a new
  // endpoint just to persist a non-coder system line was judged NOT worth it
  // per the brief's own steer to avoid new backend surface when reuse is
  // available — reuse here is the sendChatMessage path already wired below.
  // Each file's outcome is reported through its own toast rather than the
  // shared `error` banner: `error` is a single slot, so a batch like
  // [big.bin → 413, doc.txt → ok] would have doc.txt's success clear
  // big.bin's rejection before the user ever saw it (handleSend clears
  // `error` as its first statement on every send). Per-file toasts stack
  // independently and a later success can never erase an earlier failure.
  async function attachFiles(files: File[]) {
    if (files.length === 0) return;
    if (files.length > MAX_ATTACH_FILES) {
      toast({
        message: `Attach up to ${MAX_ATTACH_FILES} files at a time (you selected ${files.length}).`,
        variant: "error",
      });
      return;
    }
    setAttaching(true);
    try {
      for (const file of files) {
        if (file.size > MAX_ATTACH_BYTES) {
          toast({ message: `Couldn't attach ${file.name}: that file is too large (max 25 MB).`, variant: "error" });
          continue;
        }
        let res;
        try {
          res = await upload.mutateAsync({ file });
        } catch (err) {
          // A failed import must never post a success confirmation — this
          // catch is scoped to exactly the upload call above, so the
          // confirmation turn below is only ever reached on a successful
          // import.
          toast({ message: `Couldn't attach ${file.name}: ${attachErrorMessage(err)}`, variant: "error" });
          continue;
        }
        const warningNote = res.warnings?.length ? `\n\n_Note: ${res.warnings.join("; ")}_` : "";
        const confirmation = `📎 Attached **${file.name}** to my knowledge base as \`${res.note_path}\`.${warningNote}`;
        const sent = await sendTurn(confirmation);
        if (!sent.ok) {
          // The file IS in the KB at this point — the upload above already
          // succeeded. Only the confirmation TURN failed, so the message
          // must say so: never tell the user an import failed when it
          // didn't. This toast is the durable record of that import — the
          // optimistic "📎 Attached…" bubble above is session-only and
          // vanishes on reload since nothing was persisted to chat_messages.
          toast({
            message:
              `${file.name} was imported to your knowledge base as ${res.note_path}, ` +
              `but I couldn't notify the assistant (${sent.message}). The file is safely saved.`,
            variant: "error",
            durationMs: 10000,
          });
        }
      }
    } finally {
      setAttaching(false);
    }
  }

  function handleDragOver(e: DragEvent<HTMLDivElement>) {
    if (!e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    setDragOver(true);
  }

  function handleDragLeave(e: DragEvent<HTMLDivElement>) {
    // Ignore a dragleave fired when moving from this wrapper onto one of its
    // own children — only clear the highlight once the pointer truly leaves
    // the whole drop zone (mirrors the KB tree's own drag-highlight guard).
    if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
    setDragOver(false);
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    const files = Array.from(e.dataTransfer.files ?? []);
    if (files.length === 0) return;
    e.preventDefault();
    setDragOver(false);
    // An attach is already in flight — dropping again mid-batch would race a
    // second attachFiles against the first and break the sequential file
    // ordering the comment on attachFiles promises.
    if (attaching) return;
    void attachFiles(files);
  }

  if (isLoading || !data) {
    return <div className="flex h-full items-center justify-center text-sm text-muted-2">Loading…</div>;
  }

  const { chat, messages } = data;
  const allMessages = [...messages, ...pending];

  const attachControl = (
    <>
      <button
        type="button"
        aria-label="Attach file"
        disabled={busy || attaching}
        onClick={() => fileInputRef.current?.click()}
        className={cn(
          "shrink-0 rounded-lg p-2 text-muted-2 hover:bg-border hover:text-foreground",
          "disabled:cursor-not-allowed disabled:opacity-50",
        )}
      >
        <Paperclip className="size-4" />
      </button>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        disabled={busy || attaching}
        className="sr-only"
        aria-label="Attach file"
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          e.target.value = ""; // allow re-picking the same file(s) again
          void attachFiles(files);
        }}
      />
    </>
  );

  return (
    <div
      className={cn(
        "flex h-full min-h-0 flex-col",
        dragOver && "ring-2 ring-inset ring-ring",
      )}
      data-testid="chat-window"
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <div className="min-w-0">
            <ChatTitle chatId={chatId} name={chat.name} />
            <span className="text-xs text-muted-2">{formatShortDate(chat.created_at)}</span>
          </div>
          <StatusChip active={chat.active} />
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={action.isPending}
            onClick={() => action.mutate({ id: chatId, action: chat.active ? "stop" : "resume" })}
          >
            {chat.active ? "Stop" : "Resume"}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="Chat actions"
                className="shrink-0 rounded p-1 hover:bg-border"
              >
                <MoreHorizontal className="size-4" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem variant="destructive" onSelect={() => setDeleteOpen(true)}>
                Delete…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <ChatScroll>
        {allMessages.map((m, i) => (
          <ChatMessageBubble key={i} role={m.role} content={m.content} createdAt={m.created_at} />
        ))}
        {attaching && <TypingIndicator label="Attaching…" />}
        {busy && !attaching && <TypingIndicator />}
      </ChatScroll>

      {error && (
        <div className="flex items-center gap-2 border-t border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      <Composer
        onSend={handleSend}
        busy={busy || attaching}
        initialText={initialText}
        autoFocus={autoFocus}
        leftSlot={attachControl}
      />

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete “{chat.name}”?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">This can’t be undone.</p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)}>Cancel</Button>
            <Button variant="destructive" onClick={handleDelete}>Delete</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ChatTitle renders a chat's name as an inline-editable title: click (or Enter
// when focused) to edit, Enter/blur commits via useRenameChat, Escape reverts.
// A failed rename rolls the value back and toasts.
function ChatTitle({ chatId, name }: { chatId: string; name: string }) {
  const rename = useRenameChat();
  const { toast } = useToast();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(name);

  // Keep the field in sync when the chat switches or the server updates the name
  // (e.g. auto-title landing after the first message).
  useEffect(() => {
    setValue(name);
  }, [name]);

  function commit() {
    setEditing(false);
    const trimmed = value.trim();
    if (!trimmed || trimmed === name) {
      setValue(name);
      return;
    }
    rename.mutate(
      { id: chatId, name: trimmed },
      {
        onError: (err) => {
          setValue(name);
          toast({
            message: err instanceof ApiError ? err.message : "Couldn't rename chat",
            variant: "error",
          });
        },
      },
    );
  }

  if (editing) {
    return (
      <input
        autoFocus
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            e.currentTarget.blur();
          } else if (e.key === "Escape") {
            setValue(name);
            setEditing(false);
          }
        }}
        className="w-full truncate rounded border border-ring bg-transparent px-1 text-sm font-bold outline-none focus:ring-[3px] focus:ring-ring/50"
        aria-label="Chat title"
      />
    );
  }
  // Kept as an h2 (a heading, not a button) so the title stays in the a11y
  // heading tree — it's clickable to rename, with keyboard support via tabIndex.
  return (
    <h2
      tabIndex={0}
      title="Click to rename"
      onClick={() => setEditing(true)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          setEditing(true);
        }
      }}
      className="cursor-text truncate rounded px-1 text-sm font-bold hover:bg-chrome"
    >
      {name}
    </h2>
  );
}

export default ChatWindow;
