import { useMemo, useState } from "react";
import { Trash2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { SearchInput } from "@/components/ui/search-input";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { emojiGroups, filterEmojis } from "./emojiData";

// EmojiPicker is a dependency-free emoji chooser rendered in a dialog.
// `onSelect(emoji)` sets the icon; `onSelect(null)` clears it back to the
// default. Controlled open state so callers own where it's triggered from
// (a tree row's ⋯ menu, a page's title icon button).
//
// It now covers the full Unicode set (1906 emoji — see emojiData.ts), which is
// why the category tab strip exists: rendering nine groups into one scroll
// region turns finding anything into scrolling blind. The tabs show one group at
// a time, and a search cuts across all of them.
export default function EmojiPicker({
  open,
  onOpenChange,
  current,
  onSelect,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  current?: string;
  onSelect: (emoji: string | null) => void;
}) {
  const [query, setQuery] = useState("");
  const [groupName, setGroupName] = useState(emojiGroups[0]?.name ?? "");
  const filtered = useMemo(() => filterEmojis(query), [query]);

  const activeGroup =
    emojiGroups.find((g) => g.name === groupName) ?? emojiGroups[0];

  function pick(emoji: string | null) {
    onSelect(emoji);
    onOpenChange(false);
    setQuery("");
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* The dialog owns its height and the grid flexes into what's left. A
          `min-h` on the scroll region instead — which is what this used to have,
          to stop the picker resizing as you move between a large group and a
          small one — is a FLOOR, so it beats the dialog's own max-h and
          overflows the bottom of the modal on a short viewport. Fixing the
          height here keeps the anti-jump behaviour and cannot overflow. */}
      <DialogContent className="flex h-[min(36rem,calc(100dvh-4rem))] max-w-2xl flex-col">
        <DialogHeader>
          <DialogTitle>Choose an icon</DialogTitle>
        </DialogHeader>
        <div className="flex min-h-0 flex-col gap-3">
          <div className="flex items-center gap-3">
            <SearchInput
              autoFocus
              placeholder="Search emoji…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Search emoji"
            />
            {query && (
              <span
                aria-live="polite"
                className="shrink-0 text-sm whitespace-nowrap text-muted-2"
              >
                {filtered.length === 1 ? "1 match" : `${filtered.length} matches`}
              </span>
            )}
          </div>

          {/* Hidden while searching: results already span every category, so a
              visible category selector would imply it narrows them when it
              does not. */}
          {!query && (
            <div
              role="tablist"
              aria-label="Emoji categories"
              className="flex gap-1 overflow-x-auto pb-1"
            >
              {emojiGroups.map((g) => (
                <button
                  key={g.name}
                  type="button"
                  role="tab"
                  aria-selected={g.name === activeGroup?.name}
                  onClick={() => setGroupName(g.name)}
                  className={cn(
                    "shrink-0 rounded-md px-2.5 py-1.5 text-xs font-medium whitespace-nowrap",
                    g.name === activeGroup?.name
                      ? "bg-chrome text-foreground"
                      : "text-muted-2 hover:bg-chrome hover:text-foreground",
                  )}
                >
                  {g.name}
                </button>
              ))}
            </div>
          )}

          {/* min-h-0 is what lets a flex child actually shrink — without it the
              automatic minimum is content-based and the grid pushes out of the
              column instead of scrolling inside it. */}
          <div className="min-h-0 flex-1 overflow-y-auto pr-1">
            {query ? (
              filtered.length === 0 ? (
                <p className="py-6 text-center text-sm text-muted-2">
                  No emoji match “{query}”.
                </p>
              ) : (
                <EmojiGrid
                  emojis={filtered.map((e) => e.emoji)}
                  current={current}
                  onPick={pick}
                />
              )
            ) : (
              <EmojiGrid
                emojis={(activeGroup?.emojis ?? []).map((e) => e.emoji)}
                current={current}
                onPick={pick}
              />
            )}
          </div>

          {current && (
            <div className="flex justify-end border-t border-border pt-3">
              <Button variant="outline" size="sm" onClick={() => pick(null)}>
                <Trash2 />
                Remove icon
              </Button>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function EmojiGrid({
  emojis,
  current,
  onPick,
}: {
  emojis: string[];
  current?: string;
  onPick: (emoji: string) => void;
}) {
  // auto-fill rather than a fixed column count: the dialog's width is not the
  // grid's to assume, and a fixed 10 columns of 2.25rem is either cramped or
  // overflowing at every width but the one it was measured at.
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(2.25rem,1fr))] gap-1">
      {emojis.map((emoji) => (
        <button
          key={emoji}
          type="button"
          onClick={() => onPick(emoji)}
          aria-label={`Set icon ${emoji}`}
          className={cn(
            "flex size-9 items-center justify-center rounded-md text-xl hover:bg-chrome",
            current === emoji && "bg-border",
          )}
        >
          {emoji}
        </button>
      ))}
    </div>
  );
}
