import { useEffect, useState } from "react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { isTypingTarget } from "@/lib/useKeyboardNav";

type ShortcutRow = { keys: string; desc: string };
type ShortcutGroup = { heading: string; items: ShortcutRow[] };

// Every shortcut in the app, grouped for the help overlay. Task 5 appends a
// "Command palette scopes" group here (the ⌘K prefixes — >, #, @ — spec
// §7) once it lands; this shape (an array of {heading, items}) exists so
// that's a one-item addition, not a rework.
const GROUPS: ShortcutGroup[] = [
  {
    heading: "Go anywhere",
    items: [
      { keys: "⌘K", desc: "Open the command palette" },
      { keys: "⌘1–7", desc: "Jump to a rail destination" },
    ],
  },
  {
    heading: "Lists",
    items: [
      { keys: "j / k", desc: "Move the highlight down / up" },
      { keys: "Enter", desc: "Open the highlighted item" },
    ],
  },
  {
    heading: "Elsewhere",
    items: [
      { keys: "⌘J", desc: "Open chat" },
      { keys: "⌘S", desc: "Save the current note" },
      { keys: "?", desc: "Show this help" },
      { keys: "Esc", desc: "Close the overlay, palette, or panel" },
    ],
  },
];

export function ShortcutsOverlay() {
  const [open, setOpen] = useState(false);

  // Owns its own "?" listener (same pattern as CommandPalette owning ⌘K and
  // GlobalChatButton owning ⌘J) — suppressed while typing so "?" typed into
  // a note/composer/conversation stays a literal question mark. Esc-to-close
  // comes for free from the Dialog primitive once open.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== "?") return;
      if (isTypingTarget(e.target)) return;
      e.preventDefault();
      setOpen(true);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>Everything you can do without a mouse.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {GROUPS.map((group) => (
            <div key={group.heading}>
              <h3 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-2">
                {group.heading}
              </h3>
              <dl className="space-y-1.5">
                {group.items.map((row) => (
                  <div key={row.keys} className="flex items-center justify-between gap-4 text-sm">
                    <dt className="text-muted">{row.desc}</dt>
                    <dd>
                      <kbd className="rounded border border-border bg-chrome px-1.5 py-0.5 font-mono text-xs">
                        {row.keys}
                      </kbd>
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

export default ShortcutsOverlay;
