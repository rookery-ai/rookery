import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

type PanelBodyProps = {
  children: ReactNode;
  className?: string;
};

// Standard slide-over content padding — form/content panels (settings cards,
// designer surfaces that don't own their own scroll region, etc.) wrap their
// body in this so every panel gets the same inset + item spacing + scroll
// behavior without repeating the class list. Full-bleed panels like chat
// (GlobalChatButton's GlobalChatPanel) opt OUT by not using it — they need
// edge-to-edge content and their own internal scroll regions instead.
export function PanelBody({ children, className }: PanelBodyProps) {
  return <div className={cn("flex-1 space-y-4 overflow-y-auto p-4", className)}>{children}</div>;
}

export default PanelBody;
