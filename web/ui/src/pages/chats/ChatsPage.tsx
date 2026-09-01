import { useEffect, useRef } from "react";
import { useSearchParams } from "react-router";
import { MessageSquare, MessageSquarePlus } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { ContextPaneHeader } from "@/components/shell/ContextPaneParts";
import { Button } from "@/components/ui/button";
import { cn, formatShortDate, timeAgo } from "@/lib/utils";
import { useChats, useCreateChat } from "@/lib/chats";
import { ChatWindow } from "./ChatWindow";
import { EXPLORE_INTRO } from "./introPrompt";

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

  // ?new=1 is how a caller says "start one", as distinct from "show me my
  // chats". Home's Start chat quick action is the only one that sets it: it
  // used to link to a bare /chats and land on the empty state, so the control
  // named Start chat started nothing.
  //
  // Keyed on the parameter rather than on "nothing is selected" because the
  // icon rail links to a bare /chats — creating there would hand a user
  // browsing their own history a fresh empty chat on every visit.
  //
  // The ref is not defensive: this effect depends on the mutation object,
  // which changes as the request settles, and the chat list refetches
  // underneath it — without the guard each of those mints another chat. Same
  // shape as ChatWindow's streamOpenRef.
  const creating = useRef(false);
  useEffect(() => {
    if (params.get("new") !== "1" || selected || creating.current) return;
    creating.current = true;
    void (async () => {
      try {
        const chat = await createChat.mutateAsync(undefined);
        // replace: so Back leaves Chats rather than re-entering a URL that
        // would create a second chat.
        setParams({ chat: chat.id }, { replace: true });
      } catch {
        // Drop ?new=1 so the empty state shows rather than a blank page
        // wearing a parameter that will never resolve. The list's own New
        // chat button is the retry.
        creating.current = false;
        setParams({}, { replace: true });
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, selected]);

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <ContextPaneHeader
            title="Chats"
            action={
              <Button
                variant="outline"
                size="sm"
                onClick={handleNew}
                disabled={createChat.isPending}
              >
                <MessageSquarePlus />
                New chat
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
                <span className="truncate font-medium">{c.name}</span>
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
        <ChatWindow
          chatId={selected}
          key={selected}
          autoFocus
          // ?intro=1 is how the setup wizard hands a brand-new owner a chat
          // that starts itself. ChatWindow only sends when the chat has no
          // history, so the parameter surviving in the URL is harmless.
          initialText={params.get("intro") === "1" ? EXPLORE_INTRO : undefined}
          autoSend={params.get("intro") === "1"}
        />
      ) : (
        <ChatsEmptyState />
      )}
    </>
  );
}
