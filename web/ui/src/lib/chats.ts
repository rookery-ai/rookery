import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./api";

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

export function useChatDetail(id: string | null) {
  return useQuery({
    queryKey: ["chat", id],
    queryFn: () => api.get<{ chat: Chat; messages: ChatMessage[] }>(`/api/v1/chats/${id}`),
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

// sendChatMessage is the ONLY place that parses the legacy chat-message
// response shape: POST /api/v1/chats/:id/messages returns HTTP 200 with
// EITHER {"response": string} OR {"error": string} — a coder failure is a
// 200 + error FIELD, not a non-2xx status. Normalize that into the same
// ApiError contract every other failure uses so callers only need one
// error-handling path.
export async function sendChatMessage(id: string, message: string): Promise<string> {
  const body = await api.post<{ response?: string; error?: string }>(
    `/api/v1/chats/${id}/messages`,
    { message },
  );
  if (body.error) {
    throw new ApiError(200, "chat_error", body.error);
  }
  return body.response ?? "";
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
