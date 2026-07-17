import { useEffect, useState, type ComponentType } from "react";
import { useNavigate } from "react-router";
import {
  Bell, Bot, FileText, KeyRound, Loader2, MessageCircleQuestion, MessageSquare, Plug, Plus, Settings, Sparkles,
} from "lucide-react";
import {
  Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useSlideOver } from "@/components/shell/AppShell";
import { GlobalChatPanel } from "@/components/chat/GlobalChatButton";
import { useGlobalSearch, type SearchItem } from "@/lib/search";

const DEBOUNCE_MS = 200;

// kind -> group label + icon, per web/api_search.go's groups.
const KIND_META: Record<string, { label: string; icon: ComponentType<{ className?: string }> }> = {
  notes: { label: "Notes", icon: FileText },
  agents: { label: "Agents", icon: Bot },
  chats: { label: "Chats", icon: MessageSquare },
  skills: { label: "Skills", icon: Sparkles },
  connections: { label: "Connections", icon: Plug },
  secrets: { label: "Secrets", icon: KeyRound },
  reminders: { label: "Reminders", icon: Bell },
};

// The backend's `url` field is a template-era path — most kinds already
// match the SPA route 1:1, but a few need remapping: chats point at a
// per-chat detail page that doesn't exist in the SPA (chats live behind
// /chats?chat=<id>), and connections/secrets/reminders don't have a
// per-item route at all, only a list page.
function resolveUrl(kind: string, item: SearchItem): string {
  switch (kind) {
    case "chats":
      return item.id ? `/chats?chat=${item.id}` : "/chats";
    case "connections":
      return "/connections";
    case "secrets":
      return "/secrets";
    case "reminders":
      return "/";
    default:
      return item.url ?? "/";
  }
}

type Action = { id: string; label: string; icon: ComponentType<{ className?: string }>; onSelect: () => void };

// Wraps the first case-insensitive occurrence of `query` in `text` with
// <mark> — same approach (and same title-vs-snippet split: only the snippet
// gets marked, the title/path stays plain) as pages/kb/SearchBox.tsx's
// highlight(). Applied against the debounced `query` that actually produced
// these results (not the live `input`), so the mark never flashes ahead of
// what was searched.
function highlight(text: string, query: string) {
  if (!query) return text;
  const idx = text.toLowerCase().indexOf(query.toLowerCase());
  if (idx === -1) return text;
  return (
    <>
      {text.slice(0, idx)}
      <mark>{text.slice(idx, idx + query.length)}</mark>
      {text.slice(idx + query.length)}
    </>
  );
}

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const { open: openSlideOver } = useSlideOver();

  // Global Ctrl/Cmd+K — same input-focus guard as GlobalChatButton's Ctrl+J
  // so typing "k" while editing text elsewhere never hijacks the keystroke.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      const isShortcut = (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k";
      if (!isShortcut) return;
      const target = e.target as HTMLElement | null;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)) return;
      e.preventDefault();
      setOpen(true);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // Reset the query whenever the palette closes so reopening starts fresh
  // instead of showing stale results for a moment.
  useEffect(() => {
    if (!open) {
      setInput("");
      setQuery("");
    }
  }, [open]);

  useEffect(() => {
    const t = window.setTimeout(() => setQuery(input), DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [input]);

  const { data, isFetching } = useGlobalSearch(query);
  const groups = data?.groups ?? [];

  function go(url: string) {
    setOpen(false);
    navigate(url);
  }

  function askAssistant() {
    const q = input.trim();
    setOpen(false);
    openSlideOver(<GlobalChatPanel initialText={q} />, { title: "Chat" });
  }

  const trimmed = input.trim();
  const actions: Action[] = [
    { id: "new-agent", label: "New agent", icon: Plus, onSelect: () => go("/agents/new") },
    { id: "new-note", label: "New note", icon: Plus, onSelect: () => go("/kb") },
    { id: "open-settings", label: "Open settings", icon: Settings, onSelect: () => go("/settings") },
    ...(trimmed
      ? [{
          id: "ask-assistant",
          label: `Ask assistant about '${trimmed}'`,
          icon: MessageCircleQuestion,
          onSelect: askAssistant,
        }]
      : []),
  ];
  // Actions are static (not server-driven) so they get their own client-side
  // filter against the raw (undebounced) input — instant, no network round
  // trip needed to narrow a fixed list of ~4 items.
  const lq = trimmed.toLowerCase();
  const visibleActions = lq ? actions.filter((a) => a.label.toLowerCase().includes(lq)) : actions;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="top-[20%] max-w-xl translate-y-0 gap-0 overflow-hidden p-0" showCloseButton={false}>
        <DialogHeader className="sr-only">
          <DialogTitle>Search</DialogTitle>
          <DialogDescription>Search notes, agents, chats, skills, connections, secrets, and reminders</DialogDescription>
        </DialogHeader>
        <Command shouldFilter={false} className="rounded-none">
          <div className="flex items-center border-b border-border">
            <CommandInput
              value={input}
              onValueChange={setInput}
              placeholder="Search or run a command…"
              className="flex-1"
            />
            {isFetching && <Loader2 className="mr-3 size-4 shrink-0 animate-spin text-muted-2" />}
          </div>
          <CommandList>
            <CommandEmpty>{query ? "No results." : "Type to search…"}</CommandEmpty>
            {groups.map((group) => {
              const meta = KIND_META[group.kind] ?? { label: group.kind, icon: FileText };
              const Icon = meta.icon;
              return (
                <CommandGroup key={group.kind} heading={meta.label}>
                  {group.items.map((item) => {
                    const key = `${group.kind}:${item.id ?? item.path ?? item.title}`;
                    return (
                      <CommandItem key={key} value={key} onSelect={() => go(resolveUrl(group.kind, item))}>
                        <Icon className="size-4" />
                        <div className="flex min-w-0 flex-col">
                          <span className="truncate">{item.title}</span>
                          {item.snippet && (
                            <span className="truncate text-xs text-muted-2">{highlight(item.snippet, query)}</span>
                          )}
                        </div>
                      </CommandItem>
                    );
                  })}
                </CommandGroup>
              );
            })}
            {groups.length > 0 && visibleActions.length > 0 && <CommandSeparator />}
            {visibleActions.length > 0 && (
              <CommandGroup heading="Actions">
                {visibleActions.map((a) => (
                  <CommandItem key={a.id} value={a.id} onSelect={a.onSelect}>
                    <a.icon className="size-4" />
                    <span>{a.label}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

export default CommandPalette;
