import { useEffect } from "react";
import { useSearchParams } from "react-router";
import { MessageSquare } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { ContextPaneHeader } from "@/components/shell/ContextPaneParts";
import { Button } from "@/components/ui/button";
import { cn, formatShortDate, timeAgo } from "@/lib/utils";
import { useChats, useCreateChat } from "@/lib/chats";
import { ChatWindow } from "./ChatWindow";

function ChatsEmptyState() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center text-muted-2">
      <MessageSquare className="size-8" />
      <p className="text-sm">Select a chat or start a new one.</p>
    </div>
  );
}

export default function ChatsPage() {
  const [params, setParams] = useSearchParams();
  const selected = params.get("chat");
  const { data } = useChats();
  const createChat = useCreateChat();
  const chats = data?.chats ?? [];

  // Once the chat list has loaded, if the selected id isn't in it anymore
  // (deleted — either from this window's own Delete action, or elsewhere),
  // drop the search param so the empty state shows instead of a dead window.
  useEffect(() => {
    if (selected && data && !chats.some((c) => c.id === selected)) {
      setParams({}, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected, data]);

  async function handleNew() {
    const chat = await createChat.mutateAsync(undefined);
    setParams({ chat: chat.id });
  }

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <ContextPaneHeader
            title="Chats"
            action={
              <Button variant="outline" size="sm" onClick={handleNew} disabled={createChat.isPending}>
                + New chat
              </Button>
            }
          />
          <div className="min-h-0 flex-1 overflow-y-auto">
            {chats.map((c) => (
              <button
                key={c.id}
                type="button"
                onClick={() => setParams({ chat: c.id })}
                className={cn(
                  "flex w-full flex-col gap-0.5 border-b border-border/50 px-3 py-2 text-left text-sm",
                  selected === c.id ? "bg-border" : "hover:bg-chrome",
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate font-medium">{c.name}</span>
                  <span
                    className={cn(
                      "shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-medium",
                      c.active ? "bg-ok-soft text-ok" : "bg-muted-surface text-foreground",
                    )}
                  >
                    {c.active ? "Active" : "Stopped"}
                  </span>
                </div>
                <span className="text-xs text-muted-2">
                  {formatShortDate(c.created_at)} · {timeAgo(c.updated_at)}
                </span>
              </button>
            ))}
          </div>
        </div>
      </ContextPane>
      {selected ? (
        // key={selected}: ChatWindow remounts per chat, which is what lets the
        // mount-time autoFocus fire — and what scopes ChatWindow's
        // once-per-open auto-resume to a single chat.
        //
        // autoFocus is unconditional here: on this page every way of arriving
        // at a chat (clicking the list, a ⌘K search hit, a deep link) means the
        // user came to type. An earlier version withheld it while merely
        // browsing history; that distinction was dropped deliberately.
        <ChatWindow chatId={selected} key={selected} autoFocus />
      ) : (
        <ChatsEmptyState />
      )}
    </>
  );
}
