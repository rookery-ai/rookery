import type { ReactNode } from "react";

// Shared primitives for the middle "context pane" column every page renders
// via <ContextPane>. Fixing the header/section treatment in one place stops
// per-page drift in padding, heading case, and spacing (spec §7). Values here
// are exactly what HomePage used before this refactor — the canonical
// reference — so adopting them there is a no-op; other pages may visibly
// change (that's the point, and it's reported per-page).

interface ContextPaneHeaderProps {
  title: string;
  action?: ReactNode;
}

// The pane's title row: a bold h2 plus an optional right-aligned action
// (e.g. a "new note" icon button).
export function ContextPaneHeader({ title, action }: ContextPaneHeaderProps) {
  return (
    <div className="flex items-center justify-between px-4 pt-3 pb-1">
      <h2 className="text-sm font-bold">{title}</h2>
      {action}
    </div>
  );
}

interface ContextSectionProps {
  title: string;
  action?: ReactNode;
  children?: ReactNode;
}

// A titled sub-section within the pane (e.g. "Inbox", "Reminders"): a small
// uppercase h3 with an optional right-aligned action, then the section's
// content below it.
export function ContextSection({ title, action, children }: ContextSectionProps) {
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between px-1">
        <h3 className="text-[11px] font-bold uppercase tracking-wide text-muted-2">{title}</h3>
        {action}
      </div>
      {children}
    </div>
  );
}
