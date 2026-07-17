import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Sparkles, Plus, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, ApiError } from "@/lib/api";
import { timeAgo } from "@/lib/utils";
import { useSkills, useSkillActions, type SkillListItem, type CoreSkillListItem, type SkillDraft } from "@/lib/skills";

function SkillCard({ skill }: { skill: SkillListItem }) {
  return (
    <Link
      to={`/skills/${skill.id}`}
      className="flex flex-col gap-2 rounded-lg border border-border bg-background p-4 text-left transition-colors hover:border-primary/40 hover:shadow-sm"
    >
      <h3 className="font-semibold leading-tight">{skill.name}</h3>
      <p className="line-clamp-2 flex-1 text-sm leading-relaxed text-muted-2">
        {skill.description || <em className="text-muted-2/70">No description</em>}
      </p>
      <p className="text-xs text-muted-2/80">Added {timeAgo(skill.created_at)}</p>
    </Link>
  );
}

function CoreSkillCard({ skill }: { skill: CoreSkillListItem }) {
  return (
    <Link
      to={`/skills/core/${skill.slug}`}
      className="flex flex-col gap-2 rounded-lg border border-dashed border-border bg-chrome/50 p-4 text-left text-muted-2 transition-colors hover:border-primary/40"
    >
      <h3 className="font-semibold leading-tight text-foreground/80">{skill.name}</h3>
      <p className="line-clamp-2 flex-1 text-sm leading-relaxed">
        {skill.description || <em>No description</em>}
      </p>
      <p className="text-xs opacity-80">Built-in</p>
    </Link>
  );
}

function useDismissSkillDraft() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ status: string }>("/api/v1/skills/design/dismiss"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["skills"] }),
  });
}

function DraftBanner({ draft }: { draft: SkillDraft }) {
  const dismiss = useDismissSkillDraft();
  if (!draft) return null;
  return (
    <div className="mb-6 flex items-center justify-between gap-3 rounded-lg border border-dashed border-warn/50 bg-background p-4">
      <div>
        <p className="text-sm font-semibold">
          Unfinished draft{draft.skill_name ? `: ${draft.skill_name}` : ""}
        </p>
        <p className="text-xs text-muted-2">
          {draft.updated_at ? `Last edited ${timeAgo(draft.updated_at)}` : "In progress"}
        </p>
      </div>
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          className="text-danger"
          disabled={dismiss.isPending}
          onClick={() => dismiss.mutate()}
        >
          Discard
        </Button>
        <Button asChild size="sm">
          <Link to="/skills/new?resume=1">Resume</Link>
        </Button>
      </div>
    </div>
  );
}

function ImportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { create } = useSkillActions();
  const [content, setContent] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleImport() {
    setSaving(true);
    setError(null);
    try {
      await create(content);
      setContent("");
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Import a skill</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-2">
          Paste a SKILL.md file&rsquo;s contents (YAML frontmatter + body). ZIP upload isn&rsquo;t
          supported here yet — use the paste form for now.
        </p>
        <textarea
          aria-label="SKILL.md content"
          placeholder={"---\nname: my-skill\ndescription: What it does and when to use it.\n---\n\n..."}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          className="min-h-56 w-full resize-y rounded-md border border-border bg-background p-3 font-mono text-xs outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
        />
        {error && <p className="text-xs text-danger">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={() => void handleImport()} disabled={!content.trim() || saving}>
            Import
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EmptyState() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center text-muted-2">
      <Sparkles className="size-10" />
      <h2 className="text-lg font-bold text-foreground">No skills yet</h2>
      <p className="max-w-sm text-sm">
        Skills teach your agents new capabilities — create one by describing it in plain language.
      </p>
      <Button asChild>
        <Link to="/skills/new">
          <Plus /> Create your first skill
        </Link>
      </Button>
    </div>
  );
}

export default function SkillsPage() {
  const { data } = useSkills();
  const [importOpen, setImportOpen] = useState(false);

  const skills = data?.skills ?? [];
  const coreSkills = data?.core_skills ?? [];
  const draft = data?.draft ?? null;

  const showEmpty = skills.length === 0 && !draft;

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-bold">Skills</h1>
          {skills.length > 0 && (
            <p className="mt-0.5 text-sm text-muted-2">
              {skills.length} skill{skills.length > 1 ? "s" : ""} + {coreSkills.length} built-in
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => setImportOpen(true)}>
            <Upload /> Import
          </Button>
          <Button asChild>
            <Link to="/skills/new">
              <Plus /> New skill
            </Link>
          </Button>
        </div>
      </div>

      <DraftBanner draft={draft} />

      {showEmpty ? (
        <EmptyState />
      ) : (
        <>
          {skills.length > 0 && (
            <div className="mb-8">
              <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-2">
                Your skills
              </h2>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {skills.map((s) => (
                  <SkillCard key={s.id} skill={s} />
                ))}
              </div>
            </div>
          )}

          {coreSkills.length > 0 && (
            <div>
              <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-2">
                Core skills
              </h2>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {coreSkills.map((s) => (
                  <CoreSkillCard key={s.slug} skill={s} />
                ))}
              </div>
            </div>
          )}
        </>
      )}

      <ImportDialog open={importOpen} onOpenChange={setImportOpen} />
    </div>
  );
}
