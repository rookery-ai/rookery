import { cn } from "@/lib/utils";
import { PROVIDER_LOGOS } from "./logos";

// Deterministic fallback palette for slugs with no brand icon in
// PROVIDER_LOGOS (either genuinely missing from simple-icons, or a platform
// we haven't added yet). Picked from Tailwind's default palette so the
// literal class names below are visible to Tailwind's static scanner.
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

// WCAG-ish relative luminance of a "RRGGBB" hex string, used to decide
// whether the icon glyph should render in white or near-black for contrast
// against the brand-colored tile.
function hexLuminance(hex: string): number {
  const n = parseInt(hex, 16);
  const r = ((n >> 16) & 255) / 255;
  const g = ((n >> 8) & 255) / 255;
  const b = (n & 255) / 255;
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4));
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

export function ProviderLogo({ name, size = 32 }: { name: string; size?: number }) {
  const logo = PROVIDER_LOGOS[name.toLowerCase()];

  if (logo) {
    // Very light brand colors (e.g. Mailchimp's #FFE01B) read poorly as a
    // solid tile with a white glyph — flip to a white tile with the brand
    // color carried by the glyph instead.
    const isLight = hexLuminance(logo.hex) > 0.7;
    const tileBg = isLight ? "#ffffff" : `#${logo.hex}`;
    const glyphColor = isLight ? `#${logo.hex}` : "#ffffff";
    return (
      <div
        role="img"
        aria-label={logo.title}
        title={logo.title}
        className={cn("inline-flex shrink-0 items-center justify-center rounded-lg", isLight && "border border-black/10")}
        style={{ width: size, height: size, backgroundColor: tileBg }}
      >
        <svg
          viewBox="0 0 24 24"
          width={Math.round(size * 0.6)}
          height={Math.round(size * 0.6)}
          fill={glyphColor}
          aria-hidden="true"
        >
          <path d={logo.path} />
        </svg>
      </div>
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
