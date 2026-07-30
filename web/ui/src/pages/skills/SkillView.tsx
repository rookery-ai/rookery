import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// Shared markdown styling. It lives in one place because the whole point of this
// component is that a built-in skill and a created one look identical.
const PROSE = [
  "max-w-none rounded-lg border border-border bg-background p-6 text-sm leading-relaxed",
  "[&_p]:my-2 [&_pre]:my-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-chrome [&_pre]:p-3",
  "[&_code]:break-words [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5",
  "[&_strong]:font-semibold [&_a]:underline [&_a]:text-accent",
  "[&_h1]:mt-4 [&_h1]:text-lg [&_h1]:font-bold [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-bold",
].join(" ");

export function SkillChip({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full bg-chrome px-2 py-0.5 text-xs font-medium text-muted-2">
      {children}
    </span>
  );
}

export type SkillViewProps = {
  kind: "core" | "user";
  name: string;
  description?: string;
  category: string;
  version?: string;
  requires?: string[];
  content: string;
  /** Present only for kind="user" — a core skill is embedded in the binary. */
  onSave?: (content: string) => Promise<void>;
  onDelete?: () => void;
  /** Extra controls rendered beside the tab switcher (e.g. a back link). */
  actions?: React.ReactNode;
};

/**
 * The one viewer for both kinds of skill.
 *
 * Before this, a user skill opened as a raw monospace textarea and a core skill
 * as rendered markdown — the same document displayed as two different things.
 * Now both render the same metadata header and default to the rendered view; the
 * only difference is that a core skill's Raw tab is read-only, because there is
 * no file on disk to write to.
 */
export function SkillView({
  kind,
  name,
  description,
  category,
  version,
  requires = [],
  content,
  onSave,
  onDelete,
  actions,
}: SkillViewProps) {
  // Rendered is the default for BOTH kinds: a skill is a document meant to be
  // read, and the source is the secondary view even where it is editable.
  const [tab, setTab] = useState<"rendered" | "raw">("rendered");
  const [draft, setDraft] = useState(content);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Resets only when the LOADED content changes, never on a tab switch, so
  // toggling views cannot silently discard an unsaved edit.
  useEffect(() => setDraft(content), [content]);

  const editable = kind === "user" && !!onSave;
  const dirty = draft !== content;
  const shown = editable ? draft : content;

  async function handleSave() {
    if (!onSave) return;
    setSaving(true);
    setError(null);
    try {
      await onSave(draft);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-2 flex items-start justify-between gap-3">
        <h1 className="text-xl font-bold">{name}</h1>
        <div className="flex shrink-0 items-center gap-2">
          <div className="flex rounded-md border border-border p-0.5">
            {(["rendered", "raw"] as const).map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => setTab(t)}
                className={cn(
                  "rounded px-2 py-1 text-xs font-medium capitalize",
                  tab === t ? "bg-chrome text-foreground" : "text-muted-2",
                )}
              >
                {t}
              </button>
            ))}
          </div>
          {editable && (
            <>
              <Button
                size="sm"
                aria-label="Save skill"
                onClick={() => void handleSave()}
                disabled={!dirty || saving}
              >
                Save
              </Button>
              <Button size="sm" variant="outline" className="text-danger" onClick={onDelete}>
                Delete
              </Button>
            </>
          )}
          {actions}
        </div>
      </div>

      <div className="mb-2 flex flex-wrap items-center gap-1.5">
        <SkillChip>{category}</SkillChip>
        {version && <SkillChip>v{version}</SkillChip>}
        <SkillChip>{kind === "core" ? "Built-in" : "Yours"}</SkillChip>
      </div>

      {requires.length > 0 && (
        <p className="mb-2 text-xs text-muted-2">Needs: {requires.join(", ")}</p>
      )}
      {description && <p className="mb-4 max-w-2xl text-sm text-muted-2">{description}</p>}

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      {tab === "rendered" ? (
        <div className={PROSE}>
          {/* No rehype-raw: raw HTML in a SKILL.md renders as inert text. That
              matters MORE for user skills than built-in ones — their content is
              model-generated or pasted in. */}
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              a: ({ node: _node, ...props }) => (
                <a {...props} target="_blank" rel="noreferrer noopener" />
              ),
            }}
          >
            {shown}
          </ReactMarkdown>
        </div>
      ) : (
        <textarea
          aria-label="SKILL.md"
          value={shown}
          readOnly={!editable}
          onChange={(e) => setDraft(e.target.value)}
          className="min-h-[60vh] w-full resize-y rounded-md border border-border bg-background p-3 font-mono text-xs outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
        />
      )}
    </div>
  );
}
