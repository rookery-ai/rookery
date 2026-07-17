import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { AlertTriangle } from "lucide-react";
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

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

// User-authored skill: monospace textarea editor + Save (PUT) + Delete.
export default function SkillDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: skill } = useSkillDetail(id);
  const { save, del } = useSkillActions();

  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    if (skill) setDraft(skill.content);
  }, [skill]);

  if (!skill) return <div className="p-8 text-muted-2">Loading…</div>;

  const dirty = draft !== skill.content;

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      await save(id, draft);
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    setDeleting(true);
    try {
      await del(id);
      navigate("/skills");
    } catch (err) {
      setError(errMessage(err));
      setDeleting(false);
      setDeleteOpen(false);
    }
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-1 flex items-start justify-between gap-3">
        <h1 className="text-xl font-bold">{skill.name}</h1>
        <div className="flex shrink-0 items-center gap-2">
          <Button
            size="sm"
            aria-label="Save skill"
            onClick={() => void handleSave()}
            disabled={!dirty || saving}
          >
            Save
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="text-danger"
            onClick={() => setDeleteOpen(true)}
          >
            Delete
          </Button>
        </div>
      </div>

      {skill.description && (
        <p className="mb-4 max-w-2xl text-sm text-muted-2">{skill.description}</p>
      )}

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      <textarea
        aria-label="SKILL.md"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        className="min-h-[60vh] w-full resize-y rounded-md border border-border bg-background p-3 font-mono text-xs outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
      />

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
    </div>
  );
}

// Core (embedded) skill: read-only markdown render. No rehype-raw — mirrors
// ChatMessageBubble's safety config, raw HTML in SKILL.md renders as inert
// text rather than markup.
export function CoreSkillViewPage() {
  const { slug = "" } = useParams();
  const { data: skill } = useCoreSkill(slug);

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold">{slug}</h1>
          <p className="text-xs text-muted-2">Built-in skill — read only</p>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link to="/skills">Back to skills</Link>
        </Button>
      </div>

      {!skill ? (
        <div className="text-muted-2">Loading…</div>
      ) : (
        <div
          className={[
            "max-w-none rounded-lg border border-border bg-background p-6 text-sm leading-relaxed",
            "[&_p]:my-2 [&_pre]:my-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-chrome [&_pre]:p-3",
            "[&_code]:break-words [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5",
            "[&_strong]:font-semibold [&_a]:underline [&_a]:text-accent",
            "[&_h1]:mt-4 [&_h1]:text-lg [&_h1]:font-bold [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-bold",
          ].join(" ")}
        >
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              a: ({ node: _node, ...props }) => (
                <a {...props} target="_blank" rel="noreferrer noopener" />
              ),
            }}
          >
            {skill.content}
          </ReactMarkdown>
        </div>
      )}
    </div>
  );
}
