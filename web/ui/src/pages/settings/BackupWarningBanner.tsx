import { useState } from "react";
import { AlertTriangle, ShieldCheck, X } from "lucide-react";
import { useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { useBackupConfig } from "@/lib/backup";

// Dismissal is permanent and is deliberately never cleared — not when backups
// are enabled, and not if they are later turned off again. An owner who has
// said "not now" once has answered, and a warning that reappears after being
// dismissed is what teaches people to ignore banners.
export const BACKUP_WARNING_DISMISSED_KEY = "sa.backupWarningDismissed";

function readDismissed() {
  try {
    return localStorage.getItem(BACKUP_WARNING_DISMISSED_KEY) === "1";
  } catch {
    // Storage disabled or full: show the warning rather than crash the section.
    return false;
  }
}

export function BackupWarningBanner() {
  const { data } = useBackupConfig();
  const [dismissed, setDismissed] = useState(readDismissed);
  const [searchParams, setSearchParams] = useSearchParams();

  // Both halves matter: a passphrase with automatic runs switched off means no
  // snapshot is ever taken, which is the case this warning exists for.
  const configured = Boolean(data?.passphrase_set) && Boolean(data?.enabled);
  if (dismissed || !data || configured) return null;

  function dismiss() {
    try {
      localStorage.setItem(BACKUP_WARNING_DISMISSED_KEY, "1");
    } catch {
      // Nothing to do — the banner still hides for this mount.
    }
    setDismissed(true);
  }

  function goToBackup() {
    const next = new URLSearchParams(searchParams);
    next.set("section", "owner-backup");
    setSearchParams(next);
  }

  return (
    <div className="mb-4 rounded-md bg-warn-soft p-3 text-sm text-warn">
      <div className="flex items-center gap-2 font-bold">
        <AlertTriangle className="size-4 shrink-0" />
        Backups are not enabled
      </div>
      <p className="mt-1">
        This install has no snapshot of its database or knowledge bases. Copying
        the data folder is not a substitute — it leaves the encryption key
        behind.
      </p>
      <div className="mt-2 flex gap-2">
        <Button size="sm" onClick={goToBackup}>
          <ShieldCheck />
          Set up backups
        </Button>
        <Button size="sm" variant="ghost" onClick={dismiss}>
          <X />
          Dismiss
        </Button>
      </div>
    </div>
  );
}
