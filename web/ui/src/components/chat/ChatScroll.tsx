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
      className={cn("flex flex-1 flex-col gap-3 overflow-y-auto p-4", className)}
    >
      {children}
    </div>
  );
}
