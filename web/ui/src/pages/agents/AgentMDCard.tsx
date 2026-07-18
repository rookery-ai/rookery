import { useEffect, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { useAgentActions } from "@/lib/agents";

type AgentMDCardProps = {
  agentId: string;
  content: string;
  // AgentDetail.missing_secrets — names of secrets AGENT.md references that
  // aren't in the store yet.
  missingSecrets: string[];
};

export function AgentMDCard({ agentId, content, missingSecrets }: AgentMDCardProps) {
  const { saveAgentMD } = useAgentActions();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(content);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Keep the draft synced to the latest saved content while not actively
  // editing (e.g. after a save resolves and the detail query refetches).
  useEffect(() => {
    if (!editing) setDraft(content);
  }, [content, editing]);

  function startEditing() {
    setError(null);
    setDraft(content);
    setEditing(true);
  }

  function cancelEditing() {
    setError(null);
    setDraft(content);
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      await saveAgentMD(agentId, draft);
      setEditing(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setSaving(false);
    }
  }

  const dirty = draft !== content;

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">AGENT.md</h2>
        {editing ? (
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={cancelEditing} disabled={saving}>
              Cancel
            </Button>
            <Button
              size="sm"
              aria-label="Save AGENT.md"
              onClick={() => void handleSave()}
              disabled={!dirty || saving}
            >
              Save
            </Button>
          </div>
        ) : (
          <Button size="sm" variant="outline" onClick={startEditing}>
            Edit
          </Button>
        )}
      </div>

      {missingSecrets.length > 0 && (
        <div className="flex items-center gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">
          <AlertTriangle className="size-3.5 shrink-0" />
          This agent expects secrets: {missingSecrets.join(", ")} — add them in Secrets.
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      {editing ? (
        <textarea
          aria-label="AGENT.md"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="min-h-64 w-full resize-y rounded-md border border-border bg-background p-3 font-mono text-xs outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
        />
      ) : (
        <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-md bg-chrome p-3 font-mono text-xs">
          {content}
        </pre>
      )}
    </div>
  );
}

export default AgentMDCard;
