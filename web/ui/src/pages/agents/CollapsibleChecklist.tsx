import { useMemo, useState, type ReactNode } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";

// The checkbox list shared by the Skills, Connections and MCP servers cards.
//
// All three render the whole workspace pool — 21 core skills alone — stacked in
// a 320px sidebar column, so the panel that tells you what an agent is bound to
// is mostly a list of things it is not bound to. One component rather than
// three collapse implementations: the cards were already near copies, and a
// rule about which rows survive collapsing is exactly the sort of thing that
// drifts once it exists in three places.
//
// COLLAPSING IS A DISPLAY CONCERN AND NOTHING ELSE. The `checked` Set lives in
// the card and is independent of what is rendered, so a hidden row keeps its
// state and Save still sends the full set. The tempting implementation filters
// the pool BEFORE the Set is built, which silently drops bindings.

export type ChecklistItem = {
  // Identity in the card's `checked` Set — a skill name, a connection id.
  key: string;
  // What the row says. A node rather than a string because a connection row is
  // a provider plus a muted account label.
  label: ReactNode;
  // Heading this row sits under. Rows sharing a section render under one
  // heading; "" means no heading at all. A section whose rows are all hidden
  // renders no heading either.
  section?: string;
  // Lower is kept when collapsing. The cards use 0 for the workspace's own
  // items and 1 for the built-in pool, so an owner's own skills and services
  // are what a collapsed panel offers first.
  priority?: number;
};

type Props = {
  items: ChecklistItem[];
  checked: Set<string>;
  onToggle: (key: string) => void;
  // Plural, lowercase — goes into "View all 26 skills…".
  noun: string;
  // Rows a collapsed panel shows before it starts hiding any.
  minVisible?: number;
};

const DEFAULT_MIN_VISIBLE = 5;

// visibleKeys decides what a collapsed panel shows.
//
// Every ATTACHED row is visible, always — the deliberate departure from a flat
// "show five" cap. A checkbox panel that hides a CHECKED box misreports the
// agent's configuration, which is worse than being long, and an agent binds two
// to four skills in practice so the panel still shrinks. An agent with more
// than `minVisible` attachments shows all of them and no toggle.
//
// The remainder is filled from the unattached rows in priority order until the
// panel has `minVisible` rows, which is what makes one rule cover all three
// cases the request describes: show what is selected; failing that the owner's
// own; failing that whatever is left.
export function visibleKeys(
  items: ChecklistItem[],
  checked: Set<string>,
  minVisible = DEFAULT_MIN_VISIBLE,
): Set<string> {
  const out = new Set<string>();
  for (const it of items) {
    if (checked.has(it.key)) out.add(it.key);
  }
  if (out.size >= minVisible) return out;

  // Stable: equal priorities keep the order the card supplied.
  const rest = items
    .filter((it) => !out.has(it.key))
    .map((it, i) => ({ it, i }))
    .sort((a, b) => (a.it.priority ?? 0) - (b.it.priority ?? 0) || a.i - b.i);

  for (const { it } of rest) {
    if (out.size >= minVisible) break;
    out.add(it.key);
  }
  return out;
}

export function CollapsibleChecklist({
  items,
  checked,
  onToggle,
  noun,
  minVisible = DEFAULT_MIN_VISIBLE,
}: Props) {
  const [expanded, setExpanded] = useState(false);

  const visible = useMemo(
    () => visibleKeys(items, checked, minVisible),
    [items, checked, minVisible],
  );
  const hiddenCount = items.length - visible.size;
  const shown = expanded ? items : items.filter((it) => visible.has(it.key));

  // Sections in the order the card listed them, each holding only its shown
  // rows — so a section with nothing left renders no heading.
  const sections: { name: string; rows: ChecklistItem[] }[] = [];
  for (const it of shown) {
    const name = it.section ?? "";
    const last = sections.find((s) => s.name === name);
    if (last) last.rows.push(it);
    else sections.push({ name, rows: [it] });
  }

  return (
    <div className="flex flex-col gap-3">
      {sections.map((s) => (
        <div key={s.name} className="flex flex-col gap-1.5">
          {s.name && (
            <p className="text-xs font-medium uppercase tracking-wide text-muted-2">
              {s.name}
            </p>
          )}
          {s.rows.map((it) => (
            <label key={it.key} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={checked.has(it.key)}
                onChange={() => onToggle(it.key)}
                className="size-3.5 rounded border-border"
              />
              {it.label}
            </label>
          ))}
        </div>
      ))}

      {hiddenCount > 0 && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="flex items-center gap-1 self-start text-xs font-medium text-accent hover:underline"
        >
          {expanded ? (
            <>
              <ChevronUp className="size-3.5" /> Show fewer
            </>
          ) : (
            <>
              <ChevronDown className="size-3.5" /> View all {items.length}{" "}
              {noun}…
            </>
          )}
        </button>
      )}
    </div>
  );
}
