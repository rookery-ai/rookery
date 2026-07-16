import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type Workspace = {
  id: string;
  name: string;
  about: string;
  needs_setup: boolean;
  created_at: string;
};

export type Session = {
  authenticated: boolean;
  owner?: { id: string; username: string; must_change_password: boolean };
  workspace?: Workspace | null;
  workspaces?: Workspace[];
};

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api.get<Session>("/api/v1/auth/session"),
    staleTime: 30_000,
  });
}
