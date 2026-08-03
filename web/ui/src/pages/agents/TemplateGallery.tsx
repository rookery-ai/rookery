import { useMemo, useState } from "react";
import { Search } from "lucide-react";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { SearchInput } from "@/components/ui/search-input";
import {
  AGENT_TEMPLATES, SCRATCH_TEMPLATE_ID, templateMatches,
  type AgentTemplate, type TemplateCategory,
} from "./templates";

// TemplateGallery is the "View all templates" modal: the whole library, grouped
// by category and searchable across label/blurb/category/keywords/description
// (see templateMatches) so a template can be found by the KIND of job it does,
// not just its title.
//
// It renders the library directly rather than taking it as a prop — there is
// exactly one library and one caller, and threading it through would only add a
// seam with no second implementation behind it. The escape-hatch
// "Start from scratch" entry is excluded: it lives on the start screen as a
// first-class choice and is not something you browse a gallery for.
export default function TemplateGallery({
  open,
  onOpenChange,
  onSelect,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // Selecting hands the template back to the page, which routes it through the
  // SAME selectTemplate() path as the start-screen cards — so the
  // unsaved-custom-text confirmation guard applies here too.
  onSelect: (template: AgentTemplate) => void;
}) {
  const [query, setQuery] = useState("");

  // Grouped in the library's own order, so related templates stay adjacent and
  // a category only appears when it has a match.
  const groups = useMemo(() => {
    const out: Array<{ category: TemplateCategory; templates: AgentTemplate[] }> = [];
    for (const t of AGENT_TEMPLATES) {
      if (t.id === SCRATCH_TEMPLATE_ID) continue;
      if (!templateMatches(t, query)) continue;
      const existing = out.find((g) => g.category === t.category);
      if (existing) existing.templates.push(t);
      else out.push({ category: t.category, templates: [t] });
    }
    return out;
  }, [query]);

  const matchCount = groups.reduce((n, g) => n + g.templates.length, 0);

  function choose(t: AgentTemplate) {
    onSelect(t);
    onOpenChange(false);
    setQuery("");
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[80vh] max-w-2xl flex-col gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b border-border px-4 py-3">
          <DialogTitle>Choose a template</DialogTitle>
        </DialogHeader>

        <div className="border-b border-border px-4 py-3">
          <div className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-2" />
            <SearchInput
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search by what it does — email, downtime, weekly…"
              aria-label="Search templates"
              className="pl-8"
            />
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {matchCount === 0 ? (
            <p className="py-8 text-center text-sm text-muted-2">
              No templates match “{query}”. Try a different word, or start from scratch.
            </p>
          ) : (
            groups.map((group) => (
              <div key={group.category} className="mb-5 last:mb-0">
                <h3 className="mb-2 text-xs font-semibold tracking-wide text-muted-2 uppercase">
                  {group.category}
                </h3>
                <div className="space-y-1.5">
                  {group.templates.map((t) => (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() => choose(t)}
                      // Hover is a soft accent TINT, not `bg-accent/40`. A 40%
                      // wash of the accent (a dark blue in light mode, a light
                      // blue in dark) sat under this card's unchanged
                      // text-foreground title and text-muted-2 blurb, so the
                      // row went unreadable on hover in both themes.
                      className="w-full rounded-md border border-border p-3 text-left transition-colors hover:border-accent/40 hover:bg-accent-soft focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
                    >
                      <div className="text-sm font-medium text-foreground">{t.label}</div>
                      <div className="mt-0.5 text-xs text-muted-2">{t.blurb}</div>
                      {/* A preview of the actual brief — the thing that makes
                          the template worth picking — clamped so a long one
                          doesn't dominate the list. */}
                      <p className="mt-1.5 line-clamp-2 text-xs text-muted-2/80">{t.description}</p>
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
