import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { useSession, type Workspace } from "@/lib/session";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { EnterWorkspaceDialog, CreateWorkspaceDialog } from "@/pages/Workspaces";

export default function WorkspaceMenu() {
  const { data: session } = useSession();
  const [entering, setEntering] = useState<Workspace | null>(null);
  const [creating, setCreating] = useState(false);
  const [switchError, setSwitchError] = useState("");
  const nav = useNavigate();
  const qc = useQueryClient();
  const current = session?.workspace;

  async function leave() {
    await api.post("/api/v1/workspaces/leave");
    await qc.invalidateQueries({ queryKey: ["session"] });
    nav("/workspaces", { replace: true });
  }

  // A needs_setup workspace has no master password yet — enter it directly
  // (mirrors Workspaces.tsx's picker) and land on the setup note. Any other
  // workspace always re-prompts for its master password.
  function switchTo(w: Workspace) {
    if (!w.needs_setup) {
      setEntering(w);
      return;
    }
    setSwitchError("");
    api
      .post(`/api/v1/workspaces/${w.id}/enter`, {})
      .then(() => {
        window.location.href = "/workspaces?setup=" + w.id;
      })
      .catch((err) => {
        setSwitchError(err instanceof ApiError ? err.message : "Something went wrong");
      });
  }

  return (
    <div className="relative">
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label="Workspace"
          className="size-9 rounded-lg bg-foreground text-background flex items-center justify-center font-bold"
        >
          {current?.name?.[0]?.toUpperCase() ?? "?"}
        </DropdownMenuTrigger>
        <DropdownMenuContent side="right" align="start" className="w-56">
          <DropdownMenuLabel className="truncate">{current?.name}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          {(session?.workspaces ?? [])
            .filter((w) => w.id !== current?.id)
            .map((w) => (
              <DropdownMenuItem key={w.id} onSelect={() => switchTo(w)}>
                Switch to {w.name}
              </DropdownMenuItem>
            ))}
          <DropdownMenuItem onSelect={() => setCreating(true)}>+ Create workspace</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={leave}>Leave workspace</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {switchError && (
        <p className="absolute left-full top-0 ml-2 z-50 w-48 rounded-md border border-border bg-background px-2 py-1.5 text-xs text-danger shadow-sm">
          {switchError}
        </p>
      )}
      <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
      <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}
