import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type Workspace = {
  id: string;
  name: string;
  // Slug of a preset image (see lib/workspaceIcons). "" = show the initial.
  icon: string;
  about: string;
  needs_setup: boolean;
  created_at: string;
};

export type Session = {
  authenticated: boolean;
  owner?: { id: string; username: string; must_change_password: boolean };
  workspace?: Workspace | null;
  workspaces?: Workspace[];
  // Server-side screen lock. The workspace stays entered while locked; every
  // guarded API route answers 423 until the master password is re-entered.
  locked?: boolean;
};

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api.get<Session>("/api/v1/auth/session"),
    staleTime: 30_000,
  });
}
