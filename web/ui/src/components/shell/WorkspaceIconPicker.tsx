import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Save } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";
import { WORKSPACE_ICONS, WorkspaceAvatar } from "@/lib/workspaceIcons";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// A grid of the bundled workspace images plus a "no image" escape back to the
// initial-letter monogram. Saving invalidates the session query, which is what
// every avatar on screen reads from — so the rail trigger updates without a
// reload and without this component knowing where else the icon is rendered.
export default function WorkspaceIconPicker({
  open,
  onOpenChange,
  name,
  current,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  name?: string;
  current?: string;
}) {
  const qc = useQueryClient();
  // Local until Save: clicking a tile previews the choice rather than firing a
  // write per click, so browsing the set costs one request, not twelve.
  const [picked, setPicked] = useState<string>(current ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  // Re-seed whenever the dialog opens so a cancelled preview doesn't persist
  // into the next open.
  function handleOpenChange(next: boolean) {
    if (next) {
      setPicked(current ?? "");
      setError("");
    }
    onOpenChange(next);
  }

  async function save() {
    setSaving(true);
    setError("");
    try {
      await api.put("/api/v1/settings/workspace/icon", { icon: picked });
      await qc.invalidateQueries({ queryKey: ["session"] });
      onOpenChange(false);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Couldn't save the workspace image",
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Workspace image</DialogTitle>
        </DialogHeader>

        {/* Scrollable: 28 presets plus the clear tile no longer fit a fixed
            dialog, and a dialog that grows past the viewport puts its footer
            out of reach. */}
        <div className="grid max-h-[55vh] grid-cols-5 gap-2 overflow-y-auto pr-1">
          {/* The clear option first: it is the state every workspace starts in,
              so it belongs where the eye lands, not appended after 28 tiles. */}
          <button
            type="button"
            aria-label="No image"
            aria-pressed={picked === ""}
            onClick={() => setPicked("")}
            className={cn(
              "flex aspect-square items-center justify-center rounded-lg border transition-colors",
              picked === ""
                ? "border-ring bg-accent-soft ring-1 ring-ring"
                : "border-border hover:border-accent/40 hover:bg-accent-soft",
            )}
          >
            <WorkspaceAvatar name={name} className="size-9 text-base" />
          </button>

          {WORKSPACE_ICONS.map((ic) => {
            const active = picked === ic.slug;
            return (
              <button
                key={ic.slug}
                type="button"
                aria-label={ic.label}
                aria-pressed={active}
                onClick={() => setPicked(ic.slug)}
                className={cn(
                  "relative flex aspect-square items-center justify-center rounded-lg border transition-colors",
                  active
                    ? "border-ring bg-accent-soft ring-1 ring-ring"
                    : "border-border hover:border-accent/40 hover:bg-accent-soft",
                )}
              >
                <WorkspaceAvatar icon={ic.slug} className="size-9" />
                {active && (
                  <span className="absolute right-1 top-1 rounded-full bg-accent p-0.5 text-accent-foreground">
                    <Check className="size-2.5" />
                  </span>
                )}
              </button>
            );
          })}
        </div>

        {error && <p className="text-sm text-danger">{error}</p>}

        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={save} disabled={saving}>
            <Save />
            {saving ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
