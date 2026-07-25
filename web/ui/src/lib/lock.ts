import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// useLockUI locks the screen without leaving the workspace.
//
// The lock is server-side: POST /auth/lock flips a session flag and every
// guarded route then answers 423 until the master password is re-entered. All
// this hook has to do afterwards is refetch the session, which is what makes
// the SPA render the lock screen.
export function useLockUI() {
  const qc = useQueryClient();
  const [locking, setLocking] = useState(false);

  async function lock() {
    setLocking(true);
    try {
      await api.post<{ ok: boolean }>("/api/v1/auth/lock", {});
      await qc.invalidateQueries({ queryKey: ["session"] });
    } finally {
      setLocking(false);
    }
  }

  return { lock, locking };
}
