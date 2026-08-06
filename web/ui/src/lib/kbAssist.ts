import { useMutation } from "@tanstack/react-query";
import { api } from "@/lib/api";

export type KBAssistAction = "improve" | "proofread" | "explain" | "reformat";

export type KBAssistResponse = { action: KBAssistAction; result: string };

export function useKBAssist() {
  return useMutation({
    mutationFn: (input: { action: KBAssistAction; path: string; selection: string }) =>
      api.post<KBAssistResponse>("/api/v1/kb/assist", input),
  });
}
