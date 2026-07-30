import {
  Activity, Bell, Bot, Boxes, BrainCircuit, Building2, FileText, Folder,
  HardDriveDownload, House, Inbox, KeyRound, Library, Link2, Lock,
  MessageSquare, Plug, ScrollText, Settings, Shield, Sparkles, SunMoon,
  UserRound, Wrench, type LucideIcon,
} from "lucide-react";

// The single source of truth for "which icon means which thing".
//
// Four consumers read this map — the icon rail, PageTitle, the command
// palette's kind labels, and the settings section nav — which is the whole
// point: a page and its rail entry can no longer show different icons.
//
// Before this existed, SettingsPage carried EMOJI strings for its section nav
// (👤 🏠 🧠 ⚙️ 🔐 🌓 🛡) while every other surface used monochrome lucide. That
// single divergence is what read as "the settings page is coloured and
// everything else is grey".
//
// Rules:
//   - lucide only
//   - currentColor always; never a coloured icon except semantic status, which
//     uses text-danger / text-warn / text-ok
//   - size-4 inline, size-5 in a page title
//   - strokeWidth stays at lucide's default 2, never overridden per site
//
// The ONE exception in the app is components/brand/ProviderLogo.tsx, which
// keeps full brand colour: a monochrome Slack or Google mark is harder to
// recognise than a coloured one, which defeats the purpose of a logo.
export const ENTITY_ICONS = {
  // Rail destinations. These reuse the rail's existing choices rather than
  // renaming them, so the change is a consolidation and not a reskin.
  home: House,
  kb: Library,
  agents: Bot,
  skills: Sparkles,
  connections: Plug,
  chats: MessageSquare,
  secrets: KeyRound,
  settings: Settings,

  // Command-palette result kinds. "notes" is a search hit (a document) while
  // "kb" above is the section, so they are deliberately different glyphs.
  notes: FileText,
  note: FileText,
  folder: Folder,
  reminders: Bell,
  inbox: Inbox,

  // Workspace-scoped settings sections.
  profile: UserRound,
  workspace: Building2,
  "ai-providers": BrainCircuit,
  coder: Wrench,
  "master-password": Lock,
  appearance: SunMoon,

  // Owner-scoped settings sections. `owner` is the group; the five below are
  // its sections, each its own page after the Owner split.
  owner: Shield,
  "owner-workspaces": Boxes,
  "owner-instance-url": Link2,
  "owner-system": Activity,
  "owner-backup": HardDriveDownload,
  "owner-audit": ScrollText,
} as const satisfies Record<string, LucideIcon>;

export type EntityKind = keyof typeof ENTITY_ICONS;

// Unknown kinds degrade to a neutral glyph rather than throwing: the command
// palette receives kind strings from the server, so a kind added server-side
// must not blank the whole result list on an older frontend.
export function entityIcon(kind: string): LucideIcon {
  return (ENTITY_ICONS as Record<string, LucideIcon>)[kind] ?? FileText;
}
