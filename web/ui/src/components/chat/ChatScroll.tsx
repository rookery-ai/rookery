import { useEffect, useRef, type ReactNode } from "react";
import { cn } from "@/lib/utils";

// Stick-to-bottom: scrolled within STICK_THRESHOLD px of the bottom counts
// as "at bottom" — new content keeps auto-scrolling. Scroll up further than
// that and new content no longer yanks the view down (reading history).
const STICK_THRESHOLD = 80;

export function ChatScroll({ children, className }: { children: ReactNode; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  function handleScroll() {
    const el = ref.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    stickRef.current = distanceFromBottom <= STICK_THRESHOLD;
  }

  useEffect(() => {
    const el = ref.current;
    if (!el || !stickRef.current) return;
    el.scrollTop = el.scrollHeight;
  });

  return (
    <div
      ref={ref}
      onScroll={handleScroll}
      // px-4/py-4 rather than the shorthand p-4: a caller passing a gutter
      // (px-[10%]) must be able to override the horizontal padding
      // deterministically. tailwind-merge treats `p` and `px` as separate
      // groups, so `cn("p-4", "px-[10%]")` keeps BOTH and leaves the winner to
      // the generated stylesheet's ordering. Two `px-*` classes are one group,
      // where the last one provably wins.
      className={cn("flex flex-1 flex-col gap-3 overflow-y-auto px-4 py-4", className)}
    >
      {children}
    </div>
  );
}
