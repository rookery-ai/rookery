import type { ReactNode } from "react";
import { entityIcon } from "@/lib/entityIcons";

// The shared page heading group: icon + <h1> + an optional status line.
//
// Replaces sixteen divergent <h1>s that had drifted across three sizes and two
// weights (text-xl font-bold, text-lg font-bold, text-2xl font-semibold) and
// carried no icon at all.
//
// The icon comes from the SAME map the rail reads, so a page and its rail entry
// cannot show different glyphs for the same destination — which is the reason
// the map exists rather than each surface picking its own.
//
// Deliberately scoped to the heading GROUP, not the whole header row: pages
// already own a header row with their own search boxes and action buttons, and
// their layouts differ. Owning only the part that was inconsistent keeps this
// adoptable everywhere instead of forcing each page to restructure. It also
// carries no outer margin, so the page decides its own spacing.
export function PageTitle({
  icon,
  title,
  subtitle,
}: {
  /** An entityIcons key. Unknown keys degrade to a neutral glyph. */
  icon: string;
  title: ReactNode;
  /** Optional count/status line under the title (e.g. "3 agents configured"). */
  subtitle?: ReactNode;
}) {
  const Icon = entityIcon(icon);
  return (
    <div className="flex min-w-0 items-start gap-2.5">
      {/* Nudged down so the glyph sits on the title's optical centre rather
          than its ascender line, and stays put when a subtitle makes the
          block taller. */}
      <Icon className="mt-0.5 size-5 shrink-0 text-muted" />
      <div className="min-w-0">
        <h1 className="min-w-0 truncate text-xl font-bold">{title}</h1>
        {subtitle && <p className="mt-0.5 text-sm text-muted-2">{subtitle}</p>}
      </div>
    </div>
  );
}
