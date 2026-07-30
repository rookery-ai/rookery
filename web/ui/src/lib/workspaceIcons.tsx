// The preset workspace images.
//
// Artwork lives here as inline SVG rather than as files: a workspace picture
// then needs no upload endpoint, no vault storage, no size/MIME validation,
// and no extra request to render — and it inherits the page's own scaling and
// theme instead of being a fixed-resolution bitmap. The DB stores only the
// slug (see web/api_settings.go's workspaceIcons, which must stay in sync and
// rejects anything not in this set).
//
// Each preset is a two-stop gradient plus one simple geometric motif, so the
// twelve stay distinguishable at 20px — the size they are actually seen at in
// the rail — where a detailed illustration would collapse into mush.

export type WorkspaceIcon = {
  slug: string;
  label: string;
  /** [from, to] gradient stops. */
  colors: [string, string];
  /** Motif drawn in white at 85% opacity over the gradient. */
  motif: React.ReactNode;
};

// Motifs are authored on a 24x24 viewBox.
const dot = <circle cx="12" cy="12" r="4.5" />;
const ring = <circle cx="12" cy="12" r="5.5" fill="none" stroke="currentColor" strokeWidth="2.5" />;
const bars = (
  <>
    <rect x="5" y="13" width="3.4" height="6" rx="1.2" />
    <rect x="10.3" y="9" width="3.4" height="10" rx="1.2" />
    <rect x="15.6" y="5.5" width="3.4" height="13.5" rx="1.2" />
  </>
);
const wave = (
  <path
    d="M4 15c2.7 0 2.7-4 5.3-4s2.7 4 5.4 4 2.6-4 5.3-4"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.4"
    strokeLinecap="round"
  />
);
const triangle = <path d="M12 5.5 19 18H5z" />;
const diamond = <path d="M12 4.5 19.5 12 12 19.5 4.5 12z" />;
const leaf = <path d="M18.5 5.5c0 7-4.5 11-9.5 11-1.4 0-2.6-.3-3.5-.8 1-6.4 5.6-10.2 13-10.2z" />;
const arc = (
  <path d="M5 17a7 7 0 0 1 14 0" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" />
);
const cross = (
  <>
    <rect x="10.6" y="4.5" width="2.8" height="15" rx="1.4" />
    <rect x="4.5" y="10.6" width="15" height="2.8" rx="1.4" />
  </>
);
const hex = <path d="M12 4.2 18.8 8v8L12 19.8 5.2 16V8z" />;
const petals = (
  <>
    <circle cx="12" cy="8" r="3.2" />
    <circle cx="8.5" cy="14" r="3.2" />
    <circle cx="15.5" cy="14" r="3.2" />
  </>
);
const slab = <rect x="5" y="7.5" width="14" height="9" rx="2.5" />;

const chevrons = (
  <path
    d="M7 7l5 5-5 5M14 7l4 5-4 5"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.4"
    strokeLinecap="round"
    strokeLinejoin="round"
  />
);
const grid = (
  <>
    <rect x="5" y="5" width="5.6" height="5.6" rx="1.6" />
    <rect x="13.4" y="5" width="5.6" height="5.6" rx="1.6" />
    <rect x="5" y="13.4" width="5.6" height="5.6" rx="1.6" />
    <rect x="13.4" y="13.4" width="5.6" height="5.6" rx="1.6" />
  </>
);
const pillars = (
  <>
    <rect x="5.5" y="5.5" width="3.2" height="13" rx="1.3" />
    <rect x="10.4" y="5.5" width="3.2" height="13" rx="1.3" />
    <rect x="15.3" y="5.5" width="3.2" height="13" rx="1.3" />
  </>
);
const droplet = <path d="M12 4.5c3.4 4 5.5 6.4 5.5 9a5.5 5.5 0 0 1-11 0c0-2.6 2.1-5 5.5-9z" />;
const star = (
  <path d="M12 4.6l2.2 4.7 5.1.6-3.8 3.5 1 5.1-4.5-2.5-4.5 2.5 1-5.1L4.7 9.9l5.1-.6z" />
);
const crescent = <path d="M15.2 4.8a7.4 7.4 0 1 0 3.9 12.1A8.2 8.2 0 0 1 15.2 4.8z" />;
const bolt = <path d="M13.6 3.8L6.8 13.4h4.1l-1.3 6.8 7-9.9h-4.2z" />;
const rings = (
  <>
    <circle cx="9.6" cy="12" r="4.4" fill="none" stroke="currentColor" strokeWidth="2.2" />
    <circle cx="15" cy="12" r="4.4" fill="none" stroke="currentColor" strokeWidth="2.2" />
  </>
);
const chevronUp = (
  <path
    d="M5 15.5l7-7 7 7"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.6"
    strokeLinecap="round"
    strokeLinejoin="round"
  />
);
const square = <rect x="6.5" y="6.5" width="11" height="11" rx="3" />;
const halfMoon = <path d="M12 4.5a7.5 7.5 0 0 1 0 15z" />;
const plusRing = (
  <>
    <circle cx="12" cy="12" r="7" fill="none" stroke="currentColor" strokeWidth="2.2" />
    <rect x="11" y="8.4" width="2" height="7.2" rx="1" />
    <rect x="8.4" y="11" width="7.2" height="2" rx="1" />
  </>
);
const stack = (
  <>
    <rect x="5" y="6" width="14" height="3.4" rx="1.4" />
    <rect x="5" y="10.6" width="14" height="3.4" rx="1.4" />
    <rect x="5" y="15.2" width="14" height="3.4" rx="1.4" />
  </>
);
const eye = (
  <>
    <path d="M3.5 12S6.8 6.8 12 6.8 20.5 12 20.5 12 17.2 17.2 12 17.2 3.5 12 3.5 12z" fill="none" stroke="currentColor" strokeWidth="2.1" />
    <circle cx="12" cy="12" r="2.1" />
  </>
);
const seed = <path d="M12 4.5c4 3.2 5.6 6.6 4.4 10.1A5.2 5.2 0 0 1 7.6 14.6C6.4 11.1 8 7.7 12 4.5z" />;
const compass = (
  <>
    <circle cx="12" cy="12" r="7.2" fill="none" stroke="currentColor" strokeWidth="2.1" />
    <path d="M14.6 9.4l-1.7 4.2-4.2 1.7 1.7-4.2z" />
  </>
);

