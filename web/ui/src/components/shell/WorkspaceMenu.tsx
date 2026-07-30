import { useState } from "react";
import { Image, LogOut, Plus } from "lucide-react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { useSession, type Workspace } from "@/lib/session";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  EnterWorkspaceDialog, CreateWorkspaceDialog, resetWorkspaceScopedCache,
} from "@/pages/Workspaces";
import { WorkspaceAvatar } from "@/lib/workspaceIcons";
import WorkspaceIconPicker from "./WorkspaceIconPicker";

export default function WorkspaceMenu() {
  const { data: session } = useSession();
  const [entering, setEntering] = useState<Workspace | null>(null);
  const [creating, setCreating] = useState(false);
  const [pickingIcon, setPickingIcon] = useState(false);
  const [switchError, setSwitchError] = useState("");
  const nav = useNavigate();
  const qc = useQueryClient();
  const current = session?.workspace;

  async function leave() {
    await api.post("/api/v1/workspaces/leave");
    await resetWorkspaceScopedCache(qc);
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
          // size-11 matches the rail items below it (they were size-9 too, and
          // the whole column grew) and text-lg/font-extrabold makes the
          // fallback monogram carry at that size instead of floating in it.
          className="flex size-11 items-center justify-center rounded-lg text-lg font-extrabold transition-opacity hover:opacity-90 active:scale-95"
        >
          <WorkspaceAvatar
            name={current?.name}
            icon={current?.icon}
            className="size-11 text-lg"
          />
        </DropdownMenuTrigger>
        <DropdownMenuContent side="right" align="start" className="w-56">
          <DropdownMenuLabel className="truncate">{current?.name}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          {(session?.workspaces ?? [])
            .filter((w) => w.id !== current?.id)
            .map((w) => (
              <DropdownMenuItem key={w.id} onSelect={() => switchTo(w)}>
                {/* Each row shows its OWN image, so the switcher is scannable
                    by picture rather than by reading every name. */}
                <WorkspaceAvatar name={w.name} icon={w.icon} className="size-5 text-xs" />
                Switch to {w.name}
              </DropdownMenuItem>
            ))}
          <DropdownMenuItem onSelect={() => setPickingIcon(true)}>
            <Image /> Change image…
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setCreating(true)}>
            <Plus /> Create workspace
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={leave}>
            <LogOut /> Leave workspace
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {switchError && (
        <p className="absolute left-full top-0 ml-2 z-50 w-48 rounded-md border border-border bg-background px-2 py-1.5 text-xs text-danger shadow-sm">
          {switchError}
        </p>
      )}
      <WorkspaceIconPicker
        open={pickingIcon}
        onOpenChange={setPickingIcon}
        name={current?.name}
        current={current?.icon}
      />
      <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
      <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}
