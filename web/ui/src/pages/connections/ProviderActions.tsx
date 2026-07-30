import { useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  useProviderActions,
  type ConnectorAction,
  type ServiceProvider,
} from "@/lib/connections";

/**
 * The capability badge. The read / writes / posts-publicly split mirrors the
 * manifest's own mutating + public_write pair rather than collapsing to a single
 * "writes": pausing an ad campaign is mutating but private and reversible, while
 * a LinkedIn post is neither, and that is exactly the distinction a reader
 * deciding whether to connect an account cares about.
 */
function capability(action: ConnectorAction): { label: string; tone: string } {
  if (action.public_write) {
    return { label: "posts publicly", tone: "bg-danger-soft text-danger" };
  }
  if (action.mutating) {
    return { label: "writes", tone: "bg-warn-soft text-warn" };
  }
  return { label: "read", tone: "bg-muted-surface text-muted-2" };
}

function ActionRow({ action }: { action: ConnectorAction }) {
  const [open, setOpen] = useState(false);
  const cap = capability(action);
  const properties = action.params?.properties ?? {};
  const required = new Set(action.params?.required ?? []);
  const names = Object.keys(properties);
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div className="rounded-lg border border-border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-start justify-between gap-3 p-3 text-left"
      >
        <span className="min-w-0 text-sm leading-relaxed">{action.description}</span>
        <span className="flex shrink-0 items-center gap-2">
          <span
            className={cn(
              "rounded-full px-2 py-0.5 text-xs font-medium",
              cap.tone,
            )}
          >
            {cap.label}
          </span>
          <Chevron className="size-4 text-muted-2" aria-hidden="true" />
        </span>
      </button>

      {open && (
        <div className="space-y-2 border-t border-border px-3 py-2">
          <code className="block font-mono text-xs text-muted-2">{action.name}</code>
          {names.length === 0 ? (
            <p className="text-xs text-muted-2">No parameters.</p>
          ) : (
            <ul className="space-y-1">
              {names.map((n) => (
                <li key={n} className="flex flex-wrap gap-x-2 text-xs">
                  <span className="font-mono">
                    {n}
                    {required.has(n) && <span className="text-danger">*</span>}
                  </span>
                  {properties[n].type && (
                    <span className="text-muted-2">{properties[n].type}</span>
                  )}
                  {properties[n].description && (
                    // One template literal, not `— {expr}`: JSX interpolation
                    // would split this into two text nodes, and Testing Library's
                    // getByText matches per text node.
                    <span className="text-muted-2">{`— ${properties[n].description}`}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * A read-only reference list of everything a provider lets an agent do.
 *
 * Rendered for UNCONNECTED providers too: the manifests are static embedded data
 * with nothing account-specific in them, and "what can this do for me" is the
 * strongest reason to connect in the first place.
 */
export function ProviderActions({
  provider,
  onBack,
}: {
  provider: ServiceProvider;
  onBack: () => void;
}) {
  const { data, isLoading, isError } = useProviderActions(provider.name);
  const actions = data?.actions ?? [];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
          {provider.label} · {provider.action_count} action
          {provider.action_count === 1 ? "" : "s"}
        </div>
        <button
          type="button"
          onClick={onBack}
          className="shrink-0 text-xs text-muted-2 underline underline-offset-2 hover:text-foreground"
        >
          ← Back
        </button>
      </div>

      {isLoading && <div className="p-4 text-sm text-muted-2">Loading actions…</div>}

      {isError && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
          {`Couldn't load actions for ${provider.label}.`}
        </div>
      )}

      {!isLoading && !isError && actions.length === 0 && (
        <div className="p-4 text-sm text-muted-2">
          This service exposes no actions yet.
        </div>
      )}

      {actions.map((a) => (
        <ActionRow key={a.name} action={a} />
      ))}
    </div>
  );
}

export default ProviderActions;
