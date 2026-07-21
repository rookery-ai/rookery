import { Sparkles } from "lucide-react";

// The first thing a fresh design session shows. Before this, the transcript
// was empty until the user's first message came back — a blank page with a
// chatbox, giving no signal that the session had started or what to type.
//
// Rendered by DesignerSurface via its `intro` prop, deliberately OUTSIDE the
// `messages` array: it is a static affordance, not a fabricated assistant
// turn, so it is never sent to the server, never persisted into a draft's
// history, and disappears as soon as a real message arrives.
//
// Entity-agnostic (the surface is shared by the agent and skill designers) —
// wording comes from the caller.
export function DesignerIntro({
  title,
  blurb,
  examples,
}: {
  title: string;
  blurb: string;
  examples: string[];
}) {
  return (
    <div className="max-w-[78%] self-start rounded-lg border border-border bg-chrome/60 p-4">
      <div className="flex items-center gap-2">
        <Sparkles className="size-4 shrink-0 text-accent" />
        <h2 className="text-sm font-semibold">{title}</h2>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-muted-2">{blurb}</p>
      {examples.length > 0 && (
        <>
          <p className="mt-3 text-xs font-semibold uppercase tracking-wide text-muted-2">
            For example
          </p>
          <ul className="mt-1.5 space-y-1.5">
            {examples.map((e) => (
              <li key={e} className="text-sm leading-relaxed text-muted-2">
                &ldquo;{e}&rdquo;
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

export default DesignerIntro;
