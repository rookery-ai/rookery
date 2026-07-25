import { cn } from "@/lib/utils";
import { lookupLogo, isMonochrome } from "./logos";

// Deterministic fallback palette for slugs with no vendored logo. Every slug
// the app ships today has one (enforced by logocoverage.test.ts), so this is a
// safety net for whatever gets added next — not a state the current UI reaches.
// Picked from Tailwind's default palette so the literal class names below stay
// visible to Tailwind's static scanner.
const FALLBACK_PALETTE = [
  "bg-red-500",
  "bg-orange-500",
  "bg-amber-500",
  "bg-lime-500",
  "bg-emerald-500",
  "bg-teal-500",
  "bg-cyan-500",
  "bg-blue-500",
  "bg-indigo-500",
  "bg-violet-500",
  "bg-fuchsia-500",
  "bg-pink-500",
];

function hashString(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (h * 31 + s.charCodeAt(i)) | 0;
  }
  return Math.abs(h);
}

function fallbackColorClass(name: string): string {
  return FALLBACK_PALETTE[hashString(name) % FALLBACK_PALETTE.length];
}

export function ProviderLogo({ name, size = 32 }: { name: string; size?: number }) {
  const svg = lookupLogo(name);

  if (svg) {
    // Brand logos are drawn for a light background — a white tile is how
    // integration galleries present them, and the only backdrop that keeps
    // every mark in this set legible. It stays white in dark mode too, but
    // bordered rather than floating so it reads as a tile and doesn't glare.
    //
    // Monochrome marks are the exception in the other direction: they paint
    // with currentColor, so the tile pins `color` to near-black for them.
    return (
      <div
        role="img"
        aria-label={name}
        title={name}
        className={cn(
          "inline-flex shrink-0 items-center justify-center overflow-hidden rounded-lg",
          "border border-black/10 bg-white p-1",
          // Most vendored SVGs carry their own width/height attributes; CSS
          // beats those presentation attributes, so every logo scales to the
          // tile instead of rendering at its published size.
          "[&>svg]:size-full [&>svg]:object-contain",
        )}
        style={{
          width: size,
          height: size,
          color: isMonochrome(svg) ? "#18181b" : undefined,
        }}
        // Vendored, committed assets — not user input. The vendoring script
        // strips comments, <script> and <style> from each file before writing.
        dangerouslySetInnerHTML={{ __html: svg }}
      />
    );
  }

  const initial = name.trim().charAt(0).toUpperCase() || "?";
  return (
    <div
      role="img"
      aria-label={name}
      title={name}
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-lg font-semibold text-white",
        fallbackColorClass(name),
      )}
      style={{ width: size, height: size, fontSize: Math.round(size * 0.45) }}
    >
      {initial}
    </div>
  );
}
