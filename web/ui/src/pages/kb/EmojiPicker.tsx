import { useMemo, useState } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { emojiGroups, filterEmojis } from "./emojiData";

// EmojiPicker is a small, dependency-free emoji chooser rendered in a dialog.
// `onSelect(emoji)` sets the icon; `onSelect(null)` clears it back to the
// default. Controlled open state so callers own where it's triggered from
// (a tree row's ⋯ menu, a page's title icon button).
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
  const filtered = useMemo(() => filterEmojis(query), [query]);

  function pick(emoji: string | null) {
    onSelect(emoji);
    onOpenChange(false);
    setQuery("");
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Choose an icon</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Input
            autoFocus
            placeholder="Search emoji…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search emoji"
          />
          <div className="max-h-72 overflow-y-auto pr-1">
            {query ? (
              filtered.length === 0 ? (
                <p className="py-6 text-center text-sm text-muted-2">No matches.</p>
              ) : (
                <EmojiGrid
                  emojis={filtered.map((e) => e.emoji)}
                  current={current}
                  onPick={pick}
                />
              )
            ) : (
              emojiGroups.map((g) => (
                <div key={g.name} className="mb-3">
                  <p className="mb-1 px-0.5 text-xs font-medium text-muted-2">{g.name}</p>
                  <EmojiGrid
                    emojis={g.emojis.map((e) => e.emoji)}
                    current={current}
                    onPick={pick}
                  />
                </div>
              ))
            )}
          </div>
          {current && (
            <div className="flex justify-end border-t border-border pt-3">
              <Button variant="outline" size="sm" onClick={() => pick(null)}>
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
  return (
    <div className="grid grid-cols-8 gap-0.5">
      {emojis.map((emoji) => (
        <button
          key={emoji}
          type="button"
          onClick={() => onPick(emoji)}
          aria-label={`Set icon ${emoji}`}
          className={
            "flex h-8 w-8 items-center justify-center rounded text-lg hover:bg-chrome " +
            (current === emoji ? "bg-border" : "")
          }
        >
          {emoji}
        </button>
      ))}
    </div>
  );
}
