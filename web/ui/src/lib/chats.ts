import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type Chat = {
  id: string;
  name: string;
  platform: string;
  active: boolean;
  created_at: string;
  updated_at: string;
};

export type ChatMessage = { role: "user" | "assistant"; content: string; created_at?: string };

export function useChats() {
  return useQuery({
    queryKey: ["chats"],
    queryFn: () => api.get<{ chats: Chat[] }>("/api/v1/chats"),
  });
}

// in_flight and turn_lines let a client mounting MID-turn re-attach: the turn
// outlives the request that started it, so the browser has to be able to ask
// "is one running, and what has it done so far?" rather than infer it. Both are
// optional so a response from an older server degrades to "no turn running"
// rather than throwing.
export type ChatDetail = {
  chat: Chat;
  messages: ChatMessage[];
  in_flight?: boolean;
  turn_lines?: string[];
};

export function useChatDetail(id: string | null) {
  return useQuery({
    queryKey: ["chat", id],
    queryFn: () => api.get<ChatDetail>(`/api/v1/chats/${id}`),
    enabled: !!id,
  });
}

export function useCreateChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name?: string) => api.post<Chat>("/api/v1/chats", name ? { name } : undefined),
    onSuccess: (chat) => {
      // Insert the created chat into the cached list synchronously so the
      // ChatsPage dead-selection guard sees it immediately and the window
      // opens on the first click. Without this, the list is briefly stale
      // (no new chat), the guard clears the selection, and the user has to
      // click the row after the refetch lands.
      qc.setQueryData<{ chats: Chat[] }>(["chats"], (old) =>
        old ? { ...old, chats: [chat, ...old.chats] } : { chats: [chat] },
      );
      qc.invalidateQueries({ queryKey: ["chats"] });
    },
  });
}

// startChatTurn STARTS a turn; it does not wait for the reply.
//
// The endpoint used to run the whole turn inline and answer {"response": …} —
// which meant the turn was bound to this request. Leaving the page destroyed
// the only copy of the user's message (it was persisted only after the coder
// returned) and closing the tab cancelled the coder outright. The turn now runs
// detached server-side, so this returns 202 with a turn id and the reply
// arrives by refetching the chat once the progress stream reports done.
//
// The legacy 200-plus-error-FIELD shape is gone with it: a refused turn is a
// real 409, which `api.post` already raises as an ApiError, so callers keep
// their single error path.
export async function startChatTurn(id: string, message: string): Promise<{ turn_id: string }> {
  return api.post<{ turn_id: string }>(`/api/v1/chats/${id}/messages`, { message });
}

// chatTurnProgressURL is the SSE endpoint carrying one turn's tool-call
// milestones. Defined beside the starter so the two cannot drift.
export function chatTurnProgressURL(id: string): string {
  return `/api/v1/chats/${id}/turn/progress`;
}

// Renames a chat (edits its title). Optimistically updates the cached list and
// detail so the new title shows immediately; the invalidate reconciles.
export function useRenameChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) =>
      api.patch<Chat>(`/api/v1/chats/${id}`, { name }),
    onSuccess: (chat) => {
      qc.setQueryData<{ chats: Chat[] }>(["chats"], (old) =>
        old ? { ...old, chats: old.chats.map((c) => (c.id === chat.id ? { ...c, name: chat.name } : c)) } : old,
      );
      qc.invalidateQueries({ queryKey: ["chats"] });
      qc.invalidateQueries({ queryKey: ["chat", chat.id] });
    },
  });
}

export function useChatAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: "resume" | "stop" | "delete" }) =>
      action === "delete"
        ? api.del<{ ok: boolean }>(`/api/v1/chats/${id}`)
        : api.post<{ ok: boolean }>(`/api/v1/chats/${id}/${action}`),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["chats"] });
      qc.invalidateQueries({ queryKey: ["chat", id] });
    },
  });
}
