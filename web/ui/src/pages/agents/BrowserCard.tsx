import { useState } from "react";
import { ShieldAlert } from "lucide-react";
import { ApiError } from "@/lib/api";
import { useAgentActions, type AgentBrowserGrants } from "@/lib/agents";

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

type Props = {
  agentId: string;
  grants: AgentBrowserGrants;
};

// BrowserCard asks the owner about the one browser action that needs asking
// about: something that cannot be undone.
//
// It renders ONLY when this agent actually does such a thing — declared when the
// agent was built, or discovered when a run was refused one. That condition is
// the whole design. An earlier version showed a permissions card on every agent,
// including ones that only read a page, which trains an owner to tick whatever
// is in front of them; a warning that appears everywhere is one nobody reads.
//
// There is deliberately no switch here for clicking, typing or signing in.
// Testing showed that gated nothing: asked to log into a site, an agent did it
// with `bash` and `curl` instead, so the switch withheld one route to something
// it could do anyway — friction for the owner, no safety.
export function BrowserCard({ agentId, grants }: Props) {
  const { saveBrowserGrants } = useAgentActions();
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  // Nothing to ask about: this agent does not do anything irreversible, or this
  // server cannot browse at all.
  if (!grants.needs_irreversible || !grants.available) return null;

  async function set(irreversible: boolean) {
    setBusy(true);
    setErr("");
    try {
      await saveBrowserGrants(agentId, { irreversible });
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  }

  const granted = grants.irreversible;

  return (
    <section
      className={`rounded-lg border p-4 ${
        granted ? "border-warn/40 bg-warn-soft" : "border-border bg-chrome"
      }`}
    >
      <h2 className="mb-1 flex items-center gap-2 text-sm font-medium">
        <ShieldAlert className={`size-4 ${granted ? "text-warn" : ""}`} />
        This agent does something that cannot be undone
      </h2>

      {!granted && (
        // The guidance half. Without it an owner meets a stopped agent and a
        // refusal buried in a run log, and has to work out for themselves that a
        // switch exists — which is the gap this card is here to close.
        <p className="text-muted mb-3 text-sm">
          It pays, orders, transfers or deletes something as part of its job. Until you
          allow it, the agent will go right up to that step, stop, and tell you what it
          would have done.
        </p>
      )}

      <label className="flex cursor-pointer items-start gap-3 py-1">
        <input
          type="checkbox"
          className="mt-1"
          checked={granted}
          disabled={busy}
          onChange={(e) => set(e.target.checked)}
        />
        <span className="text-sm">
          <span className="font-medium">Let it complete those actions</span>
          <span className="text-muted block">
            Paying, placing orders, transferring money, deleting things.
          </span>
        </span>
      </label>

      {granted && (
        <p className="mt-2 text-sm">
          This agent can spend money without asking you first. It runs on its schedule,
          which may be while you are asleep.
        </p>
      )}

      {err && <p className="text-danger mt-2 text-sm">{err}</p>}
    </section>
  );
}
