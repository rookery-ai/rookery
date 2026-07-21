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

// Overwriting an existing secret is the SAME endpoint as creating one — the
// underlying INSERT is an upsert (internal/db/repositories.go's
// `ON CONFLICT(workspace_id, name) DO UPDATE`), so there is no separate PUT to
// call. This exists as its own hook purely so the update dialog gets its own
// isPending/error state instead of sharing the add form's.
//
// No master password is required, matching create (and GitHub Actions
// secrets, which likewise don't re-auth to rotate a value). Deleting still
// does, because that one is destructive.
export function useUpdateSecret() {
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
