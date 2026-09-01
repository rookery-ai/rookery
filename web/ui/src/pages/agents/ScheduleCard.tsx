import { useEffect, useState } from "react";
import { AlertTriangle, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/api";
import { useAgentActions, type AgentSchedule } from "@/lib/agents";
import { describeCronSentence } from "@/lib/cron";

type ScheduleCardProps = {
  agentId: string;
  schedule: AgentSchedule;
};

export function ScheduleCard({ agentId, schedule }: ScheduleCardProps) {
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
