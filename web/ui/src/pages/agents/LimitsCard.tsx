import { useEffect, useState } from "react";
import { AlertTriangle, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/api";
import { useSaveAgentLimits, type AgentLimits } from "@/lib/agents";

// An empty field is "no limit", which the API spells 0.
const show = (n: number) => (n > 0 ? String(n) : "");
const toInt = (s: string) => {
  const n = Math.floor(Number(s));
  return Number.isFinite(n) && n > 0 ? n : 0;
};
const toMoney = (s: string) => {
  const n = Number(s);
  return Number.isFinite(n) && n > 0 ? n : 0;
};

type Draft = {
  runTokens: string;
  runTurns: string;
  runUSD: string;
  dayTokens: string;
  dayUSD: string;
  monthTokens: string;
  monthUSD: string;
};

function toDraft(l: AgentLimits): Draft {
  return {
    runTokens: show(l.run.tokens),
    runTurns: show(l.run.turns),
    runUSD: show(l.run.usd),
    dayTokens: show(l.day.tokens),
    dayUSD: show(l.day.usd),
    monthTokens: show(l.month.tokens),
    monthUSD: show(l.month.usd),
  };
}

function spendLine(s: { tokens: number; usd: number; usd_reported: boolean }, when: string) {
  const money = s.usd_reported ? ` · $${s.usd.toFixed(2)}` : "";
  return `${s.tokens.toLocaleString("en-US")} tokens${money} ${when}`;
}

export function LimitsCard({ agentId, limits }: { agentId: string; limits: AgentLimits }) {
  const save = useSaveAgentLimits();
  const [draft, setDraft] = useState<Draft>(() => toDraft(limits));
  const [error, setError] = useState<string | null>(null);
  const disabled = !limits.enforced;
  const dollars = limits.dollars_supported;

  // Re-seed when the server's figures change (a save, or another tab), keyed on
  // the serialized values so an unrelated refetch does not wipe what is typed.
  const key = JSON.stringify([limits.run, limits.day, limits.month]);
  useEffect(() => {
    setDraft(toDraft(limits));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const set = (k: keyof Draft) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setDraft((d) => ({ ...d, [k]: e.target.value }));

  async function handleSave() {
    setError(null);
    try {
      // A full replace: every figure is sent, 0 for the empty ones.
      await save.mutateAsync({
        id: agentId,
        body: {
          run: { tokens: toInt(draft.runTokens), turns: toInt(draft.runTurns), usd: toMoney(draft.runUSD) },
          day: { tokens: toInt(draft.dayTokens), usd: toMoney(draft.dayUSD) },
          month: { tokens: toInt(draft.monthTokens), usd: toMoney(draft.monthUSD) },
        },
      });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  const field = (label: string, k: keyof Draft, money = false) => (
    <label className="flex min-w-0 flex-1 flex-col gap-1 text-xs text-muted-2">
      {label}
      <Input
        aria-label={label}
        type="number"
        min={0}
        step={money ? "0.01" : "1"}
        placeholder="No limit"
        value={draft[k]}
        onChange={set(k)}
        disabled={disabled}
      />
    </label>
  );

  return (
    <div id="limits-card" className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <h2 className="text-sm font-semibold">Limits</h2>

      {disabled && (
        <p className="text-xs text-muted-2">
          Limits apply only when this agent runs on an API coder. They are kept and take effect
          again if it does.
        </p>
      )}
      {!dollars && (
        <p className="text-xs text-muted-2">
          Dollar limits need a provider that reports cost (OpenRouter, xAI).
        </p>
      )}

      <fieldset className="flex flex-col gap-1.5" disabled={disabled}>
        <legend className="text-xs font-medium">Per run</legend>
        <div className="flex gap-2">
          {field("Per run tokens", "runTokens")}
          {field("Per run turns", "runTurns")}
          {dollars && field("Per run dollars", "runUSD", true)}
        </div>
      </fieldset>

      <fieldset className="flex flex-col gap-1.5" disabled={disabled}>
        <legend className="text-xs font-medium">Per day</legend>
        <div className="flex gap-2">
          {field("Per day tokens", "dayTokens")}
          {dollars && field("Per day dollars", "dayUSD", true)}
        </div>
        <p className="text-xs text-muted-2">{spendLine(limits.spend.day, "today")}</p>
      </fieldset>

      <fieldset className="flex flex-col gap-1.5" disabled={disabled}>
        <legend className="text-xs font-medium">Per month</legend>
        <div className="flex gap-2">
          {field("Per month tokens", "monthTokens")}
          {dollars && field("Per month dollars", "monthUSD", true)}
        </div>
        <p className="text-xs text-muted-2">{spendLine(limits.spend.month, "this month")}</p>
      </fieldset>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      <div className="flex">
        <Button
          size="sm"
          aria-label="Save limits"
          onClick={() => void handleSave()}
          disabled={disabled || save.isPending}
        >
          <Save />
          Save
        </Button>
      </div>
    </div>
  );
}

export default LimitsCard;
