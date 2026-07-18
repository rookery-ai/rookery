import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

type StepperProps = {
  steps: [string, string, string, string];
  activeIndex: number; // 0-3; steps before it are "done", the rest pending
};

// Describe → Design → Build → Review (labels come from DesignerLabels so the
// skill creator — Task 8 — can relabel without a new component).
export function Stepper({ steps, activeIndex }: StepperProps) {
  return (
    <ol data-testid="stepper" className="flex items-center gap-2 text-xs font-medium text-muted-2">
      {steps.map((label, i) => {
        const isActive = i === activeIndex;
        const isDone = i < activeIndex;
        return (
          <li key={label} className="flex items-center gap-2">
            <span
              className={cn(
                "flex size-5 shrink-0 items-center justify-center rounded-full border text-[10px]",
                isActive && "border-foreground bg-foreground text-background",
                isDone && "border-ok bg-ok text-white",
                !isActive && !isDone && "border-border text-muted-2",
              )}
            >
              {isDone ? <Check className="size-3" /> : i + 1}
            </span>
            <span className={cn(isActive && "text-foreground")}>{label}</span>
            {i < steps.length - 1 && <span aria-hidden className="mx-1 h-px w-4 bg-border" />}
          </li>
        );
      })}
    </ol>
  );
}
