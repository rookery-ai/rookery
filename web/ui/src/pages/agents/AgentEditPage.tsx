import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { AlertTriangle } from "lucide-react";
import {
  DesignerSurface,
  type DesignerEndpoints,
  type DesignerLabels,
} from "@/components/designer/DesignerSurface";
import { Composer } from "@/components/chat/Composer";
import { api, ApiError } from "@/lib/api";
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

// Conversational agent editing. The FIRST message is a special endpoint
// (`/agents/:id/edit/start`) that creates the design session server-side —
// once that's done the session is indistinguishable from a create session,
// so the shared DesignerSurface's own mount-recovery (GET .../design/state)
// picks it up and every later message goes through the normal design
// endpoint. Reloading mid-edit is covered the same way: the draft persists
// server-side after every turn, so `hasMatchingDraft` below skips straight
// to DesignerSurface, which then resumes it (or replays it, if the in-memory
// session survived the reload).
export default function AgentEditPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: detail } = useAgentDetail(id);
  const { data } = useAgents();
  const draft = data?.draft ?? null;
  const hasMatchingDraft = !!draft && draft.is_edit && draft.agent_id === id;

  const [editStarted, setEditStarted] = useState(false);
  const [pending, setPending] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);

  const showSurface = editStarted || hasMatchingDraft;

  async function handleFirstSubmit(text: string) {
    setPending(true);
    setStartError(null);
    try {
      await api.post(`/api/v1/agents/${id}/edit/start`, { message: text });
      setEditStarted(true);
    } catch (err) {
      setStartError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setPending(false);
    }
  }

  if (!showSurface) {
    return (
      <div className="flex h-full flex-col">
        <div className="border-b border-border px-6 py-4">
          <h1 className="text-lg font-bold">
            Edit {detail?.agent.name ?? "agent"}
          </h1>
          <p className="text-sm text-muted-2">What would you like to change?</p>
        </div>
        <div className="flex-1" />
        {startError && (
          <div className="flex items-center gap-2 border-t border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
            <AlertTriangle className="size-3.5 shrink-0" />
            {startError}
          </div>
        )}
        <Composer
          onSend={(v) => void handleFirstSubmit(v)}
          busy={pending}
          placeholder="Describe the change…"
          autoFocus
        />
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <DesignerSurface
        endpoints={ENDPOINTS}
        labels={LABELS}
        draft={draft && draft.is_edit ? { name: draft.agent_name } : null}
        onDone={() => navigate(`/agents/${id}`)}
      />
    </div>
  );
}
