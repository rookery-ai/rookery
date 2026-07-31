import { cn } from "@/lib/utils";

// The shared bordered container.
//
// Replaces the hand-rolled "rounded-lg border border-border p-3" blocks that
// had been copied across the homepage, skills, secrets and connections pages,
// so outline weight, radius and padding cannot drift per page.
//
// Deliberately 1px, not border-2: a 2px hairline across two dozen cards reads
// as heavy and noisy rather than crisp. The "bolder" in the request is
// delivered by the darkened --border token (see index.css), the "bigger" by
// rounded-xl + p-4. --border-strong exists as a defined next step if cards
// still read thin in the running app — that is a one-line change here rather
// than a new invented value at 23 call sites.
export const cardClass = "rounded-xl border border-border bg-background p-4";

export function Card({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="card" className={cn(cardClass, className)} {...props} />;
}

// The small uppercase heading used inside a Card. Its own component because
// every card had re-declared the same four utilities, and one of them had
// already drifted to a different size.
export function CardTitle({ className, ...props }: React.ComponentProps<"h3">) {
  return (
    <h3
      className={cn("mb-2 text-xs font-bold uppercase tracking-wide text-muted-2", className)}
      {...props}
    />
  );
}
