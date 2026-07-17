import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useChatDetail, useChatAction, sendChatMessage, type ChatMessage } from "@/lib/chats";
import { ChatScroll } from "@/components/chat/ChatScroll";
import { ChatMessageBubble, TypingIndicator } from "@/components/chat/Bubbles";
import { Composer } from "@/components/chat/Composer";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

function StatusChip({ active }: { active: boolean }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full px-2 py-0.5 text-xs font-medium",
        active ? "bg-ok-soft text-ok" : "bg-muted text-muted-2",
      )}
    >
      {active ? "Active" : "Stopped"}
    </span>
  );
}

// Container-agnostic: no page margins/max-width here — Task 3's slide-over
// mounts this directly, KBPage-style embedding would double up chrome.
export function ChatWindow({ chatId }: { chatId: string }) {
  const { data, isLoading } = useChatDetail(chatId);
  const qc = useQueryClient();
  const action = useChatAction();
  // Optimistic-append pattern: pending holds messages not yet reflected in
  // the query cache. Merged after the fetched history on render, cleared
  // once invalidateQueries confirms the server has them (its promise
  // resolves after the refetch completes for an active query).
  const [pending, setPending] = useState<ChatMessage[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);

  async function handleSend(text: string) {
    setError(null);
    setPending((p) => [...p, { role: "user", content: text }]);
    setBusy(true);
    try {
      const response = await sendChatMessage(chatId, text);
      setPending((p) => [...p, { role: "assistant", content: response }]);
      await qc.invalidateQueries({ queryKey: ["chat", chatId] });
      setPending([]);
    } catch (err) {
      // The user bubble already pushed above stays visible — the failure
      // is on the assistant's turn, not the user's message.
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  function handleDelete() {
    setDeleteOpen(false);
    action.mutate({ id: chatId, action: "delete" });
  }

  if (isLoading || !data) {
    return <div className="flex h-full items-center justify-center text-sm text-muted-2">Loading…</div>;
  }

  const { chat, messages } = data;
  const allMessages = [...messages, ...pending];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <h2 className="truncate text-sm font-bold">{chat.name}</h2>
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
          <ChatMessageBubble key={i} role={m.role} content={m.content} />
        ))}
        {busy && <TypingIndicator />}
      </ChatScroll>

      {error && (
        <div className="flex items-center gap-2 border-t border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      <Composer onSend={handleSend} busy={busy} />

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

export default ChatWindow;
