import { NavLink, useLocation } from "react-router";
import {
  House, Library, Bot, Sparkles, Plug, MessageSquare, KeyRound,
} from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { cn } from "@/lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session";
import { useInboxPoll } from "@/lib/home";
import WorkspaceMenu from "./WorkspaceMenu";

export const railItems = [
  { to: "/", label: "Home", icon: House },
  { to: "/kb", label: "Knowledge Base", icon: Library },
  { to: "/agents", label: "Agents", icon: Bot },
  { to: "/skills", label: "Skills", icon: Sparkles },
  { to: "/connections", label: "Connections", icon: Plug },
  { to: "/chats", label: "Chats", icon: MessageSquare },
  { to: "/secrets", label: "Secrets", icon: KeyRound },
];

// Small accent dot + count shown on the Home rail icon when there's unread
// inbox activity. Caps the VISIBLE label at "9+" rather than growing the
// pill for arbitrarily large counts. Renders nothing at 0 — no badge, not a
// "0" badge. Purely decorative for assistive tech (aria-hidden): the parent
// NavLink already has an explicit aria-label, and once an ancestor carries
// one the accessible-name algorithm ignores any aria-label on a descendant
// — so the count has to be folded into the LINK's label (see the "Home"
// case below) rather than announced here.
function UnreadBadge({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span
      aria-hidden="true"
      className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[10px] font-semibold leading-none text-accent-foreground"
    >
      {count > 9 ? "9+" : count}
    </span>
  );
}

// isRailActive mirrors NavLink's own default matching (exact for "/", prefix
// on a path SEGMENT boundary otherwise), computed here rather than taken from
// NavLink's render-prop.
//
// Why: every rail item is a TooltipTrigger `asChild`, and Radix's Slot merges
// the child's `className` into a STRING before NavLink ever sees it. Hand
// NavLink a function and what lands in the DOM is the stringified source of
// that function — the rail items were rendering with their own JavaScript as
// their class attribute, so NONE of the active/hover styling applied. That is
// the actual reason the rail had no perceptible hover or current-page state.
// Passing a plain string keeps Slot's merge lossless.
export function isRailActive(to: string, pathname: string): boolean {
  if (to === "/") return pathname === "/";
  return pathname === to || pathname.startsWith(`${to}/`);
}

export default function IconRail() {
  const { data: poll } = useInboxPoll();
  const unread = poll?.unread ?? 0;
  const { pathname } = useLocation();

  return (
    <nav
      aria-label="Primary"
      className="fixed bottom-0 inset-x-0 z-20 flex flex-row items-center justify-around border-t border-border bg-chrome px-2 py-1
                 md:static md:h-full md:w-16 md:flex-col md:justify-start md:border-t-0 md:border-r md:py-3 md:gap-1"
    >
      <div className="md:mb-2">
        <WorkspaceMenu />
      </div>
      {railItems.map(({ to, label, icon: Icon }) => {
        // Only the Home item carries a badge, so only it needs a dynamic
        // accessible name — every other item's aria-label is just its label.
        const ariaLabel = to === "/" && unread > 0 ? `${label} (${unread} unread)` : label;
        const active = isRailActive(to, pathname);
        return (
          <Tooltip key={to}>
            <TooltipTrigger asChild>
              <NavLink
                to={to}
                aria-label={ariaLabel}
                className={cn(
                  "relative flex size-11 items-center justify-center rounded-lg transition-colors",
                  // active:scale-95 gives the press a tactile beat. It sits
                  // under index.css's global prefers-reduced-motion rule,
                  // which flattens the transition for anyone who asked the
                  // OS not to move things.
                  "active:scale-95",
                  active
                    ? "bg-accent-soft text-accent"
                    : "text-muted hover:bg-muted-surface hover:text-foreground",
                )}
              >
                {/* The active marker is a SHAPE, not only a tint: a bar on the
                    rail's inner edge (left on the desktop vertical rail, top
                    on the mobile bottom bar). Colour alone at this size was
                    the thing that read as "no indication". */}
                {active && (
                  <span
                    data-testid="rail-active-bar"
                    aria-hidden="true"
                    className="absolute inset-x-2 top-0 h-[3px] rounded-full bg-accent
                               md:inset-x-auto md:top-auto md:left-0 md:h-6 md:w-[3px]"
                  />
                )}
                {/* stroke-[2.25]: a lucide glyph is stroked, not a font —
                    "bold" here means widening the stroke, which a font-weight
                    cannot do to an SVG. */}
                <Icon className="size-5 stroke-[2.25]" />
                {to === "/" && <UnreadBadge count={unread} />}
              </NavLink>
            </TooltipTrigger>
            <TooltipContent side="right">{label}</TooltipContent>
          </Tooltip>
        );
      })}
      <div className="md:mt-auto">
        <Tooltip>
          <TooltipTrigger asChild>
            <NavLink
              to="/settings"
              aria-label="Profile & Settings"
              className={cn(
                "flex size-11 items-center justify-center rounded-lg transition-colors active:scale-95",
                isRailActive("/settings", pathname) ? "bg-accent-soft" : "hover:bg-muted-surface",
              )}
            >
              <Avatar className="size-9">
                <AvatarFallback className="bg-accent-soft text-accent text-sm font-semibold">
                  <ProfileInitial />
                </AvatarFallback>
              </Avatar>
            </NavLink>
          </TooltipTrigger>
          <TooltipContent side="right">Profile &amp; Settings</TooltipContent>
        </Tooltip>
      </div>
    </nav>
  );
}

function ProfileInitial() {
  const { data } = useSession();
  return <>{data?.owner?.username?.[0]?.toUpperCase() ?? "?"}</>;
}
