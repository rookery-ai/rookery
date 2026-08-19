import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ClipboardCheck } from "lucide-react";
import { MessageMeta } from "@/components/chat/MessageMeta";

export type ReviewCardProps = {
  title: string;
  subtitle: string;
  content: string;
  createdAt?: string;
  // The action row. Optional because a read-only mirror (the session is being
  // driven from a chat app) must still SEE the dry run — it just cannot act on
  // it, and an empty bordered footer would read as broken chrome.
  children?: React.ReactNode;
};

// The dry run is the one turn in the whole conversation where the user MUST
// act, and it used to render as an ordinary ChatMessageBubble — a 75%-width
// grey bubble with a row of `size="sm"` buttons at `pl-1`, visually identical
// to every clarifying question above it, and it scrolls.
//
// This is presentation only. No FSM state, no endpoint and no response field
// changes: DesignerSurface already knows `fsmState` and which turn is last.
// Reaching for a structured response field would add a wire contract to solve a
// styling problem.
//
// Deliberately NOT sticky and it does not trap focus. It is the last thing in
// the transcript and ChatScroll already pins to the bottom; a sticky overlay
// would fight the same scroll container the KB pane's `overscroll-contain` fix
// exists to keep well-behaved.
export function ReviewCard({ title, subtitle, content, createdAt, children }: ReviewCardProps) {
  return (
    <div
      data-testid="review-card"
      // shrink-0 is load-bearing, not spacing. ChatScroll is `flex flex-col`,
      // and a flex item's default flex-shrink is 1 — so a tall card is
      // COMPRESSED below its content height instead of making the container
      // scroll. Combined with the overflow-hidden below (which the rounded
      // corners and the header's border need) that clipped the dry run to its
      // first line, with nothing to scroll: the sample was rendered, sized to
      // nothing, and hidden. Bubbles survive the same squeeze only because
      // they have no overflow-hidden and simply spill.
      //
      // my-1 gives the card breathing room from the bubble above and from the
      // action bar below, which lives outside the scroll container and would
      // otherwise sit flush against it.
      className="group my-1 w-full shrink-0 overflow-hidden rounded-xl border border-accent/40 border-l-4 border-l-accent bg-chrome"
    >
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        <ClipboardCheck className="size-4 shrink-0 text-accent" />
        <div className="min-w-0">
          <div className="text-sm font-semibold text-foreground">{title}</div>
          <div className="text-xs text-muted-2">{subtitle}</div>
        </div>
      </div>

      <div
        data-testid="review-body"
        className={[
          "max-w-none px-4 py-3 text-sm leading-relaxed break-words text-foreground",
          "[&_p]:my-1 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0",
          "[&_pre]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-background [&_pre]:p-3",
          "[&_code]:break-words",
          "[&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-5",
          "[&_strong]:font-semibold [&_a]:underline [&_a]:text-accent",
        ].join(" ")}
      >
        {/* Same renderer config as ChatMessageBubble — no rehype-raw, so raw
            HTML in a dry run renders as inert text rather than markup. */}
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          components={{
            a: ({ node: _node, ...props }) => (
              <a {...props} target="_blank" rel="noreferrer noopener" />
            ),
          }}
        >
          {content}
        </ReactMarkdown>
      </div>

      {children && (
        <div className="flex flex-wrap items-center justify-center gap-2 border-t border-border px-4 py-3">
          {children}
        </div>
      )}

      {/* The copy control the ordinary bubble carries — losing it when a turn
          is promoted to a card would be a silent capability regression, and the
          dry run's sample output is exactly the text worth copying. */}
      <div className="px-4 pb-2">
        <MessageMeta content={content} createdAt={createdAt} />
      </div>
    </div>
  );
}

export default ReviewCard;
