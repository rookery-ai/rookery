import { useState } from "react";
import { Globe, ShieldAlert } from "lucide-react";
import { ApiError } from "@/lib/api";
import { useAgentActions, type AgentBrowserGrants } from "@/lib/agents";

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

type Props = {
  agentId: string;
  grants: AgentBrowserGrants;
};

// BrowserCard is the owner's control over what an agent may do in a real
// browser.
//
// The copy is deliberately concrete rather than abstract. "Allow acting" tells
// an owner nothing about the consequence; "click buttons and fill in forms,
// including signing in with your stored passwords" tells them what they are
// agreeing to. This is the one screen in the product where a mis-set switch
// spends real money, so it names the risk instead of gesturing at it.
//
// Saving is immediate on toggle, with no separate Save button: a permission
// left un-saved because the owner did not notice a button is a permission they
// think they granted and did not — or worse, think they revoked and did not.
export function BrowserCard({ agentId, grants }: Props) {
  const { saveBrowserGrants } = useAgentActions();
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function set(next: { acting?: boolean; irreversible?: boolean }) {
    setBusy(true);
    setErr("");
    try {
      await saveBrowserGrants(agentId, next);
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  }

  if (!grants.available) {
    return (
      <section className="rounded-lg border border-border bg-chrome p-4">
        <h2 className="mb-1 flex items-center gap-2 text-sm font-medium">
          <Globe className="size-4" /> Browser
        </h2>
        <p className="text-muted text-sm">
          This server has no browser installed, so agents cannot read
          JavaScript-rendered pages. Run{" "}
          <code className="rounded bg-background px-1 py-0.5">rookery browser install</code>{" "}
          on the server to enable it.
        </p>
      </section>
    );
  }

  return (
    <section className="rounded-lg border border-border bg-chrome p-4">
      <h2 className="mb-1 flex items-center gap-2 text-sm font-medium">
        <Globe className="size-4" /> Browser
      </h2>
      <p className="text-muted mb-3 text-sm">
        This agent can always open and read web pages in a real browser. What it may
        <em> do</em> on those pages is up to you.
      </p>

      <label className="flex cursor-pointer items-start gap-3 py-2">
        <input
          type="checkbox"
          className="mt-1"
          checked={grants.acting}
          disabled={busy}
          onChange={(e) => set({ acting: e.target.checked })}
        />
        <span className="text-sm">
          <span className="font-medium">Let it use pages, not just read them</span>
          <span className="text-muted block">
            Click buttons, fill in forms and sign in using passwords you have stored as
            secrets. The agent never sees the passwords themselves.
          </span>
        </span>
      </label>

      <label
        className={`flex items-start gap-3 py-2 ${grants.acting ? "cursor-pointer" : "cursor-not-allowed opacity-50"}`}
      >
        <input
          type="checkbox"
          className="mt-1"
          checked={grants.irreversible}
          disabled={busy || !grants.acting}
          onChange={(e) => set({ irreversible: e.target.checked })}
        />
        <span className="text-sm">
          <span className="flex items-center gap-1.5 font-medium">
            <ShieldAlert className="size-3.5 text-warn" />
            Let it do things that cannot be undone
          </span>
          <span className="text-muted block">
            Paying, placing orders, transferring money, deleting things. Without this the
            agent stops at the final button and tells you instead.
          </span>
        </span>
      </label>

      {grants.irreversible && (
        <p className="mt-2 rounded border border-warn/40 bg-warn-soft px-3 py-2 text-sm">
          This agent can spend money without asking you first. It runs on its schedule,
          which may be while you are asleep.
        </p>
      )}

      {err && <p className="text-danger mt-2 text-sm">{err}</p>}
    </section>
  );
}
