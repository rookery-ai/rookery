import { useState } from "react";
import { ArrowRight } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
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
import { api } from "@/lib/api";
import { useAgents } from "@/lib/agents";
import { cn } from "@/lib/utils";
import {
  AGENT_TEMPLATES,
  featuredTemplates,
  type AgentTemplate,
} from "./templates";
import TemplateGallery from "./TemplateGallery";

// Hoisted out of ENDPOINTS because DesignerEndpoints.state is OPTIONAL — the skill
// designer has no such endpoint — so reading it back off the annotated object gives
// `string | undefined` and will not typecheck as a query URL.
const DESIGN_STATE_URL = "/api/v1/agents/design/state";

const ENDPOINTS: DesignerEndpoints = {
  design: "/api/v1/agents/design",
  cancel: "/api/v1/agents/design/cancel",
  resume: "/api/v1/agents/design/resume",
  dismiss: "/api/v1/agents/design/dismiss",
  progress: "/api/v1/agents/design/progress",
  state: DESIGN_STATE_URL,
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Review"],
  buildButton: "Approve & build",
  saveButton: "Save agent",
  entityName: "agent",
};

// Derived from the template briefs so the examples shown here stay in the same
// non-technical register as the text a template drops into the field
// (templates.ts enforces that with a banned-jargon test).
//
// Only the OPENING SENTENCE, though: the briefs are now several sentences long
// (they pre-answer what/when/notify/which-service for the designer), and
// quoting three of them in full would bury the intro card. The examples exist
// to model how to phrase a request, not to be a complete brief.
function openingSentence(text: string): string {
  return text.split(/(?<=[.!?])\s/)[0] ?? text;
}

const INTRO_EXAMPLES = ["daily-digest", "watch-for-changes", "inbox-triage"]
  .map((id) => AGENT_TEMPLATES.find((t) => t.id === id)?.description ?? "")
  .filter(Boolean)
  .map(openingSentence);

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

  // The design session is a per-workspace SINGLETON: a build already running
  // in another tab is the same session this page would otherwise adopt.
  // DESIGN_STATE_URL is the exact response DesignerSurface itself polls once
  // mounted, but that's too late here — a fresh /agents/new load (no
  // ?resume=1, no local draft yet) reaches the name-gate form before
  // DesignerSurface ever mounts, so this page queries it independently to
  // decide whether to offer that form at all. `!resumeParam` lets an actual
  // resume (the "Open it" link below, or a draft's own Resume action)
  // through untouched — this notice exists only to stop a SECOND, unaware
  // attempt to start something new.
  const designState = useQuery({
    queryKey: ["agent-design-state"],
    queryFn: () => api.get<{ active: boolean; generating: boolean; name?: string }>(DESIGN_STATE_URL),
    // Matches the 5s interval DesignerSurface itself polls at while a build
    // is running (see the "Third completion signal" effect in
    // DesignerSurface.tsx) — this page needs the same freshness for the same
    // reason: without it, the notice below never clears once the other
    // tab's build finishes (refetchOnWindowFocus is off app-wide), and the
    // user is stuck reading stale state until they navigate away or reload.
    refetchInterval: 5000,
  });
  const buildInProgress = !resumeParam && designState.data?.generating === true;
  const buildingName = designState.data?.name;

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
  const [galleryOpen, setGalleryOpen] = useState(false);

  // The start screen shows only the promoted templates; the rest are reachable
  // through the gallery. The button is gated on there actually being more than
  // is already on screen, so it can never open an empty modal.
  const featured = featuredTemplates();
  const hasMoreTemplates = AGENT_TEMPLATES.length > featured.length;

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
      !window.confirm(
        `Replace what you've written with the "${template.label}" starting point?`,
      )
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

  // designState's FIRST fetch is folded in here too: without it, a fresh
  // /agents/new load renders the name-gate form for one round trip before
  // designState resolves and (if a build is running) swaps in the notice
  // below — a flash of exactly the form this page exists to stop showing.
  // isLoading is true only for that first fetch, never for the background
  // refetchInterval ticks above, so the recurring poll doesn't re-blank the
  // page.
  if (waitingForDraft || designState.isLoading) {
    return <div className="p-8 text-muted-2">Loading…</div>;
  }

  if (buildInProgress) {
    const notice = buildingName
      ? `“${buildingName}” is already building in another tab. Only one can run at a time.`
      : "An agent is already building in another tab. Only one can run at a time.";
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
        <p className="text-sm text-muted-2">{notice}</p>
        <Button onClick={() => navigate("/agents/new?resume=1")}>Open it</Button>
      </div>
    );
  }

  if (showNameGate) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4 overflow-y-auto p-8">
        <div className="w-full max-w-xl space-y-5">
          <div>
            <h1 className="text-lg font-bold">Create an agent</h1>
            <p className="text-sm text-muted-2">
              Name it, then start from a template or describe what you want
              yourself.
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
            <div className="flex items-baseline justify-between gap-3">
              <p className="text-sm font-medium">
                Start from a template{" "}
                <span className="font-normal text-muted-2">
                  (optional — fills the description below, which you can edit)
                </span>
              </p>
              {hasMoreTemplates && (
                <button
                  type="button"
                  onClick={() => setGalleryOpen(true)}
                  className="shrink-0 text-xs font-medium text-accent underline-offset-2 hover:underline"
                >
                  View all templates
                </button>
              )}
            </div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              {featured.map((t) => {
                const active = activeTemplateId === t.id;
                return (
                  <button
                    key={t.id}
                    type="button"
                    aria-pressed={active}
                    onClick={() => selectTemplate(t)}
                    className={cn(
                      "rounded-md border p-2.5 text-left text-xs transition-colors",
                      // Selected/hover are soft accent TINTS. A full `bg-accent`
                      // (or a 40% wash of it) left this card's own
                      // text-foreground title and text-muted-2 blurb unchanged
                      // on top of it — unreadable in light AND dark. The ring
                      // carries "selected"; the tint only warms the surface.
                      active
                        ? "border-ring bg-accent-soft ring-1 ring-ring"
                        : "border-border hover:border-accent/40 hover:bg-accent-soft",
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

          <Button
            onClick={confirmName}
            disabled={!name.trim()}
            className="w-full"
          >
            <ArrowRight />
            Continue
          </Button>
        </div>

        {/* Selecting from the gallery routes through the SAME selectTemplate()
            as the cards above, so the unsaved-custom-text guard applies. */}
        <TemplateGallery
          open={galleryOpen}
          onOpenChange={setGalleryOpen}
          onSelect={selectTemplate}
        />
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DesignerSurface
        endpoints={ENDPOINTS}
        labels={LABELS}
        startPayload={{ name: name.trim() }}
        // The description the user wrote (or a template they picked) is SENT as
        // the first message when they click Continue — the conversation starts
        // with their brief instead of dropping it into an unsent composer. When
        // the description is blank (e.g. "Start from scratch"), autoSendInitial
        // no-ops and the composer opens empty for them to type.
        initialText={description.trim() ? description : undefined}
        autoSendInitial
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
        gateBuildOnPlanReady
        onDone={(id) => navigate(id ? `/agents/${id}` : "/agents")}
      />
    </div>
  );
}
