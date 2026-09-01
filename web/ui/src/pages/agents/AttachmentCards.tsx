import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { AlertTriangle, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { useAgentMCPServers, useSaveAgentMCPServers } from "@/lib/mcp";
import { CollapsibleChecklist, type ChecklistItem } from "./CollapsibleChecklist";
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
export function SkillsCard({
  agentId,
  attachedSkills,
  coreSkills,
  allSkills,
}: SkillsCardProps) {
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
    checked.size !== attachedSkills.length ||
    attachedSkills.some((n) => !checked.has(n));
  const empty = coreSkills.length === 0 && allSkills.length === 0;

  // Your skills first (priority 0), core second (1): when nothing is attached
  // yet, a collapsed panel should offer the owner's own skills before the
  // built-in pool. Sections keep the headings the card already had, and the
  // list still reads Core-then-yours when expanded because the sections are
  // emitted in this array's order.
  const items = useMemo<ChecklistItem[]>(
    () => [
      ...coreSkills.map((sk) => ({
        key: sk.name,
        label: sk.name,
        section: "Core",
        priority: 1,
      })),
      ...allSkills.map((sk) => ({
        key: sk.name,
        label: sk.name,
        section: "Your skills",
        priority: 0,
      })),
    ],
    [coreSkills, allSkills],
  );

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
          <Save />
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
        <CollapsibleChecklist
          items={items}
          checked={checked}
          onToggle={toggle}
          noun="skills"
        />
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
  const [checked, setChecked] = useState<Set<string>>(
    new Set(attachedConnectionIds),
  );
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

  // One flat, unlabelled section: every connection is the workspace's own, so
  // there is no core-versus-yours split to draw here.
  const items = useMemo<ChecklistItem[]>(
    () =>
      connections.map((c) => ({
        key: c.id,
        label: (
          <>
            <span className="capitalize">{c.provider}</span>
            <span className="text-muted-2">— {c.account_label}</span>
          </>
        ),
      })),
    [connections],
  );

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
            <Save />
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
        <CollapsibleChecklist
          items={items}
          checked={checked}
          onToggle={toggle}
          noun="connections"
        />
      )}
    </div>
  );
}

// MCPServersCard binds an agent to MCP servers, mirroring ConnectionsCard.
//
// It is the RELIABLE path of the three. The designer also parses a "# MCP:" header
// and auto-binds the servers a build actually called, but a weak model can omit both
// — and an agent silently bound to nothing looks identical to one whose server is
// down. This card is where that gets fixed by hand.
export function MCPServersCard({ agentId }: { agentId: string }) {
  const query = useAgentMCPServers(agentId);
  const save = useSaveAgentMCPServers(agentId);
  const servers = query.data?.servers ?? [];
  const attached = query.data?.attached ?? [];
  const attachedKey = attached.join(",");

  const [checked, setChecked] = useState<Set<string>>(new Set(attached));
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setChecked(new Set(attachedKey ? attachedKey.split(",") : []));
    // Keyed on the joined ids rather than the array: `attached` is a fresh array on
    // every background refetch, which would otherwise wipe in-progress ticks.
  }, [attachedKey]);

  const dirty =
    checked.size !== attached.length || attached.some((id) => !checked.has(id));

  // MCP was not named in the request, but it has the identical problem and
  // sits in the same sidebar column — leaving it out would make one panel there
  // behave differently for no reason a reader could infer.
  const items = useMemo<ChecklistItem[]>(
    () =>
      servers.map((s) => ({
        key: s.id,
        label: (
          <>
            <span>{s.name}</span>
            <span className="text-muted-2">— {s.active_tools} tools</span>
          </>
        ),
      })),
    [servers],
  );

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">MCP servers ({checked.size})</h2>
        {servers.length > 0 && (
          <Button
            size="sm"
            aria-label="Save MCP servers"
            onClick={async () => {
              setError(null);
              try {
                await save.mutateAsync(Array.from(checked));
              } catch (err) {
                setError(errMessage(err));
              }
            }}
            disabled={!dirty || save.isPending}
          >
            <Save />
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

      {servers.length === 0 ? (
        <p className="text-xs text-muted-2">
          No MCP servers yet.{" "}
          <Link to="/connections" className="text-accent underline">
            Add one first
          </Link>
          .
        </p>
      ) : (
        <CollapsibleChecklist
          items={items}
          checked={checked}
          onToggle={(id) =>
            setChecked((prev) => {
              const next = new Set(prev);
              if (next.has(id)) next.delete(id);
              else next.add(id);
              return next;
            })
          }
          noun="MCP servers"
        />
      )}
    </div>
  );
}
