import { createContext, useContext, useState } from "react";
import { Outlet } from "react-router";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { TooltipProvider } from "@/components/ui/tooltip";
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

export function AppShell() {
  const [panel, setPanel] = useState<SlideOverState>(null);
  const [contextPane, setContextPane] = useState<React.ReactNode | null>(null);

  return (
    <ShellCtx.Provider
      value={{
        openPanel: (node, opts) => setPanel({ node, title: opts?.title }),
        closePanel: () => setPanel(null),
        setContextPane,
      }}
    >
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
          <Sheet open={panel !== null} onOpenChange={(o) => !o && setPanel(null)}>
            <SheetContent side="right" className="w-full sm:max-w-md p-0 flex flex-col">
              <SheetHeader className="border-b border-border px-4 py-3">
                <SheetTitle className="text-sm font-bold">{panel?.title ?? ""}</SheetTitle>
              </SheetHeader>
              <div className="flex-1 overflow-y-auto p-4">{panel?.node}</div>
            </SheetContent>
          </Sheet>
        </div>
      </TooltipProvider>
    </ShellCtx.Provider>
  );
}
