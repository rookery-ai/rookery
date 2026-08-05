import { useCallback, useState } from "react";
import { api, ApiError } from "./api";

/** The code an install-level route answers with until the owner has re-entered
 *  their password within the server's TTL. Shared with OwnerGate. */
export const OWNER_GATE_CODE = "owner_verification_required";

export function isOwnerGateError(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.status === 403 &&
    err.code === OWNER_GATE_CODE
  );
}

/**
 * Runs an install-level action, prompting for the owner password only if the
 * server says it needs one.
 *
 * The shape is **attempt → catch the gate → prompt → retry**, deliberately not
 * "read session.owner_verified and decide whether to show a password field".
 * OwnerGate already states the rule this follows: the server owns expiry, and a
 * clock here could only disagree with it — a cached session can predate the
 * lapse, and the TTL can expire between rendering the form and submitting it.
 * A pre-check would need this 403 path anyway, so it would add a second,
 * disagreeing source of truth and remove nothing.
 *
 * Usage: call `run(fn)`. If it returns without setting `needsPassword`, the
 * action already went through. Otherwise render a password step and call
 * `verify(password)`, which confirms and then re-runs the SAME `fn` — so the
 * caller's already-collected input is carried through rather than re-asked.
 */
export function useOwnerVerify() {
  const [needsPassword, setNeedsPassword] = useState(false);
  const [pending, setPending] = useState<(() => Promise<void>) | null>(null);
  const [busy, setBusy] = useState(false);

  const run = useCallback(async (fn: () => Promise<void>) => {
    setBusy(true);
    try {
      await fn();
      setNeedsPassword(false);
      setPending(null);
    } catch (err) {
      if (!isOwnerGateError(err)) throw err;
      // Stored as a thunk: useState treats a bare function argument as a
      // lazy initializer and would CALL it, firing the gated action again.
      setPending(() => fn);
      setNeedsPassword(true);
    } finally {
      setBusy(false);
    }
  }, []);

  /** Confirms the owner password, then retries the action that was refused.
   *  Throws on a wrong password (and on whatever the retried action throws),
   *  so the caller renders the error where it belongs. */
  const verify = useCallback(
    async (password: string) => {
      setBusy(true);
      try {
        await api.post("/api/v1/auth/owner-verify", { password });
        if (pending) await pending();
        setNeedsPassword(false);
        setPending(null);
      } finally {
        setBusy(false);
      }
    },
    [pending],
  );

  /** Drops any pending action — call when the surrounding dialog closes, so a
   *  reopened dialog starts at the name step rather than mid-retry. */
  const reset = useCallback(() => {
    setNeedsPassword(false);
    setPending(null);
    setBusy(false);
  }, []);

  return { run, verify, reset, needsPassword, busy };
}
