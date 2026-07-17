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
import { useAgents } from "@/lib/agents";

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

// Conversational agent creation. A new agent needs a name before the design
// POST can start a session — that's collected here (once, up front) rather
// than inside DesignerSurface, which stays entity-agnostic for Task 8's
// skill-creator reuse.
export default function AgentNewPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const resumeParam = params.get("resume") === "1";
  const { data } = useAgents();
  const draft = data?.draft ?? null;

  const [name, setName] = useState("");
  const [nameConfirmed, setNameConfirmed] = useState(false);

  // An existing draft (create OR edit-in-progress under this workspace)
  // means there's already a session to resume/discard — DesignerSurface's
  // own recovery banner handles that, so the name gate is skipped and it
  // takes over immediately. Likewise ?resume=1 goes straight there.
  const showNameGate = !resumeParam && !draft && !nameConfirmed;

  function confirmName() {
    if (name.trim()) setNameConfirmed(true);
  }

  if (showNameGate) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4 p-8">
        <div className="w-full max-w-sm space-y-3">
          <div>
            <h1 className="text-lg font-bold">Name your agent</h1>
            <p className="text-sm text-muted-2">You can change this later.</p>
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
        draft={draft ? { name: draft.agent_name } : null}
        autoResume={resumeParam}
        onDone={(id) => navigate(id ? `/agents/${id}` : "/agents")}
      />
    </div>
  );
}
