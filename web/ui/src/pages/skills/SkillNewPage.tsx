import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import {
  DesignerSurface,
  type DesignerEndpoints,
  type DesignerLabels,
} from "@/components/designer/DesignerSurface";
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
  buildButton: "🔨 Build it",
  saveButton: "✅ Save skill",
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
  const { data } = useSkills();
  const draft = data?.draft ?? null;

  const [name, setName] = useState("");
  const [nameConfirmed, setNameConfirmed] = useState(false);

  const showNameGate = !resumeParam && !draft && !nameConfirmed;

  function confirmName() {
    if (name.trim()) setNameConfirmed(true);
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
        draft={draft ? { name: draft.skill_name } : null}
        autoResume={resumeParam}
        cancelTo="/skills"
        onDone={() => navigate("/skills")}
      />
    </div>
  );
}
