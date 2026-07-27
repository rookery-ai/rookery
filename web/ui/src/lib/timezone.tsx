import { createContext, useContext, type ReactNode } from "react";
import { useDisplayTimeZone } from "./session";

// The display timezone reaches chat bubbles as CONTEXT, not as a `useSession()`
// call inside the bubble itself.
//
// ChatMessageBubble is rendered by the agent/skill DesignerSurface and by bare
// component tests that mount it with no QueryClientProvider above them; a
// useQuery call down there throws ("No QueryClient set"). A context with an
// undefined default degrades silently to the browser's own zone instead, which
// is exactly the fallback formatMessageTime already implements — so the wrong
// answer is impossible and the missing-provider case is merely less precise.
export const TimeZoneContext = createContext<string | undefined>(undefined);

// Mounted once at the app root (inside QueryClientProvider, which it needs).
export function TimeZoneProvider({ children }: { children: ReactNode }) {
  const tz = useDisplayTimeZone();
  return <TimeZoneContext.Provider value={tz}>{children}</TimeZoneContext.Provider>;
}

export function useTimeZone(): string | undefined {
  return useContext(TimeZoneContext);
}
