import { useState } from "react";
import { ArrowRight } from "lucide-react";
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
import { useSkills } from "@/lib/skills";

// No `state` endpoint — the skill designer has no mount-recovery route
// (handlers_skill_design.go registers design/cancel/resume/dismiss/progress
// only). DesignerSurface treats a missing `state` as "skip recovery" (its
// `recovering` flag inits to `!!endpoints.state`), so this never issues a GET
// to a state URL.
const ENDPOINTS: DesignerEndpoints = {
  design: "/api/v1/skills/design",
  cancel: "/api/v1/skills/design/cancel",
  resume: "/api/v1/skills/design/resume",
  dismiss: "/api/v1/skills/design/dismiss",
  progress: "/api/v1/skills/design/progress",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Design", "Build", "Vet & Review"],
  buildButton: "Build it",
  saveButton: "Save skill",
  entityName: "skill",
};

// Conversational skill creation, mirroring AgentNewPage's shape. A new skill
// needs a name up front (handleSkillDesignChat rejects a session start
// without one) — collected here, not inside the entity-agnostic
// DesignerSurface.
export default function SkillNewPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const resumeParam = params.get("resume") === "1";
  const { data, isLoading } = useSkills();
  const draft = data?.draft ?? null;

  const [name, setName] = useState("");
  const [nameConfirmed, setNameConfirmed] = useState(false);

  const showNameGate = !resumeParam && !draft && !nameConfirmed;
  // DesignerSurface decides whether to show its resume banner / auto-resume
  // ONCE, on its own mount effect — it never re-checks a `draft` prop that
  // arrives later. Normal navigation (Resume link from /skills, where the
  // ["skills"] query is already cached) never hits this; a direct load of
  // ?resume=1 before the draft has fetched would otherwise mount
  // DesignerSurface with `draft` still null. Hold off mounting it until the
  // query settles so it always sees the real draft on its first (only) check.
  const waitingForDraft = resumeParam && isLoading;

  function confirmName() {
    if (name.trim()) setNameConfirmed(true);
  }

  if (waitingForDraft) {
    return <div className="p-8 text-muted-2">Loading…</div>;
  }

  if (showNameGate) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4 p-8">
        <div className="w-full max-w-sm space-y-3">
          <div>
            <h1 className="text-lg font-bold">Name your skill</h1>
            <p className="text-sm text-muted-2">You can change this later.</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="skill-name">Name</Label>
            <Input
              id="skill-name"
              autoFocus
              placeholder="e.g. invoice-formatter"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") confirmName();
              }}
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
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DesignerSurface
        endpoints={ENDPOINTS}
        labels={LABELS}
        startPayload={{ name: name.trim() }}
        intro={
          <DesignerIntro
            title="Tell me what this skill should teach your agents"
            blurb="A skill is a reusable capability any of your agents can call on. Describe what it should do and when it should kick in — I'll ask a few questions, then build and check it before you save it."
            examples={[
              "Turn a messy invoice into a tidy summary with the total, due date, and who sent it.",
              "Whenever I share a long article, pull out the key points as short bullets.",
              "Format a set of numbers into a clean table I can drop into a note.",
            ]}
          />
        }
        draft={draft ? { name: draft.skill_name } : null}
        autoResume={resumeParam}
        cancelTo="/skills"
        onDone={() => navigate("/skills")}
      />
    </div>
  );
}
