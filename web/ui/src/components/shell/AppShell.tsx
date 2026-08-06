import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { Outlet } from "react-router";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { GlobalChatButton } from "@/components/chat/GlobalChatButton";
import { CommandPalette, GlobalSearchButton } from "@/components/search/CommandPalette";
import { useRailShortcuts } from "@/lib/useKeyboardNav";
import IconRail from "./IconRail";
import { PaneResizeHandle, usePaneWidth } from "./usePaneWidth";
import { ShortcutsOverlay } from "./ShortcutsOverlay";
import { ToastProvider, ToastHost } from "./Toast";
import { DockedComposerCtx } from "./dockedComposer";

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
  const [dockedComposers, setDockedComposers] = useState(0);
  const [contextPane, setContextPane] = useState<React.ReactNode | null>(null);
  // Held here rather than inside CommandPalette so the search FAB — which
  // lives in this subtree, stacked with the chat button — can open it. The
  // palette keeps its own ⌘K listener and routes it through this setter.
  const [searchOpen, setSearchOpen] = useState(false);
  const { width, setWidth, reset } = usePaneWidth();
  useRailShortcuts();

  const openPanel = useCallback(
    (node: React.ReactNode, opts?: { title?: string }) => setPanel({ node, title: opts?.title }),
    [],
  );
  const closePanel = useCallback(() => setPanel(null), []);
  const registerDockedComposer = useCallback(() => {
    setDockedComposers((n) => n + 1);
    return () => setDockedComposers((n) => Math.max(0, n - 1));
  }, []);

  const value = useMemo(
    () => ({ openPanel, closePanel, setContextPane }),
    [openPanel, closePanel, setContextPane],
  );
  const dockedValue = useMemo(() => ({ register: registerDockedComposer }), [registerDockedComposer]);

  return (
    <ShellCtx.Provider value={value}>
      <DockedComposerCtx.Provider value={dockedValue}>
      <ToastProvider>
        <TooltipProvider>
          {/* overflow-hidden: the shell is a fixed-height frame and every
              scrolling region inside it is explicit. Without this, tall content
              in a pane (a long KB note) propagates its scrollable overflow to
              the initial containing block, making the DOCUMENT scrollable — and
              a scrollable document means the rail and context pane can be
              scrolled out of view, which is never wanted on any route.
              relative is what makes that overflow-hidden actually bite. An
              overflow value clips a descendant only when the clipping element
              is in that descendant's containing-block chain; a static shell is
              not, so a long note's overflowing content escaped to the initial
              containing block and became the ROOT element's scroll box —
              measured at <html> clientHeight 900 against scrollHeight 13425.
              The visible symptom was wheeling over the icon rail scrolling the
              whole page. Setting overflow:hidden on html/body instead only
              suppresses the user's scroll and leaves the oversized document in
              place. */}
          <div className="relative h-screen overflow-hidden flex flex-col md:flex-row bg-background">
            <IconRail />
            {contextPane && (
              <aside
                className="hidden md:flex relative shrink-0 flex-col border-r border-border bg-chrome/60 overflow-y-auto"
                style={{ width }}
              >
                {contextPane}
                <PaneResizeHandle width={width} setWidth={setWidth} reset={reset} />
              </aside>
            )}
            <main className="flex-1 min-w-0 overflow-y-auto pb-16 md:pb-0">
              <Outlet />
            </main>
            {/* flex-col-reverse: the chat button is the primary action and
                stays anchored at the bottom, with search sitting above it.
                GlobalChatButton renders null on /chats, and the stack simply
                closes up rather than leaving a hole. */}
            <div
              className={cn(
                "fixed right-4 md:right-6 z-30 flex flex-col-reverse gap-3",
                dockedComposers > 0 ? "bottom-32 md:bottom-24" : "bottom-20 md:bottom-6",
              )}
            >
              <GlobalChatButton />
              <GlobalSearchButton onClick={() => setSearchOpen(true)} />
            </div>
            <CommandPalette open={searchOpen} onOpenChange={setSearchOpen} />
            <ShortcutsOverlay />
            <ToastHost />
            <Sheet open={panel !== null} onOpenChange={(o) => !o && setPanel(null)}>
              {/* gap-0 + no padding on the content well: panel content owns its
                  own inner padding (see ChatWindow's "container-agnostic"
                  note) — a shell-level p-4/gap-4 would double up chrome for
                  any full-height embed like the global chat panel. */}
              <SheetContent side="right" className="w-[clamp(400px,33vw,720px)] max-w-full p-0 gap-0 flex flex-col">
                <SheetHeader className="border-b border-border px-4 py-3">
                  <SheetTitle className="text-sm font-bold">{panel?.title ?? ""}</SheetTitle>
                </SheetHeader>
                <div className="flex-1 min-h-0 overflow-y-auto">{panel?.node}</div>
              </SheetContent>
            </Sheet>
          </div>
        </TooltipProvider>
      </ToastProvider>
      </DockedComposerCtx.Provider>
    </ShellCtx.Provider>
  );
}
