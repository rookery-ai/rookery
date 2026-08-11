// The Rookery mark, as inline SVG.
//
// One definition, three consumers: the sign-in screen, the workspace-selection
// screen, and the workspace image presets in lib/workspaceIcons.tsx. It is a
// component rather than a file in assets/ because an <img> cannot inherit
// `currentColor` — which is exactly how the mark on the documentation site ended
// up painting black and disappearing against the dark theme. Drawing it inline
// means it takes the colour of whatever it sits in, in both themes, for free.
//
// The geometry is the favicon's, authored on a 32x32 grid: two rules and a bowl.
// Keep the two in step — public/favicon.svg is the only copy that cannot be
// generated from this one, because a browser tab needs a real file.

/** The glyph alone, stroked in `currentColor`. */
export function RookeryMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      className={className}
      role="img"
      aria-label="Rookery"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.8"
      strokeLinecap="round"
    >
      <path d="M12 10h8M9.2 14.4h13.6M6.8 18.8C6.8 24 10.8 26.4 16 26.4S25.2 24 25.2 18.8" />
    </svg>
  );
}

/**
 * The mark on its own rounded tile, the way it appears as a favicon or an app
 * icon. `tone` picks the tile fill; the glyph is always the cream that the brand
 * pairs with ember, since a tile is a fixed background rather than an inherited
 * one.
 */
export function RookeryTile({
  className,
  from = "#a94c1c",
  to = "#d98a4f",
  id = "rookery-tile",
}: {
  className?: string;
  from?: string;
  to?: string;
  /** Gradient id. Must be unique when two tiles are on screen at once. */
  id?: string;
}) {
  return (
    <svg viewBox="0 0 32 32" className={className} role="img" aria-label="Rookery">
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor={from} />
          <stop offset="100%" stopColor={to} />
        </linearGradient>
      </defs>
      <rect width="32" height="32" rx="7" fill={`url(#${id})`} />
      <path
        d="M12 10h8M9.2 14.4h13.6M6.8 18.8C6.8 24 10.8 26.4 16 26.4S25.2 24 25.2 18.8"
        fill="none"
        stroke="#ece5db"
        strokeWidth="2.8"
        strokeLinecap="round"
      />
    </svg>
  );
}

/**
 * Mark plus wordmark, locked up as one unit.
 *
 * The gap is deliberately tight (`gap-2` at this size). Mark and word read as
 * one logo only while they are closer to each other than either is to anything
 * else on the screen; spaced further apart they read as two separate objects
 * that happen to be adjacent — which is what the documentation site's header was
 * doing.
 */
export function RookeryLogo({
  className,
  markClassName = "size-8",
}: {
  className?: string;
  markClassName?: string;
}) {
  return (
    <span className={`inline-flex items-center gap-2 ${className ?? ""}`}>
      <RookeryMark className={`${markClassName} text-accent`} />
      <span className="text-2xl font-semibold tracking-tight lowercase">rookery</span>
    </span>
  );
}
