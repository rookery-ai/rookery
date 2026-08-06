// Pure geometry + alt-text parsing, extracted so it is actually testable:
// jsdom has no layout engine and cannot drive a pointer drag, so a test
// against the NodeView could prove an image renders but never that a resize
// lands on the right width. Same reasoning as placeMenu in SlashMenu.tsx.

export const MIN_IMAGE_WIDTH = 80;

export function clampImageWidth(desired: number, columnWidth: number): number {
  const max = Math.max(MIN_IMAGE_WIDTH, Math.round(columnWidth));
  return Math.min(Math.max(Math.round(desired), MIN_IMAGE_WIDTH), max);
}

// Obsidian's convention puts the width in the alt slot: ![alt|420](src).
// Split on the LAST pipe and only when the tail is a bare integer, so an alt
// that genuinely contains a pipe survives.
export function splitAltWidth(alt: string): { alt: string; width: number | null } {
  const i = alt.lastIndexOf("|");
  if (i === -1) return { alt, width: null };
  const tail = alt.slice(i + 1);
  if (!/^\d+$/.test(tail)) return { alt, width: null };
  return { alt: alt.slice(0, i), width: parseInt(tail, 10) };
}

export function joinAltWidth(alt: string, width: number | null): string {
  return width === null ? alt : `${alt}|${width}`;
}
