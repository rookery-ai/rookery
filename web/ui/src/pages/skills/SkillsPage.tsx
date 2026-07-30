import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Sparkles, Plus, Search, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, ApiError } from "@/lib/api";
import { timeAgo } from "@/lib/utils";
import { SkillChip } from "./SkillView";
import { useSkills, useSkillActions, type SkillListItem, type CoreSkillListItem, type SkillDraft } from "@/lib/skills";

// One card for both kinds. Built-in skills used to render dashed, muted and
// half-contrast, which read as second-class — they are in fact the skills every
// workspace actually has. The only difference now is the chip.
function SkillCard({
  to,
  name,
  description,
  category,
  version,
  footer,
  badge,
}: {
  to: string;
  name: string;
  description: string;
  category: string;
  version: string;
  footer: string;
  badge: "Built-in" | "Yours";
}) {
  return (
    <Link
      to={to}
      className="flex flex-col gap-2 rounded-xl border border-border bg-background p-4 text-left transition-colors hover:border-primary/40 hover:shadow-sm"
    >
      <h3 className="font-semibold leading-tight">{name}</h3>
      <div className="flex flex-wrap items-center gap-1.5">
        <SkillChip>{category}</SkillChip>
        {version && <SkillChip>v{version}</SkillChip>}
        <SkillChip>{badge}</SkillChip>
      </div>
      <p className="line-clamp-2 flex-1 text-sm leading-relaxed text-muted-2">
        {description || <em className="text-muted-2/70">No description</em>}
      </p>
      <p className="text-xs text-muted-2/80">{footer}</p>
    </Link>
  );
}

function UserSkillCard({ skill }: { skill: SkillListItem }) {
  return (
    <SkillCard
      to={`/skills/${skill.id}`}
      name={skill.name}
      description={skill.description}
      category={skill.category}
      version={skill.version}
      footer={`Added ${timeAgo(skill.created_at)}`}
      badge="Yours"
    />
  );
}

function CoreSkillCard({ skill }: { skill: CoreSkillListItem }) {
  return (
    <SkillCard
      to={`/skills/core/${skill.slug}`}
      name={skill.name}
      description={skill.description}
      category={skill.category}
      version={skill.version}
      footer="Always available to every agent"
      badge="Built-in"
    />
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
    <div className="mb-6 flex items-center justify-between gap-3 rounded-xl border border-dashed border-warn/50 bg-background p-4">
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

// Shown in place of the "Your skills" grid only. It must NOT gate the whole
// page body: the core skills below are always available (every workspace gets
// them, embedded in the binary) and previously stayed invisible on a fresh
// workspace until the user happened to start a draft.
function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border p-8 text-center text-muted-2">
      <Sparkles className="size-10" />
      <h2 className="text-lg font-bold text-foreground">No skills of your own yet</h2>
      <p className="max-w-sm text-sm">
        Skills teach your agents new capabilities — create one by describing it in plain language.
        The built-in skills below are always available.
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
  const { data, isLoading } = useSkills();
  const [importOpen, setImportOpen] = useState(false);
  const [query, setQuery] = useState("");

  const skills = data?.skills ?? [];
  const coreSkills = data?.core_skills ?? [];
  const draft = data?.draft ?? null;

  // Same shape as SecretsPage's filter: a trimmed, lower-cased substring test,
  // here across name AND description since a skill's description is the part
  // that says when it applies.
  const q = query.trim().toLowerCase();
  const filteredSkills = useMemo(
    () => (q ? skills.filter((s) => `${s.name} ${s.description}`.toLowerCase().includes(q)) : skills),
    [skills, q],
  );
  const filteredCore = useMemo(
    () => (q ? coreSkills.filter((s) => `${s.name} ${s.description}`.toLowerCase().includes(q)) : coreSkills),
    [coreSkills, q],
  );

  // The "create your first skill" state belongs to the Your-skills section
  // alone, and only when nothing is being searched for — a search that simply
  // matches no user skill is not an empty workspace. `!isLoading` matters too:
  // an unresolved query looks identical to an empty one, so without it the
  // empty state flashes on every visit before the list lands.
  const showEmpty = !isLoading && !q && skills.length === 0 && !draft;
  const noMatches = !!q && filteredSkills.length === 0 && filteredCore.length === 0;
  const total = skills.length + coreSkills.length;

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-bold">Skills</h1>
          {total > 0 && (
            <p className="mt-0.5 text-sm text-muted-2">
              {skills.length > 0 && (
                <>
                  {skills.length} skill{skills.length > 1 ? "s" : ""} +{" "}
                </>
              )}
              {coreSkills.length} built-in
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {total > 0 && (
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-2" />
              <Input
                aria-label="Search skills"
                placeholder="Search skills…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                className="w-56 pl-8"
              />
            </div>
          )}
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

      {noMatches && (
        <p className="p-8 text-center text-sm text-muted-2">No skills match “{query.trim()}”.</p>
      )}

      {showEmpty ? (
        <div className="mb-8">
          <EmptyState />
        </div>
      ) : (
        filteredSkills.length > 0 && (
          <div className="mb-8">
            <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-2">
              Your skills
            </h2>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {filteredSkills.map((s) => (
                <UserSkillCard key={s.id} skill={s} />
              ))}
            </div>
          </div>
        )
      )}

      {filteredCore.length > 0 && (
        <div>
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-2">
            Core skills
          </h2>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {filteredCore.map((s) => (
              <CoreSkillCard key={s.slug} skill={s} />
            ))}
          </div>
        </div>
      )}

      <ImportDialog open={importOpen} onOpenChange={setImportOpen} />
    </div>
  );
}
