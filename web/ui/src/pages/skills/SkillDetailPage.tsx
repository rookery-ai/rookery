import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiError } from "@/lib/api";
import { useSkillDetail, useSkillActions, useCoreSkill } from "@/lib/skills";
import { SkillView } from "./SkillView";

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

// User-authored skill: the shared viewer, with the Raw tab editable and
// Save/Delete wired up.
export default function SkillDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: skill } = useSkillDetail(id);
  const { save, del } = useSkillActions();

  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function handleDelete() {
    setDeleting(true);
    try {
      await del(id);
      navigate("/skills");
    } catch (err) {
      setDeleteError(errMessage(err));
      setDeleting(false);
      setDeleteOpen(false);
    }
  }

  if (!skill) return <div className="p-8 text-muted-2">Loading…</div>;

  return (
    <>
      <SkillView
        kind="user"
        name={skill.name}
        description={skill.description}
        category={skill.category}
        version={skill.version}
        requires={skill.requires}
        content={skill.content}
        onSave={async (content) => {
          await save(id, content);
        }}
        onDelete={() => setDeleteOpen(true)}
      />

      {deleteError && (
        <p className="px-6 pb-4 text-xs text-danger" role="alert">
          {deleteError}
        </p>
      )}

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete &ldquo;{skill.name}&rdquo;?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">This can&rsquo;t be undone.</p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)} disabled={deleting}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void handleDelete()} disabled={deleting}>
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

// Core (embedded) skill: the same viewer, read-only. There is no file on disk to
// write to, so no Save/Delete and the Raw tab does not accept input.
export function CoreSkillViewPage() {
  const { slug = "" } = useParams();
  const { data: skill } = useCoreSkill(slug);

  if (!skill) return <div className="p-8 text-muted-2">Loading…</div>;

  return (
    <SkillView
      kind="core"
      name={slug}
      description={skill.description}
      category={skill.category}
      version={skill.version}
      requires={skill.requires}
      content={skill.content}
      actions={
        <Button asChild variant="outline" size="sm">
          <Link to="/skills">Back to skills</Link>
        </Button>
      }
    />
  );
}
