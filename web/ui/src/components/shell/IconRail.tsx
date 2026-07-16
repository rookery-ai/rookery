import { NavLink } from "react-router";
import {
  House, Library, Bot, Sparkles, Plug, MessageSquare, KeyRound,
} from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session";
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

export default function IconRail() {
  return (
    <nav
      aria-label="Primary"
      className="fixed bottom-0 inset-x-0 z-20 flex flex-row items-center justify-around border-t border-border bg-chrome px-2 py-1
                 md:static md:h-full md:w-14 md:flex-col md:justify-start md:border-t-0 md:border-r md:py-3 md:gap-1.5"
    >
      <div className="md:mb-2">
        <WorkspaceMenu />
      </div>
      {railItems.map(({ to, label, icon: Icon }) => (
        <Tooltip key={to}>
          <TooltipTrigger asChild>
            <NavLink
              to={to}
              aria-label={label}
              className={({ isActive }) =>
                `flex size-9 items-center justify-center rounded-lg transition-colors ${
                  isActive ? "bg-border text-foreground" : "text-muted hover:bg-border/60"
                }`
              }
            >
              <Icon className="size-[18px]" />
            </NavLink>
          </TooltipTrigger>
          <TooltipContent side="right">{label}</TooltipContent>
        </Tooltip>
      ))}
      <div className="md:mt-auto">
        <Tooltip>
          <TooltipTrigger asChild>
            <NavLink to="/settings" aria-label="Profile & Settings">
              <Avatar className="size-8">
                <AvatarFallback className="bg-accent-soft text-accent text-xs font-semibold">
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
