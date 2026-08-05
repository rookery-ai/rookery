import { useState } from "react";
import { LogOut } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

/**
 * Ends the owner session.
 *
 * Mounted by the two screens that render OUTSIDE AppShell — the workspace
 * picker and the lock screen. It is deliberately absent inside the shell: every
 * page owns its own top-right (a search box and a primary action in a
 * justify-between header row), so a fixed button there would land on "New
 * agent". Leaving a workspace returns here, where this lives.
 *
 * Fixed to the viewport corner rather than placed inside either card, which is
 * what lets one component serve a centered card on `bg-chrome` and a
 * `fixed inset-0` overlay. z-[60] clears LockScreen's own z-50.
 *
 * On `variant="destructive"`: the design system reserves it for actions that
 * remove data, and this removes none. It is used anyway because `destructive`
 * is the app's red and a visible red control is the requirement; a new colour
 * token would mean re-running contrast.test.ts to buy a shade nobody asked for.
 * Treat this as the sanctioned exception, not as drift.
 *
 * It works while the screen is locked, and that is not incidental: api_auth.go
 * exempts logout from the lock gate specifically so "the user can always escape
 * it". The escape hatch existed with no affordance until now.
 */
export function SignOutButton() {
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  async function signOut() {
    setBusy(true);
    try {
      await api.post("/api/v1/auth/logout", {});
      // Same discipline as a tenant switch (resetWorkspaceScopedCache): drop
      // every cached query but keep ["session"], whose own refetch is what
      // sends the route guard to /login. Clearing it instead would blank the
      // app behind a loading fallback on the way out.
      //
      // The redirect is the GUARD's job, not this handler's — declarative, like
      // every other redirect in router.tsx, and it covers a logged-out visitor
      // typing the URL too. Both screens this button mounts on are guarded:
      // the lock screen by RequireAuth, the picker by RequireOwnerSession.
      // The picker had no guard at all until that was added, which is why
      // signing out used to leave the owner sitting on it.
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
      await qc.invalidateQueries({ queryKey: ["session"] });
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  }

  return (
    <>
      <div className="fixed right-4 top-4 z-[60]">
        <Button variant="destructive" onClick={() => setConfirming(true)}>
          <LogOut />
          Sign out
        </Button>
      </div>

      {/* Confirmed rather than fired on click: a mis-click in a screen corner
          costs a re-login AND a workspace master-password re-entry. A plain
          confirm, not the deferred-delete/undo pattern used for inbox rows —
          session teardown cannot be un-done from the client. */}
      <Dialog open={confirming} onOpenChange={(o) => !o && setConfirming(false)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Sign out?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">
            You’ll need your owner password to sign back in, and each
            workspace’s master password to re-enter it.
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void signOut()}
            >
              <LogOut />
              {busy ? "Signing out…" : "Sign out"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
