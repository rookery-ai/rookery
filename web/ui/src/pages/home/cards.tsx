import { Link } from "react-router";
import { BookOpen, FileText, MessagesSquare, Plug, Plus } from "lucide-react";
import { buttonVariants } from "@/components/ui/button";
import { cardClass, CardTitle } from "@/components/ui/card";
import { useAgents } from "@/lib/agents";
import { useRecentFiles } from "@/pages/kb/useRecentFiles";
import { cn, formatMessageTime } from "@/lib/utils";
import { useTimeZone } from "@/lib/timezone";
import type { DashboardRun, DashboardUpcoming } from "@/lib/home";

// The homepage cards that fill what was a half-empty dashboard.
//
// They live in their own module because HomePage.tsx was already 646 lines
// carrying the context pane, the inbox, reminders and their dialogs.
//
// Every card here is built from data the SPA ALREADY fetches — no new API
// endpoint exists for any of them. `recent_runs` in particular was already on
// the page and only its failures were ever rendered.

// How many runs the activity card lists. Enough to show a pattern, few enough
// that the card stays a summary rather than becoming the run log.
const ACTIVITY_LIMIT = 8;

// ── Quick actions ───────────────────────────────────────────────────────────

// Gives the homepage a job beyond reporting status. Rendered as links, not
// buttons, so middle-click and "open in new tab" work — a button with an
// onClick navigate() silently breaks both.
export function QuickActions() {
  // Matches the Agents page header: the primary action is a default-variant
  // button at default size, secondary actions are outline at the same size.
  // Home previously rendered all four as outline/sm, which made the same kind
  // of action look like a different control depending on the page.
  const primary = cn(buttonVariants());
  const secondary = cn(buttonVariants({ variant: "outline" }));
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Link to="/agents/new" className={primary}>
        <Plus /> New agent
      </Link>
      <Link to="/kb" className={secondary}>
        <BookOpen /> New note
      </Link>
      {/* ?new=1 rather than a bare /chats: this control says "start chat", and
          the bare path lands on the list's empty state, which asks the user to
          start one — so the button started nothing. It stays a Link (see
          above), so ChatsPage does the creating. */}
      <Link to="/chats?new=1" className={secondary}>
        <MessagesSquare /> Start chat
      </Link>
      <Link to="/connections" className={secondary}>
        <Plug /> Connect a service
      </Link>
    </div>
  );
}

// ── Recent activity ─────────────────────────────────────────────────────────

function statusDot(status: DashboardRun["status"]): string {
  if (status === "failed") return "bg-danger";
  if (status === "running") return "bg-warn";
  return "bg-ok";
}

// Unlike NeedsAttentionCard — which filters to failures because that IS its job
// — this shows every recent run. The data was already fetched and its successes
// were simply never rendered, so a healthy install looked like it had done
// nothing at all.
export function RecentActivityCard({ runs }: { runs: DashboardRun[] }) {
  const tz = useTimeZone();
  const shown = runs.slice(0, ACTIVITY_LIMIT);
  return (
    <section aria-label="Recent activity" className={cardClass}>
      <CardTitle>Recent activity</CardTitle>
      {shown.length === 0 ? (
        <p className="text-sm text-muted-2">No runs yet.</p>
      ) : (
        <ul className="space-y-2">
          {shown.map((r) => (
            <li key={r.id} className="flex items-start gap-2.5 text-sm">
              <span
                aria-hidden
                className={cn("mt-1.5 size-2 shrink-0 rounded-full", statusDot(r.status))}
              />
              <span className="min-w-0 flex-1">
                <Link
                  to={`/agents/${r.agent_id}`}
                  className="font-medium text-foreground hover:underline"
                >
                  {r.agent_name}
                </Link>
                <span className="text-muted-2">
                  {" · "}
                  {r.trigger}
                  {r.status === "failed" && <span className="text-danger"> · failed</span>}
                </span>
              </span>
              <span className="shrink-0 text-xs text-muted-2">
                {formatMessageTime(r.started_at, tz)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ── Agents at a glance ──────────────────────────────────────────────────────

export function AgentsAtAGlanceCard({ upcoming }: { upcoming: DashboardUpcoming[] }) {
  // useAgents returns { agents, draft } — the draft is the designer's
  // in-progress session and has no place on a dashboard.
  const { data } = useAgents();
  const list = data?.agents ?? [];
  // Next-run times arrive keyed by agent, not on the agent row itself.
  const nextRun = new Map(upcoming.map((u) => [u.agent_id, u]));

  return (
    <section aria-label="Agents at a glance" className={cardClass}>
      <CardTitle>Agents at a glance</CardTitle>
      {list.length === 0 ? (
        <p className="text-sm text-muted-2">
          No agents yet.{" "}
          <Link to="/agents/new" className="text-accent hover:underline">
            Create one
          </Link>
          .
        </p>
      ) : (
        // Wide content scrolls inside its OWN container, never the page — the
        // page container is fluid now, so an unbounded table would otherwise
        // make the whole document scroll sideways.
        <div className="overflow-x-auto">
          <table className="w-full min-w-[28rem] text-sm">
            <thead>
              <tr className="text-left text-xs uppercase tracking-wide text-muted-2">
                <th className="pb-2 pr-3 font-bold">Agent</th>
                <th className="pb-2 pr-3 font-bold">Status</th>
                <th className="pb-2 font-bold">Next run</th>
              </tr>
            </thead>
            <tbody>
              {list.map((a) => {
                const next = nextRun.get(a.id);
                return (
                  <tr key={a.id} className="border-t border-border">
                    <td className="py-2 pr-3">
                      <Link
                        to={`/agents/${a.id}`}
                        className="font-medium text-foreground hover:underline"
                      >
                        {a.name}
                      </Link>
                    </td>
                    <td className="py-2 pr-3 text-muted-2">
                      {a.running ? (
                        <span className="text-warn">Running</span>
                      ) : a.active ? (
                        <span className="text-ok">Active</span>
                      ) : (
                        "Paused"
                      )}
                    </td>
                    <td className="py-2 text-muted-2">
                      {next?.next_run_at
                        ? new Date(next.next_run_at).toLocaleString()
                        : next
                          ? "soon"
                          : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

// ── Recently edited notes ───────────────────────────────────────────────────

// How many recents the card lists. The KB pane's own strip shows fewer; this is
// the fuller view for someone starting their day here.
const RECENT_NOTES_LIMIT = 6;

// Makes Home a starting point for knowledge work rather than only an agent
// dashboard. Reads the existing client-side recents store, so no request.
export function RecentNotesCard() {
  const { recent } = useRecentFiles();
  const shown = recent.slice(0, RECENT_NOTES_LIMIT);
  return (
    <section aria-label="Recently edited notes" className={cardClass}>
      <CardTitle>Recent notes</CardTitle>
      {shown.length === 0 ? (
        <p className="text-sm text-muted-2">
          Nothing opened yet.{" "}
          <Link to="/kb" className="text-accent hover:underline">
            Browse the knowledge base
          </Link>
          .
        </p>
      ) : (
        <ul className="space-y-1.5 text-sm">
          {shown.map((f) => (
            <li key={f.path} className="flex items-center gap-2">
              <FileText className="size-4 shrink-0 text-muted-2" />
              <Link
                to={`/kb?path=${encodeURIComponent(f.path)}`}
                className="min-w-0 truncate text-foreground hover:underline"
              >
                {f.title || f.path}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
