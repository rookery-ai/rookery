import { PageTitle } from "@/components/shell/PageTitle";

export default function Placeholder({ title, icon }: { title: string; icon?: string }) {
  return (
    <div className="p-8">
      {/* No icon key given? PageTitle's own fallback handles it — a placeholder
          page is not worth a required prop at every call site. */}
      <PageTitle icon={icon ?? title.toLowerCase()} title={title} />
      <p className="text-muted-2 mt-2">Coming in a later sub-plan.</p>
    </div>
  );
}
