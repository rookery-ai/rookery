import type { ReactNode } from "react";
import { AlertTriangle, Check, Loader2 } from "lucide-react";

// Small status primitives shared by every chat-connect step. They live here
// rather than in ChatAppWizard so the setup wizard renders the identical
// affordances — the two surfaces looking different is the shape of the bug
// this module exists to prevent.

export function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {children}
    </div>
  );
}

export function WarningNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
      <div>{children}</div>
    </div>
  );
}

export function OkNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-ok-soft px-3 py-2 text-sm font-medium text-ok">
      <Check className="size-4 shrink-0" />
      {children}
    </div>
  );
}

export function Spinner({ text }: { text: string }) {
  return (
    <div className="flex items-center gap-2 text-sm text-muted-2">
      <Loader2 className="size-4 shrink-0 animate-spin" />
      {text}
    </div>
  );
}

/**
 * Test-connection result, shared by the connect wizard's test step, the setup
 * wizard's test phase and the Manage panel.
 */
export function TestResult({
  platform,
  pending,
  ok,
  identity,
  error,
}: {
  platform: string;
  pending: boolean;
  ok: boolean | null;
  identity?: string;
  error?: string;
}) {
  if (pending) return <Spinner text="Checking the connection…" />;
  if (ok === true) return <OkNote>Connected as {identity ?? platform} ✓</OkNote>;
  if (ok === false) return <ErrorNote>{error ?? "Connection failed"}</ErrorNote>;
  return null;
}
