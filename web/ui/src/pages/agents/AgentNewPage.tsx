import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import {
  DesignerSurface,
  type DesignerEndpoints,
  type DesignerLabels,
} from "@/components/designer/DesignerSurface";
import { DesignerIntro } from "@/components/designer/DesignerIntro";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAgents } from "@/lib/agents";
import { cn } from "@/lib/utils";
import { AGENT_TEMPLATES, type AgentTemplate } from "./templates";

const ENDPOINTS: DesignerEndpoints = {
  design: "/api/v1/agents/design",
  cancel: "/api/v1/agents/design/cancel",
  resume: "/api/v1/agents/design/resume",
  dismiss: "/api/v1/agents/design/dismiss",
  progress: "/api/v1/agents/design/progress",
  state: "/api/v1/agents/design/state",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Review"],
  buildButton: "🔨 Build it",
  saveButton: "✅ Save agent",
  entityName: "agent",
};

// Reused verbatim from the template briefs so the examples shown here and the
// text a template drops into the composer are the same non-technical register
// (templates.ts enforces that with a banned-jargon test).
const INTRO_EXAMPLES = ["daily-digest", "watch-for-changes", "inbox-triage"]
  .map((id) => AGENT_TEMPLATES.find((t) => t.id === id)?.description ?? "")
  .filter(Boolean);

// Conversational agent creation. A new agent needs a name before the design
// POST can start a session — that's collected here (once, up front) rather
// than inside DesignerSurface, which stays entity-agnostic for Task 8's
// skill-creator reuse.
export default function AgentNewPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const resumeParam = params.get("resume") === "1";
  const { data, isLoading } = useAgents();
  const draft = data?.draft ?? null;

  const [name, setName] = useState("");
  const [nameConfirmed, setNameConfirmed] = useState(false);
  const [description, setDescription] = useState("");
  // Tracks which template's text is currently sitting in the field, so the
  // card can stay visibly "on" after a pick (spec judgment call: the
  // selection is a real, persistent state, not a one-shot fill-and-forget).
  // Cleared as soon as the field's text diverges from that template's text —
  // by hand-editing OR by picking a different template — since at that point
  // it's the user's own brief again, not the template's.
  const [activeTemplateId, setActiveTemplateId] = useState<string | null>(null);

  // An existing draft (create OR edit-in-progress under this workspace)
  // means there's already a session to resume/discard — DesignerSurface's
  // own recovery banner handles that, so the name gate is skipped and it
  // takes over immediately. Likewise ?resume=1 goes straight there.
  const showNameGate = !resumeParam && !draft && !nameConfirmed;

  // DesignerSurface's mount-recovery effect (refetchState) reads `draft` only
  // ONCE, when the request to endpoints.state resolves to "no live session" —
  // it never re-checks a `draft` prop that arrives later. On a cold cache,
  // useAgents() hasn't settled yet, so `draft` is still null at that moment
  // and a direct load of ?resume=1 would silently skip the resume banner /
  // auto-resume. Hold off mounting DesignerSurface until the query settles so
  // it always sees the real draft (mirrors SkillNewPage's waitingForDraft).
  const waitingForDraft = resumeParam && isLoading;

  function confirmName() {
    if (name.trim()) setNameConfirmed(true);
  }

  // True when the field holds text a template pick would silently clobber:
  // either hand-typed prose (no active template) or a divergence from the
  // currently active template. Picking a template while this is true asks
  // for confirmation first — see selectTemplate.
  function hasUnsavedCustomText(): boolean {
    if (activeTemplateId === null) return description.trim() !== "";
    const active = AGENT_TEMPLATES.find((t) => t.id === activeTemplateId);
    return description !== (active?.description ?? "");
  }

  function selectTemplate(template: AgentTemplate) {
    if (
      hasUnsavedCustomText() &&
      !window.confirm(`Replace what you've written with the "${template.label}" starting point?`)
    ) {
      return;
    }
    setDescription(template.description);
    setActiveTemplateId(template.id);
  }

  function handleDescriptionChange(value: string) {
    setDescription(value);
    if (activeTemplateId !== null) {
      const active = AGENT_TEMPLATES.find((t) => t.id === activeTemplateId);
      if (value !== (active?.description ?? "")) setActiveTemplateId(null);
    }
  }

  if (waitingForDraft) {
    return <div className="p-8 text-muted-2">Loading…</div>;
  }

  if (showNameGate) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4 overflow-y-auto p-8">
        <div className="w-full max-w-xl space-y-5">
          <div>
            <h1 className="text-lg font-bold">Create an agent</h1>
            <p className="text-sm text-muted-2">
              Name it, then start from a template or describe what you want yourself.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="agent-name">Name</Label>
            <Input
              id="agent-name"
              autoFocus
              placeholder="e.g. Inbox Triager"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") confirmName();
              }}
            />
          </div>

          <div className="space-y-2">
            <p className="text-sm font-medium">
              Start from a template <span className="font-normal text-muted-2">(optional)</span>
            </p>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              {AGENT_TEMPLATES.map((t) => {
                const active = activeTemplateId === t.id;
                return (
                  <button
                    key={t.id}
                    type="button"
                    aria-pressed={active}
                    onClick={() => selectTemplate(t)}
                    className={cn(
                      "rounded-md border p-2.5 text-left text-xs transition-colors",
                      active
                        ? "border-ring bg-accent ring-1 ring-ring"
                        : "border-border hover:bg-accent/40",
                    )}
                  >
                    <div className="font-medium text-foreground">{t.label}</div>
                    <div className="mt-0.5 text-muted-2">{t.blurb}</div>
                  </button>
                );
              })}
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="agent-description">What should it do?</Label>
            <textarea
              id="agent-description"
              rows={4}
              placeholder="Describe what you want — the designer will ask follow-up questions."
              value={description}
              onChange={(e) => handleDescriptionChange(e.target.value)}
              className="min-h-24 w-full resize-y rounded-md border border-border bg-background p-3 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
            />
          </div>

          <Button onClick={confirmName} disabled={!name.trim()} className="w-full">
            Continue
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DesignerSurface
        endpoints={ENDPOINTS}
        labels={LABELS}
        startPayload={{ name: name.trim() }}
        // A picked/typed template brief pre-fills the first chat message —
        // still just a starting point the user can edit or clear before
        // sending, never auto-sent on their behalf.
        initialText={description.trim() ? description : undefined}
        intro={
          <DesignerIntro
            title="Tell me what you want this agent to do"
            blurb="Describe it in your own words — no need to be technical. I'll ask a few questions, then build it and test it for real before you save it."
            examples={INTRO_EXAMPLES}
          />
        }
        draft={draft ? { name: draft.agent_name } : null}
        autoResume={resumeParam}
        cancelTo="/agents"
        onDone={(id) => navigate(id ? `/agents/${id}` : "/agents")}
      />
    </div>
  );
}
