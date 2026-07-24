import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export type SpecPanelProps = {
  agentMD: string;
  tools: Record<string, string>;
};

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

// parseSchedule translates only cron shapes we can PROVE are right — a
// plausible-but-wrong plain-language schedule is worse than showing the raw
// cron, because the user has no way to tell it's wrong. Anything outside
// these four shapes falls back to the raw expression. Deliberately not a
// general cron parser.
//
// The regexes below match *shape* only — they don't bound the captured
// digits — so every branch re-checks the captured value against the field's
// real range before trusting it: minute step 1–59 (a step of 0 isn't an
// interval at all, and a step >59 in a 0-59 minute field only ever matches
// minute 0 — i.e. it actually runs HOURLY, not "every 90 minutes"), hour
// 0–23, weekday 0–7 (cron allows both 0 and 7 for Sunday). Out-of-range
// values fall through to the raw-expression fallback rather than emitting
// confidently wrong prose.
export function parseSchedule(md: string): string | null {
  const m = md.match(/^#\s*Suggested schedule:\s*(.+?)\s*$/im);
  if (!m) return null;
  const cron = m[1]!.trim();
  if (!cron || /^none$/i.test(cron)) return null;

  let mm = cron.match(/^\*\/(\d+)\s+\*\s+\*\s+\*\s+\*$/);
  if (mm) {
    const step = Number(mm[1]);
    if (step >= 1 && step <= 59) {
      return step === 1 ? "every minute" : `every ${step} minutes`;
    }
    return `schedule: ${cron}`;
  }

  if (/^0\s+\*\s+\*\s+\*\s+\*$/.test(cron)) return "every hour";

  mm = cron.match(/^0\s+(\d{1,2})\s+\*\s+\*\s+\*$/);
  if (mm) {
    const hour = Number(mm[1]);
    if (hour >= 0 && hour <= 23) return `every day at ${mm[1]!.padStart(2, "0")}:00`;
    return `schedule: ${cron}`;
  }

  mm = cron.match(/^0\s+(\d{1,2})\s+\*\s+\*\s+(\d)$/);
  if (mm) {
    const hour = Number(mm[1]);
    const weekday = Number(mm[2]);
    if (hour >= 0 && hour <= 23 && weekday >= 0 && weekday <= 7) {
      const day = WEEKDAYS[weekday % 7]; // cron allows both 0 and 7 for Sunday
      if (day) return `every ${day} at ${mm[1]!.padStart(2, "0")}:00`;
    }
    return `schedule: ${cron}`;
  }

  return `schedule: ${cron}`;
}

function parseHeaderList(md: string, label: string): string[] {
  const re = new RegExp(`^#\\s*${label}:\\s*(.*)$`, "im");
  const m = md.match(re);
  if (!m) return [];
  const raw = m[1]!.trim();
  if (!raw || /^none$/i.test(raw)) return [];
  return raw
    .split(/[,;|]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function parseSkills(md: string): string[] {
  return parseHeaderList(md, "Skills");
}

export function parseConnections(md: string): string[] {
  return parseHeaderList(md, "Connections");
}

// The `# Suggested schedule:` / `# Skills:` / `# Connections:` lines are
// designer-machine directives, not part of the brief written for the human —
// they're surfaced separately as the meta badges above. Strip them from the
// markdown body so they don't ALSO render as headings (and so their values,
// e.g. "pdf", don't collide with the badge text in a text query).
function stripHeaderLines(md: string): string {
  return md
    .split("\n")
    .filter((line) => !/^#\s*(Suggested schedule|Skills|Connections):/i.test(line))
    .join("\n")
    .trim();
}

// Read-only monospace file body — mirrors FileViewer.tsx's presentation for
// KB "code" files rather than inventing a second style.
function ToolFileBody({ content }: { content: string }) {
  return (
    <pre className="min-w-full overflow-x-auto whitespace-pre border-t border-border bg-background px-4 py-4 font-mono text-xs text-foreground">
      {content}
    </pre>
  );
}

// Markdown render config mirrors CoreSkillViewPage's (pages/skills/SkillDetailPage.tsx)
// full-document brief viewer: remarkGfm only, no rehype-raw (raw HTML in the
// brief renders as inert text, not markup), links open in a new tab.
const MARKDOWN_CLASSES = [
  "max-w-none text-sm leading-relaxed",
  "[&_p]:my-2 [&_pre]:my-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-chrome [&_pre]:p-3",
  "[&_code]:break-words [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5",
  "[&_strong]:font-semibold [&_a]:underline [&_a]:text-accent",
  "[&_h1]:mt-4 [&_h1]:text-lg [&_h1]:font-bold [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-bold",
].join(" ");

export function SpecPanel({ agentMD, tools }: SpecPanelProps) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const hasContent = agentMD.trim().length > 0 || Object.keys(tools).length > 0;

  if (!hasContent) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
        <p className="max-w-sm text-sm text-muted-2">
          The spec is what the designer builds — the agent&apos;s brief plus its schedule, skills,
          connections, and any helper scripts. It appears here once you&apos;ve built the agent.
        </p>
      </div>
    );
  }

  const schedule = parseSchedule(agentMD);
  const skills = parseSkills(agentMD);
  const connections = parseConnections(agentMD);
  const toolEntries = Object.entries(tools);
  const hasMeta = !!schedule || skills.length > 0 || connections.length > 0;
  const brief = stripHeaderLines(agentMD);

  function toggle(path: string) {
    setExpanded((e) => ({ ...e, [path]: !e[path] }));
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      {hasMeta && (
        <div className="mb-5 flex flex-wrap gap-2 text-xs">
          {schedule && (
            <span className="rounded-full border border-border bg-chrome px-2.5 py-1 text-muted-2">
              Schedule: {schedule}
            </span>
          )}
          {skills.length > 0 && (
            <span className="rounded-full border border-border bg-chrome px-2.5 py-1 text-muted-2">
              Skills: {skills.join(", ")}
            </span>
          )}
          {connections.length > 0 && (
            <span className="rounded-full border border-border bg-chrome px-2.5 py-1 text-muted-2">
              Connections: {connections.join(", ")}
            </span>
          )}
        </div>
      )}

      {brief && (
        <div className={MARKDOWN_CLASSES}>
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              a: ({ node: _node, ...props }) => (
                <a {...props} target="_blank" rel="noreferrer noopener" />
              ),
            }}
          >
            {brief}
          </ReactMarkdown>
        </div>
      )}

      {toolEntries.length > 0 && (
        <div className="mt-6 flex flex-col gap-2">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-2">Files</h2>
          {toolEntries.map(([path, content]) => (
            <div key={path} className="overflow-hidden rounded-lg border border-border">
              <button
                type="button"
                onClick={() => toggle(path)}
                className="flex w-full items-center justify-between px-3 py-2 text-left font-mono text-sm hover:bg-chrome"
              >
                {path}
              </button>
              {expanded[path] && <ToolFileBody content={content} />}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default SpecPanel;
