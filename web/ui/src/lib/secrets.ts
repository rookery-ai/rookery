import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors web/api_secrets.go's DTOs. Values are never returned by the API —
// only names — so there is no client-side representation of a secret value
// anywhere, ever.

// Mirrors apiSecretName.
export type Secret = { name: string };

export function useSecrets() {
  return useQuery({
    queryKey: ["secrets"],
    queryFn: () => api.get<{ secrets: Secret[] }>("/api/v1/secrets"),
  });
}

export function useAddSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      api.post<{ ok: boolean }>("/api/v1/secrets", { name, value }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });
}

export function useDeleteSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, masterPassword }: { name: string; masterPassword: string }) =>
      api.del<{ ok: boolean }>(`/api/v1/secrets/${encodeURIComponent(name)}`, {
        master_password: masterPassword,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });
}
