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
  /** True while the owner-password confirmation guarding install-level
   *  settings is still within its server-side TTL. */
  owner_verified?: boolean;
  // The active workspace's profile timezone (an IANA name, or "" when unset).
  // Free text server-side, so consumers must tolerate a bogus value — see
  // formatMessageTime.
  timezone?: string;
};

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api.get<Session>("/api/v1/auth/session"),
    staleTime: 30_000,
  });
}

// The zone chat timestamps render in. Undefined (no workspace, no configured
// timezone, or the session not loaded yet) means "use the browser's own zone",
// which is what formatMessageTime does with an absent/empty value.
export function useDisplayTimeZone(): string | undefined {
  return useSession().data?.timezone || undefined;
}
