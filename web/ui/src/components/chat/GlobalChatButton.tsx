import { useEffect, useRef } from "react";
import { Link, useLocation } from "react-router";
import { MessageSquarePlus } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
import { useChats, useCreateChat } from "@/lib/chats";
import { ChatWindow } from "@/pages/chats/ChatWindow";

const CHATS_PATH = "/chats";

// Renders inside the shell's slide-over. Picks the most recently updated
// ACTIVE chat; if there isn't one, kicks off a single create on mount (ref
// guard so React 18 StrictMode's double-invoke of the effect can't fire two
// creates) and waits for it to land in the (now-invalidated) chats list.
export function GlobalChatPanel() {
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
    if (!data || mostRecent || attemptedCreate.current) return;
    attemptedCreate.current = true;
    createChat.mutate(undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, mostRecent]);

  if (!mostRecent) {
    return <div className="flex h-full items-center justify-center text-sm text-muted-2">Loading…</div>;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1">
        <ChatWindow chatId={mostRecent.id} />
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
  const { open } = useSlideOver();
  const hidden = location.pathname === CHATS_PATH;

  function openPanel() {
    open(<GlobalChatPanel />, { title: "Chat" });
  }

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
