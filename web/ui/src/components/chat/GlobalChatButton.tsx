import { useEffect, useRef } from "react";
import { Link, useLocation } from "react-router";
import { MessageSquarePlus } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
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
export function GlobalChatPanel({ initialText }: { initialText?: string } = {}) {
  const { close } = useSlideOver();
  const { data } = useChats();
  const createChat = useCreateChat();
  const attemptedCreate = useRef(false);

  const chats = data?.chats ?? [];
  const active = chats.filter((c) => c.active);
  const mostRecent = active.length
    ? active.reduce((a, b) => (new Date(b.updated_at).getTime() > new Date(a.updated_at).getTime() ? b : a))
    : undefined;

  useEffect(() => {
    if (!data || mostRecent || attemptedCreate.current || createChatInFlight) return;
    attemptedCreate.current = true;
    createChatInFlight = true;
    createChat.mutate(undefined, { onSettled: () => { createChatInFlight = false; } });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, mostRecent]);

  if (!mostRecent) {
    return <div className="flex h-full items-center justify-center text-sm text-muted-2">Loading…</div>;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1">
        <ChatWindow chatId={mostRecent.id} initialText={initialText} />
      </div>
      <div className="shrink-0 border-t border-border px-4 py-2 text-center">
        <Link
          to={`${CHATS_PATH}?chat=${mostRecent.id}`}
          onClick={close}
          className="text-xs font-medium text-accent hover:underline"
        >
          Open full page ↗
        </Link>
      </div>
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
      className="fixed bottom-20 md:bottom-6 right-4 md:right-6 z-30 flex size-12 items-center justify-center rounded-full bg-accent text-accent-foreground shadow-lg transition-colors hover:bg-accent/90"
    >
      <MessageSquarePlus className="size-5" />
    </button>
  );
}

export default GlobalChatButton;
