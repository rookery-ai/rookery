import { createContext, useContext, useEffect } from "react";

// A page-level composer docked to the bottom of the content area (the chats
// page and both designers) sits exactly where the shell's floating action
// buttons are, so its Send button ends up underneath them. The 10% side gutter
// clears the FABs on a wide window but not below ~1100px, so the FABs move
// instead of the button.
//
// This lives in its own module rather than in AppShell.tsx purely to break an
// import cycle: Composer would otherwise import AppShell, which imports
// GlobalChatButton → ChatWindow → Composer. The cycle happened to resolve at
// runtime, but it is exactly the kind of thing that turns into an `undefined is
// not a function` under a different chunking strategy.
export const DockedComposerCtx = createContext<{ register: () => () => void }>({
  // No-op default so a Composer rendered outside the shell (component tests,
  // the slide-over) neither throws nor moves anything.
  register: () => () => {},
});

/**
 * Registers this composer as the page's docked bottom bar for as long as it is
 * mounted and `active`. Counted by the shell rather than tracked as a boolean:
 * during a route change the incoming composer mounts before the outgoing one
 * unmounts, and a boolean would be left false by that trailing cleanup.
 */
export function useDockedComposer(active: boolean) {
  const { register } = useContext(DockedComposerCtx);
  useEffect(() => {
    if (!active) return;
    return register();
  }, [active, register]);
}
