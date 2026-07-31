import { cn } from "@/lib/utils";

// The one page-content wrapper.
//
// Replaces four independent hardcoded widths — Settings (max-w-3xl, 768px),
// Connections (max-w-5xl), FolderPage and NoteEditor — all of which centred
// their content and left roughly 900px of empty margin on a 1920px display.
//
// mx-auto only takes effect once the 1600px cap is reached, so a 1440px
// viewport is genuinely fluid while a 2560px one does not grow 200-character
// line lengths. Forms inside still cap their own field column (~640px): the
// fluid container is for LAYOUT, and a 1500px-wide text input is worse than a
// cramped one, not better.
//
// Note px-8 py-6 rather than a p-* shorthand. A caller overriding the
// horizontal padding (the KB editor passes px-[7%]) must actually win, and
// tailwind-merge keeps BOTH classes when p-* meets px-* because they are
// different groups — leaving the winner to generated-stylesheet ordering.
export function PageContainer({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="page-container"
      className={cn("mx-auto w-full max-w-[1600px] px-8 py-6", className)}
      {...props}
    />
  );
}
