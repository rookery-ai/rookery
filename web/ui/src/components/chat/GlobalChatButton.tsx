import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { MessageSquarePlus, Plus } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
import { Button } from "@/components/ui/button";
import { useChats, useCreateChat } from "@/lib/chats";
import { ChatWindow } from "@/pages/chats/ChatWindow";

const CHATS_PATH = "/chats";

// Module-level (not component-level) in-flight guard: GlobalChatPanel is
// unmounted whenever the slide-over closes (panel?.node in AppShell goes
// away), so a plain useRef resets on every close/reopen. If the create POST
// from a first open hasn't resolved yet, closing and reopening before then
// would otherwise mount a fresh instance that sees no active chat and fires
// a second create. This flag survives across that unmount/remount (it's
// process-lifetime, reset once the in-flight request settles either way).
let createChatInFlight = false;

// Renders inside the shell's slide-over. Picks the most recently updated
// ACTIVE chat; if there isn't one, kicks off a single create on mount (ref
// guard so React 18 StrictMode's double-invoke of the effect can't fire two
// creates within the same mount; the module-level flag above covers the
// close/reopen case across mounts) and waits for it to land in the
// (now-invalidated) chats list.
//
// `forceNew` overrides that resume-most-recent default: the caller wants a
// FRESH conversation, not whatever was last open. That is what "Chat about
// this file" needs — dropping a question about a note into an unrelated
// ongoing thread is worse than having no button at all.
// `autoSend` forwards ChatWindow's own once-per-mount, once-per-chat send.
// The KB editor's "Edit with AI" uses it: parking a quoted passage in the
// composer and waiting made the user retype the request they had already
// expressed by selecting the text and clicking the button.
export function GlobalChatPanel({
  initialText,
  forceNew,
  autoSend,
}: { initialText?: string; forceNew?: boolean; autoSend?: boolean } = {}) {
  const { close } = useSlideOver();
  const { data } = useChats();
  const createChat = useCreateChat();
  const attemptedCreate = useRef(false);
  // A chat this panel deliberately created (the New chat button, or forceNew).
  // Pinned rather than re-derived from "most recently updated": the list query
  // may not have refetched yet, and a brand-new chat has no messages, so
  // "most recent" can still resolve to the OLD one for a beat — the panel
  // would visibly snap back to the previous conversation.
  const [pinnedId, setPinnedId] = useState<string | null>(null);

  const chats = data?.chats ?? [];
  const active = chats.filter((c) => c.active);
  const mostRecent = active.length
    ? active.reduce((a, b) => (new Date(b.updated_at).getTime() > new Date(a.updated_at).getTime() ? b : a))
    : undefined;

  useEffect(() => {
    // forceNew creates unconditionally (there may well be an active chat — we
    // just don't want it); the default path only creates when there is none.
    if (!data || pinnedId || attemptedCreate.current) return;
    if (!forceNew && (mostRecent || createChatInFlight)) return;
    attemptedCreate.current = true;
    createChatInFlight = true;
    createChat.mutate(undefined, {
      onSuccess: (chat) => setPinnedId(chat.id),
      onSettled: () => { createChatInFlight = false; },
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, mostRecent, forceNew, pinnedId]);

  // The explicit New chat button. Not guarded by createChatInFlight (that flag
  // exists to stop the AUTOMATIC create from firing twice across a
  // close/reopen); this is a user gesture, and the mutation's own isPending
  // already disables the control while one is in flight.
  function startNewChat() {
    if (createChat.isPending) return;
    createChat.mutate(undefined, { onSuccess: (chat) => setPinnedId(chat.id) });
  }

  const shownId = pinnedId ?? mostRecent?.id ?? null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center justify-end border-b border-border px-4 py-1.5">
        <Button variant="ghost" size="xs" onClick={startNewChat} disabled={createChat.isPending}>
          <Plus /> New chat
        </Button>
      </div>
      <div className="min-h-0 flex-1">
        {shownId ? (
          // key: remounting on a chat switch resets the composer and the
          // scroll position. Without it, the new conversation would inherit
          // the previous one's half-typed draft.
          // autoFocus only for a chat this panel deliberately created (the
          // New chat button, or forceNew) — resuming the last conversation on
          // a plain panel open is not a "type now" gesture.
          <ChatWindow
            key={shownId}
            chatId={shownId}
            initialText={initialText}
            autoSend={autoSend}
            autoFocus={shownId === pinnedId}
            compact
          />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted-2">Loading…</div>
        )}
      </div>
      {shownId && (
        <div className="shrink-0 border-t border-border px-4 py-2 text-center">
          <Link
            to={`${CHATS_PATH}?chat=${shownId}`}
            onClick={close}
            className="text-xs font-medium text-accent hover:underline"
          >
            Open full page ↗
          </Link>
        </div>
      )}
    </div>
  );
}

export function GlobalChatButton() {
  const location = useLocation();
  const { open, close } = useSlideOver();
  const hidden = location.pathname === CHATS_PATH;

  function openPanel() {
    open(<GlobalChatPanel />, { title: "Chat" });
  }

  // The panel's own "Open full page" link closes itself before navigating,
  // but /chats can also be reached other ways (browser back/forward,
  // address bar, a bookmark) that bypass that handler — without this, the
  // global panel and the full ChatsPage could end up stacked. close() on an
  // already-closed panel is a no-op (setPanel(null) when already null).
  useEffect(() => {
    if (hidden) close();
  }, [hidden, close]);

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (hidden) return;
      const isShortcut = (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "j";
      if (!isShortcut) return;
      const target = e.target as HTMLElement | null;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)) return;
      e.preventDefault();
      openPanel();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hidden]);

  if (hidden) return null;

  return (
    <button
      type="button"
      aria-label="Open chat"
      onClick={openPanel}
      // Positioning lives in AppShell's FAB stack, not here — the stack also
      // holds the search button, and this component returns null on /chats,
      // which must collapse the stack rather than leave a gap.
      className="flex size-12 items-center justify-center rounded-full bg-accent text-accent-foreground shadow-lg transition-colors hover:bg-accent/90"
    >
      <MessageSquarePlus className="size-5" />
    </button>
  );
}

export default GlobalChatButton;
