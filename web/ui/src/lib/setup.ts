import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { APIProvider, CoderCatalogEntry, DetectedCoder } from "./settings";
import type { ConnectorPlatform } from "./connections";

// Mirrors web/api_settings.go's apiGetSetup response. The step-conditional
// fields only arrive when `step` (the server's authoritative, recomputed-
// every-GET progression indicator) equals the step that needs them — the
// wizard caches each one locally the first time it sees it (see
// SetupWizard's coderData/connectorData/botUsername state) so Back-
// navigating to an already-completed step doesn't lose the supplemental
// data (a plain re-GET at that point reflects the CURRENT — later — step,
// not the one being revisited).
export type SetupResponse = {
  step: number;
  needs_setup: boolean;
  detected_coders?: DetectedCoder[];
  api_providers?: APIProvider[];
  coder_catalog?: CoderCatalogEntry[];
  // "slim" builds ship no CLI coder binary; the wizard hides the local engine.
  coder_mode?: "full" | "slim";
  platforms?: ConnectorPlatform[];
  // Step-7 summary of the connected chat app. Replaces the old
  // `bot_username`, which read a Telegram-only setting key and therefore left
  // a Discord install with no bot name and no linking instruction at all.
  // Absent when the operator skipped the chat-app step.
  platform?: string;
  platform_label?: string;
  bot_identity?: string;
  linked?: boolean;
  linked_identity?: string;
  dm_url?: string;
  invite_url?: string;
  bot_online?: boolean;
};

export function useSetupQuery() {
  return useQuery({
    queryKey: ["setup"],
    queryFn: () => api.get<SetupResponse>("/api/v1/setup"),
  });
}

// Mirrors apiSetupOK's envelope — every step POST returns this shape. Each
// wizard step posts its own apiSetupRequest-shaped body directly via `api`
// (see SetupWizard.tsx) rather than through a shared mutation hook — the
// coder step is the one exception, using its own useMutation so it can be
// handed to CoderSection's `saveOverride` prop.
export type SetupStepResponse = { ok: boolean; next_step: number };
