import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export type SpecPanelProps = {
  agentMD: string;
  tools: Record<string, string>;
  // The [TECHNICAL SPEC] block the designer appends to its proposal, available
  // BEFORE any build exists. Without it this panel had nothing to show until a
  // build finished — which is exactly the moment a user most wants to re-read
  // what they are about to approve.
  spec?: string;
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

// Mirrors internal/agentdesigner/parse_mcp.go's heading, which accepts both
// "# MCP:" and "# MCP servers:". The panel listed skills and connections and
// silently omitted MCP servers, so an agent bound to one showed no sign of it.
export function parseMCP(md: string): string[] {
  return parseHeaderList(md, "MCP(?: servers)?");
}

// TECHNICAL_SPEC_FIELDS is the closed set of labels the designer is asked to
// emit (prompts.BuildDesignSystemPrompt). Anything else in the block is shown
// verbatim in the raw fallback rather than guessed at — same policy as
// parseSchedule: a plausible-but-wrong summary of what an agent is about to do
// is worse than the raw text, because the user cannot tell it is wrong.
const TECHNICAL_SPEC_FIELDS = [
  "Tier",
  "Change",
  "Root cause",
  "Tier change",
  "Schedule",
  "Notifies user",
  "Knowledge base writes",
  "Secrets",
  "External services",
] as const;

export type SpecField = { label: string; value: string };

// parseTechnicalSpec reads "Label: value" lines out of the block. It returns []
// when it recognises nothing, which is the caller's signal to render the raw
// block instead of an empty table.
export function parseTechnicalSpec(spec: string): SpecField[] {
  const out: SpecField[] = [];
  for (const line of spec.split("\n")) {
    const m = line.match(/^\s*([A-Za-z][A-Za-z ]*?)\s*:\s*(.*)$/);
    if (!m) continue;
    const label = m[1]!.trim();
    const value = m[2]!.trim();
    if (!value) continue;
    const known = TECHNICAL_SPEC_FIELDS.find(
      (f) => f.toLowerCase() === label.toLowerCase(),
    );
    if (!known) continue;
    out.push({ label: known, value });
  }
  return out;
}

// The `# Suggested schedule:` / `# Skills:` / `# Connections:` lines are
// designer-machine directives, not part of the brief written for the human —
// they're surfaced separately as the meta badges above. Strip them from the
// markdown body so they don't ALSO render as headings (and so their values,
// e.g. "pdf", don't collide with the badge text in a text query).
function stripHeaderLines(md: string): string {
  return md
    .split("\n")
    .filter(
      (line) =>
        !/^#\s*(Suggested schedule|Skills|Connections|MCP(?: servers)?):/i.test(line),
    )
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

// Badge is the one pill style the meta row uses, so a new fact (MCP servers)
// cannot arrive looking like a different kind of thing.
function Badge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full border border-border bg-chrome px-2.5 py-1 text-muted-2">
      {children}
    </span>
  );
}

// The pre-build view: the plan the user is about to approve, as an artifact
// they can re-read. Rendered only when there is no build yet — once one exists
// the generated AGENT.md is the more truthful description of what will run.
function PlannedSpec({ spec }: { spec: string }) {
  const fields = parseTechnicalSpec(spec);
  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <h2 className="mb-1 text-sm font-semibold text-foreground">Proposed plan</h2>
      <p className="mb-4 text-xs text-muted-2">
        What will be built when you approve. Nothing has run yet.
      </p>
      {fields.length > 0 ? (
        <dl className="grid grid-cols-[minmax(0,10rem)_1fr] gap-x-4 gap-y-2 text-sm">
          {fields.map((f) => (
            <div key={f.label} className="contents">
              <dt className="text-muted-2">{f.label}</dt>
              <dd className="break-words text-foreground">{f.value}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-chrome p-3 font-mono text-xs text-foreground">
          {spec}
        </pre>
      )}
    </div>
  );
}

export function SpecPanel({ agentMD, tools, spec }: SpecPanelProps) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const hasContent = agentMD.trim().length > 0 || Object.keys(tools).length > 0;

  if (!hasContent && spec && spec.trim()) {
    return <PlannedSpec spec={spec.trim()} />;
  }

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
  const mcp = parseMCP(agentMD);
  const toolEntries = Object.entries(tools);
  const hasMeta =
    !!schedule || skills.length > 0 || connections.length > 0 || mcp.length > 0;
  const brief = stripHeaderLines(agentMD);

  function toggle(path: string) {
    setExpanded((e) => ({ ...e, [path]: !e[path] }));
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      {hasMeta && (
        <div className="mb-5 flex flex-wrap gap-2 text-xs">
          {schedule && <Badge>Schedule: {schedule}</Badge>}
          {skills.length > 0 && <Badge>Skills: {skills.join(", ")}</Badge>}
          {connections.length > 0 && <Badge>Connections: {connections.join(", ")}</Badge>}
          {mcp.length > 0 && <Badge>MCP servers: {mcp.join(", ")}</Badge>}
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