export const WORKSPACE_ICONS: WorkspaceIcon[] = [
  { slug: "aurora", label: "Aurora", colors: ["#6366f1", "#22d3ee"], motif: arc },
  { slug: "orbit", label: "Orbit", colors: ["#0f172a", "#475569"], motif: ring },
  { slug: "prism", label: "Prism", colors: ["#7c3aed", "#ec4899"], motif: triangle },
  { slug: "meadow", label: "Meadow", colors: ["#15803d", "#84cc16"], motif: leaf },
  { slug: "ember", label: "Ember", colors: ["#b91c1c", "#f59e0b"], motif: dot },
  { slug: "tide", label: "Tide", colors: ["#0e7490", "#38bdf8"], motif: wave },
  { slug: "dusk", label: "Dusk", colors: ["#312e81", "#a855f7"], motif: diamond },
  { slug: "grove", label: "Grove", colors: ["#065f46", "#10b981"], motif: hex },
  { slug: "signal", label: "Signal", colors: ["#1d4ed8", "#60a5fa"], motif: bars },
  { slug: "quartz", label: "Quartz", colors: ["#be185d", "#fb7185"], motif: cross },
  { slug: "bloom", label: "Bloom", colors: ["#c2410c", "#fbbf24"], motif: petals },
  { slug: "slate", label: "Slate", colors: ["#334155", "#94a3b8"], motif: slab },
  { slug: "cascade", label: "Cascade", colors: ["#0369a1", "#67e8f9"], motif: chevrons },
  { slug: "lattice", label: "Lattice", colors: ["#4338ca", "#818cf8"], motif: grid },
  { slug: "forum", label: "Forum", colors: ["#78350f", "#d97706"], motif: pillars },
  { slug: "spring", label: "Spring", colors: ["#0891b2", "#a5f3fc"], motif: droplet },
  { slug: "nova", label: "Nova", colors: ["#a21caf", "#f0abfc"], motif: star },
  { slug: "eclipse", label: "Eclipse", colors: ["#1e1b4b", "#6366f1"], motif: crescent },
  { slug: "surge", label: "Surge", colors: ["#ca8a04", "#fde047"], motif: bolt },
  { slug: "venn", label: "Venn", colors: ["#047857", "#5eead4"], motif: rings },
  { slug: "summit", label: "Summit", colors: ["#374151", "#9ca3af"], motif: chevronUp },
  { slug: "monolith", label: "Monolith", colors: ["#7c2d12", "#fb923c"], motif: square },
  { slug: "waning", label: "Waning", colors: ["#581c87", "#c084fc"], motif: halfMoon },
  { slug: "clinic", label: "Clinic", colors: ["#b91c1c", "#fca5a5"], motif: plusRing },
  { slug: "strata", label: "Strata", colors: ["#155e75", "#7dd3fc"], motif: stack },
  { slug: "beacon", label: "Beacon", colors: ["#1e40af", "#93c5fd"], motif: eye },
  { slug: "sprout", label: "Sprout", colors: ["#3f6212", "#a3e635"], motif: seed },
  { slug: "voyage", label: "Voyage", colors: ["#0f766e", "#2dd4bf"], motif: compass },
];

export function findWorkspaceIcon(slug: string | undefined): WorkspaceIcon | undefined {
  if (!slug) return undefined;
  return WORKSPACE_ICONS.find((i) => i.slug === slug);
}

// WorkspaceAvatar renders the chosen preset, or the workspace name's initial
// when none is set (or the stored slug is one this build doesn't know — a
// workspace configured by a newer version must still render SOMETHING, not a
// blank square).
//
// The gradient id is derived from the slug and marked with a stable prefix:
// two avatars for the same workspace (the rail trigger and the picker's
// selected tile) can be on screen at once, and duplicate SVG ids would make
// the second one reference the first one's gradient — harmless here only
// because they are identical, but it stops being harmless the moment two
// DIFFERENT workspaces are shown side by side in the switcher.
export function WorkspaceAvatar({
  name,
  icon,
  className,
}: {
  name?: string;
  icon?: string;
  className?: string;
}) {
  const preset = findWorkspaceIcon(icon);
  if (!preset) {
    return (
      <span
        aria-hidden="true"
        className={`flex items-center justify-center rounded-lg bg-foreground font-bold text-background ${className ?? ""}`}
      >
        {name?.[0]?.toUpperCase() ?? "?"}
      </span>
    );
  }
  const gradId = `wsicon-${preset.slug}`;
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      className={`rounded-lg ${className ?? ""}`}
      role="presentation"
    >
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor={preset.colors[0]} />
          <stop offset="100%" stopColor={preset.colors[1]} />
        </linearGradient>
      </defs>
      <rect width="24" height="24" rx="6" fill={`url(#${gradId})`} />
      <g fill="#ffffff" color="#ffffff" opacity="0.9">
        {preset.motif}
      </g>
    </svg>
  );
}
