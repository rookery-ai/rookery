import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
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
  const nav = useNavigate();
  const qc = useQueryClient();
  const current = session?.workspace;

  async function leave() {
    await api.post("/api/v1/workspaces/leave");
    await qc.invalidateQueries({ queryKey: ["session"] });
    nav("/workspaces", { replace: true });
  }

  return (
    <>
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
              <DropdownMenuItem key={w.id} onSelect={() => setEntering(w)}>
                Switch to {w.name}
              </DropdownMenuItem>
            ))}
          <DropdownMenuItem onSelect={() => setCreating(true)}>+ Create workspace</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={leave}>Leave workspace</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
      <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
    </>
  );
}
