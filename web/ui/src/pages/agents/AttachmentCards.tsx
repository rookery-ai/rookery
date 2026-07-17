import { useEffect, useState } from "react";
import { Link } from "react-router";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import {
  useAgentActions,
  // Aliased per Task 8's contract note: lib/agents exports the generic type
  // names Skill/Connection/CoreSkill — alias on import so any file that also
  // pulls in lib/skills.ts's differently-named types never collides.
  type Skill as AgentSkill,
  type CoreSkill as AgentCoreSkill,
  type Connection as AgentConnection,
} from "@/lib/agents";

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

type SkillsCardProps = {
  agentId: string;
  attachedSkills: string[];
  coreSkills: AgentCoreSkill[];
  allSkills: AgentSkill[];
};

// Checkboxes over the core ∪ user skill-name pool. Checked state starts from
// the agent's currently-declared attached_skills; Save PUTs the full checked
// set (skill_names) — matches how the designer's own `# Skills:` header is
// the DB-backed source of truth (see agent_skills table).
export function SkillsCard({ agentId, attachedSkills, coreSkills, allSkills }: SkillsCardProps) {
  const { saveSkills } = useAgentActions();
  const [checked, setChecked] = useState<Set<string>>(new Set(attachedSkills));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setChecked(new Set(attachedSkills));
  }, [attachedSkills]);

  function toggle(name: string) {
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      await saveSkills(agentId, Array.from(checked));
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSaving(false);
    }
  }

  const dirty =
    checked.size !== attachedSkills.length || attachedSkills.some((n) => !checked.has(n));
  const empty = coreSkills.length === 0 && allSkills.length === 0;

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Skills ({checked.size})</h2>
        <Button
          size="sm"
          aria-label="Save skills"
          onClick={() => void handleSave()}
          disabled={!dirty || saving}
        >
          Save
        </Button>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      {empty ? (
        <p className="text-xs text-muted-2">No skills available.</p>
      ) : (
        <div className="flex flex-col gap-3">
          {coreSkills.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <p className="text-[10px] font-medium uppercase tracking-wide text-muted-2">Core</p>
              {coreSkills.map((sk) => (
                <label key={sk.name} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={checked.has(sk.name)}
                    onChange={() => toggle(sk.name)}
                    className="size-3.5 rounded border-border"
                  />
                  {sk.name}
                </label>
              ))}
            </div>
          )}
          {allSkills.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <p className="text-[10px] font-medium uppercase tracking-wide text-muted-2">
                Your skills
              </p>
              {allSkills.map((sk) => (
                <label key={sk.id} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={checked.has(sk.name)}
                    onChange={() => toggle(sk.name)}
                    className="size-3.5 rounded border-border"
                  />
                  {sk.name}
                </label>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

type ConnectionsCardProps = {
  agentId: string;
  attachedConnectionIds: string[];
  connections: AgentConnection[];
};

// Checkboxes over the workspace's connection pool. An empty pool means
// nothing is connected yet — point at /connections instead of rendering a
// useless empty Save button.
export function ConnectionsCard({
  agentId,
  attachedConnectionIds,
  connections,
}: ConnectionsCardProps) {
  const { saveConnections } = useAgentActions();
  const [checked, setChecked] = useState<Set<string>>(new Set(attachedConnectionIds));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setChecked(new Set(attachedConnectionIds));
  }, [attachedConnectionIds]);

  function toggle(id: string) {
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      await saveConnections(agentId, Array.from(checked));
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSaving(false);
    }
  }

  const dirty =
    checked.size !== attachedConnectionIds.length ||
    attachedConnectionIds.some((id) => !checked.has(id));

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Connections ({checked.size})</h2>
        {connections.length > 0 && (
          <Button
            size="sm"
            aria-label="Save connections"
            onClick={() => void handleSave()}
            disabled={!dirty || saving}
          >
            Save
          </Button>
        )}
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      {connections.length === 0 ? (
        <p className="text-xs text-muted-2">
          No connected services yet.{" "}
          <Link to="/connections" className="text-accent underline">
            Connect services first
          </Link>
          .
        </p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {connections.map((c) => (
            <label key={c.id} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={checked.has(c.id)}
                onChange={() => toggle(c.id)}
                className="size-3.5 rounded border-border"
              />
              <span className="capitalize">{c.provider}</span>
              <span className="text-muted-2">— {c.account_label}</span>
            </label>
          ))}
        </div>
      )}
    </div>
  );
}
