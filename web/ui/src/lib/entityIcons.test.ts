import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { ENTITY_ICONS, entityIcon } from "./entityIcons";

const here = path.dirname(fileURLToPath(import.meta.url));

// Every kind any of the four consumers can ask for. A missing key here means a
// surface silently renders the fallback glyph instead of the right one.
const REQUIRED = [
  "home", "kb", "agents", "skills", "connections", "chats", "secrets", "settings",
  "notes", "note", "folder", "reminders", "inbox",
  "profile", "workspace", "ai-providers", "coder", "master-password", "appearance",
  "owner", "owner-workspaces", "owner-instance-url", "owner-system",
  "owner-backup", "owner-audit",
] as const;

test("every entity kind the app navigates to has an icon", () => {
  for (const k of REQUIRED) {
    expect(ENTITY_ICONS[k], `missing icon for "${k}"`).toBeTruthy();
  }
});

test("entityIcon falls back rather than crashing on an unknown kind", () => {
  // The command palette receives kind strings from the server; a kind added
  // server-side must not blank the whole result list on an older frontend.
  expect(entityIcon("a-kind-from-the-future")).toBeTruthy();
});

test("the settings section nav uses the icon map, not emoji", () => {
  // Regression guard for the exact drift this map fixed: SettingsPage used
  // emoji strings while every other surface used monochrome lucide.
  const src = readFileSync(path.join(here, "../pages/settings/SettingsPage.tsx"), "utf8");
  expect(src).not.toMatch(/icon:\s*"[^"]*[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}\u{1F000}-\u{1F0FF}]/u);
});

test("the icon rail reads the shared map instead of importing lucide directly", () => {
  const src = readFileSync(path.join(here, "../components/shell/IconRail.tsx"), "utf8");
  expect(src).toMatch(/entityIcons/);
});
