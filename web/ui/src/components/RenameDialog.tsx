import { useState } from "react";
import { Pencil } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useToast } from "@/components/shell/Toast";
import { ApiError } from "@/lib/api";
import { renameNotice, type RenameResult } from "@/lib/rename";

export type RenameDialogProps = {
  /** "agent" or "skill": used in the dialog's words. */
  label: string;
  currentName: string;
  hint?: string;
  onClose: () => void;
  onRename: (name: string) => Promise<RenameResult>;
};

/**
 * Mount it only while open, so that a page which never renames does not need a toast
 * provider and each opening starts from the current name.
 */
export function RenameDialog({ label, currentName, hint, onClose, onRename }: RenameDialogProps) {
  const { toast } = useToast();
  const [name, setName] = useState(currentName);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const trimmed = name.trim();
  const canSubmit = trimmed !== "" && trimmed !== currentName && !saving;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setSaving(true);
    setError(null);
    try {
      const result = await onRename(trimmed);
      const notice = renameNotice(result);
      if (notice) {
        toast({
          message: notice,
          variant: result.warnings.length > 0 ? "error" : "default",
          durationMs: 10000,
        });
      }
      onClose();
    } catch (err) {
      // A refusal says what to do next, so it stays beside the field rather than in a
      // toast that disappears.
      setError(err instanceof ApiError ? err.message : "Something went wrong");
      setSaving(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-md">
        <form onSubmit={(e) => void handleSubmit(e)} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Rename {label}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="rename-name">New name</Label>
            <Input
              id="rename-name"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-invalid={error != null}
            />
            {hint && <p className="text-xs text-muted-2">{hint}</p>}
            <p className="text-xs text-muted-2">
              This changes the name only, not what the {label} does.
            </p>
            {error && (
              <p className="text-xs text-danger" role="alert">
                {error}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={saving}>
              Cancel
            </Button>
            <Button type="submit" disabled={!canSubmit}>
              <Pencil />
              Rename
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
