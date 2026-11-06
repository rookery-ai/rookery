import { useEffect, useState } from "react";
import { AlertTriangle, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/api";
import { useAgentActions, type AgentSchedule, type AgentScheduleSync } from "@/lib/agents";
import { describeCronSentence } from "@/lib/cron";

type ScheduleCardProps = {
  agentId: string;
  schedule: AgentSchedule;
  sync?: AgentScheduleSync;
};

// ScheduleSyncNote says what happened to the sentences in this agent's instructions.
// Every change is shown with its original text, because a model made it and nobody was
// asked first. A pass that found nothing to do shows nothing.
function ScheduleSyncNote({ sync }: { sync: AgentScheduleSync | undefined }) {
  if (!sync) return null;
  if (sync.state === "running") {
    return (
      <p role="status" className="text-xs text-muted-2">
        Checking the instructions for sentences that mention the old timing…
      </p>
    );
  }
  if (sync.state === "failed") {
    return (
      <div className="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">
        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
        <span>
          The schedule is saved, but sentences in the instructions that mention timing were not
          checked. {sync.error} Save again to retry.
        </span>
      </div>
    );
  }
  const refused = sync.refused > 0;
  if (sync.changes.length === 0 && sync.review.length === 0 && !refused) return null;
  return (
    <div className="flex flex-col gap-2 text-xs text-muted-2">
      {sync.changes.length > 0 && (
        <div className="flex flex-col gap-1">
          <p className="font-medium text-foreground">Updated in the instructions</p>
          <ul className="flex flex-col gap-1">
            {sync.changes.map((c) => (
              <li key={c.find} className="break-words">
                <span className="line-through">{c.find}</span> → {c.replace}
              </li>
            ))}
          </ul>
        </div>
      )}
      {sync.review.length > 0 && (
        <div className="flex flex-col gap-1">
          <p className="font-medium text-foreground">Worth checking, left as written</p>
          <ul className="flex flex-col gap-1">
            {sync.review.map((line) => (
              <li key={line} className="break-words">{line}</li>
            ))}
          </ul>
        </div>
      )}
      {refused && (
        <p>
          {sync.refused === 1
            ? "One suggested change was not safe to apply, so the instructions may still mention the old timing."
            : `${sync.refused} suggested changes were not safe to apply, so the instructions may still mention the old timing.`}
        </p>
      )}
    </div>
  );
}

export function ScheduleCard({ agentId, schedule, sync }: ScheduleCardProps) {
  const { saveSchedule, deleteSchedule } = useAgentActions();
  const [cron, setCron] = useState(schedule?.cron_expr ?? "");
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setCron(schedule?.cron_expr ?? "");
  }, [schedule?.cron_expr]);

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      await saveSchedule(agentId, cron);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setSaving(false);
    }
  }

  async function handleRemove() {
    setRemoving(true);
    setError(null);
    try {
      await deleteSchedule(agentId);
      setCron("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setRemoving(false);
    }
  }

  const description = describeCronSentence(cron);

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <h2 className="text-sm font-semibold">Schedule</h2>

      <Input
        aria-label="Cron expression"
        placeholder="*/10 * * * *"
        value={cron}
        onChange={(e) => setCron(e.target.value)}
      />

      {/* Reads the field the user is TYPING, not the saved schedule, so the
          sentence confirms what is about to be saved rather than what already
          was. describeCronSentence returns null for anything it cannot prove —
          including every half-finished expression on the way to a valid one —
          and nothing is rendered then: a flash of the wrong reading is worse
          than no reading at all. */}
      {description && (
        <p className="text-xs text-muted-2">{description}</p>
      )}

      {schedule && (
        <p className="text-xs text-muted-2">
          {schedule.enabled ? "Enabled" : "Disabled"}
          {schedule.next_run_at &&
            ` — next run ${new Date(schedule.next_run_at).toLocaleString()}`}
        </p>
      )}

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      <ScheduleSyncNote sync={sync} />

      <div className="flex gap-2">
        <Button
          size="sm"
          aria-label="Save schedule"
          onClick={() => void handleSave()}
          disabled={saving || !cron.trim()}
        >
          <Save />
          Save
        </Button>
        {schedule && (
          <Button
            size="sm"
            variant="outline"
            className="text-danger"
            onClick={() => void handleRemove()}
            disabled={removing}
          >
            <Trash2 />
            Remove
          </Button>
        )}
      </div>
    </div>
  );
}

export default ScheduleCard;
