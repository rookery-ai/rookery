import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { Outlet } from "react-router";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { TooltipProvider } from "@/components/ui/tooltip";
import { GlobalChatButton } from "@/components/chat/GlobalChatButton";
import { CommandPalette } from "@/components/search/CommandPalette";
import IconRail from "./IconRail";

type SlideOverState = { node: React.ReactNode; title?: string } | null;

const ShellCtx = createContext<{
  openPanel: (node: React.ReactNode, opts?: { title?: string }) => void;
  closePanel: () => void;
  setContextPane: (node: React.ReactNode | null) => void;
}>({ openPanel: () => {}, closePanel: () => {}, setContextPane: () => {} });

export function useSlideOver() {
  const { openPanel, closePanel } = useContext(ShellCtx);
  return { open: openPanel, close: closePanel };
}

// Call the returned setter only from an effect (useEffect), never during
// render — the argument is a ReactNode and setting state during render loops.
export function useContextPane() {
  return useContext(ShellCtx).setContextPane;
}

// Declarative wrapper around useContextPane: mount to publish content into
// the shell's context pane, unmount to clear it. Prefer this over the raw
// useContextPane escape hatch in page components.
export function ContextPane({ children }: { children: React.ReactNode }) {
  const setContextPane = useContextPane();
  useEffect(() => {
    setContextPane(children);
    return () => setContextPane(null);
  }, [children, setContextPane]);
  return null;
}

export function AppShell() {
  const [panel, setPanel] = useState<SlideOverState>(null);
  const [contextPane, setContextPane] = useState<React.ReactNode | null>(null);

  const openPanel = useCallback(
    (node: React.ReactNode, opts?: { title?: string }) => setPanel({ node, title: opts?.title }),
    [],
  );
  const closePanel = useCallback(() => setPanel(null), []);
  const value = useMemo(
    () => ({ openPanel, closePanel, setContextPane }),
    [openPanel, closePanel, setContextPane],
  );

  return (
    <ShellCtx.Provider value={value}>
      <TooltipProvider>
        <div className="h-screen flex flex-col md:flex-row bg-background">
          <IconRail />
          {contextPane && (
            <aside className="hidden md:flex w-64 shrink-0 flex-col border-r border-border bg-chrome/60 overflow-y-auto">
              {contextPane}
            </aside>
          )}
          <main className="flex-1 min-w-0 overflow-y-auto pb-16 md:pb-0">
            <Outlet />
          </main>
          <GlobalChatButton />
          <CommandPalette />
          <Sheet open={panel !== null} onOpenChange={(o) => !o && setPanel(null)}>
            {/* gap-0 + no padding on the content well: panel content owns its
                own inner padding (see ChatWindow's "container-agnostic"
                note) — a shell-level p-4/gap-4 would double up chrome for
                any full-height embed like the global chat panel. */}
            <SheetContent side="right" className="w-full sm:max-w-md p-0 gap-0 flex flex-col">
              <SheetHeader className="border-b border-border px-4 py-3">
                <SheetTitle className="text-sm font-bold">{panel?.title ?? ""}</SheetTitle>
              </SheetHeader>
              <div className="flex-1 min-h-0 overflow-y-auto">{panel?.node}</div>
            </SheetContent>
          </Sheet>
        </div>
      </TooltipProvider>
    </ShellCtx.Provider>
  );
}
