export default function Placeholder({ title }: { title: string }) {
  return (
    <div className="p-8">
      <h1 className="text-xl font-bold">{title}</h1>
      <p className="text-muted-2 mt-2">Coming in a later sub-plan.</p>
    </div>
  );
}
