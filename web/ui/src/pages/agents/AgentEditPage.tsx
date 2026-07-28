import { useNavigate, useParams } from "react-router";
import {
  DesignerSurface,
  type DesignerEndpoints,
  type DesignerLabels,
} from "@/components/designer/DesignerSurface";
import { DesignerIntro } from "@/components/designer/DesignerIntro";
import { useAgents, useAgentDetail } from "@/lib/agents";

const ENDPOINTS: DesignerEndpoints = {
  design: "/api/v1/agents/design",
  cancel: "/api/v1/agents/design/cancel",
  resume: "/api/v1/agents/design/resume",
  dismiss: "/api/v1/agents/design/dismiss",
  progress: "/api/v1/agents/design/progress",
  state: "/api/v1/agents/design/state",
};

const LABELS: DesignerLabels = {
  steps: ["Describe", "Diagnose", "Build", "Review"],
  buildButton: "🔨 Build it",
  saveButton: "✅ Save agent",
  entityName: "agent",
};

// Phrased the way someone describes a problem, not the way they'd file a bug —
// the whole design conversation is deliberately non-technical.
const INTRO_EXAMPLES = [
  "It runs too often — once a day in the morning is enough.",
  "It keeps telling me about things I've already seen.",
  "Only message me when something actually changed.",
];

// Conversational agent editing — the SAME surface as agent creation, so the chat
// never changes shape mid-conversation. The only difference is the first
// message: it POSTs to /agents/:id/edit/start, which creates the design session
// server-side. Once that returns, the session is indistinguishable from a create
// session and every later message goes through the normal design endpoint, which
// is why the rest of the flow needs no special casing at all.
//
// This page used to render its own full-width pre-screen until that first reply
// landed, then swap in DesignerSurface — a visible jump from full width to the
// designer's 10% gutter, and a first turn that produced no bubble and no typing
// indicator while a full coder round-trip ran. DesignerSurface's `startEndpoint`
// prop removed the only reason a second chat surface existed.
export default function AgentEditPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: detail } = useAgentDetail(id);
  const { data } = useAgents();
  const draft = data?.draft ?? null;
  // Only a draft for THIS agent's edit can be resumed here — a create draft, or
  // an edit draft for another agent, belongs to a different page.
  const matchingDraft = draft && draft.is_edit && draft.agent_id === id ? draft : null;
  const agentName = detail?.agent.name ?? "this agent";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DesignerSurface
        endpoints={ENDPOINTS}
        labels={LABELS}
        startEndpoint={`/api/v1/agents/${id}/edit/start`}
        // The design session is a per-workspace singleton; without this the
        // surface's mount recovery would adopt an unrelated live create session
        // and offer to save the wrong agent — the job the deleted pre-screen's
        // draft gate used to do.
        acceptRecoveredSession={(s) => s.isEdit && s.agentId === id}
        intro={
          <DesignerIntro
            title={`What would you like to change about ${agentName}?`}
            blurb="Describe what's wrong or what you'd like different. I'll work out why it's happening, tell you what I plan to change, and only rebuild once you're happy with the plan."
            examples={INTRO_EXAMPLES}
          />
        }
        draft={matchingDraft ? { name: matchingDraft.agent_name } : null}
        cancelTo={`/agents/${id}`}
        onDone={() => navigate(`/agents/${id}`)}
      />
    </div>
  );
}
