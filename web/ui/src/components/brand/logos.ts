// Brand-logo registry for ProviderLogo.
//
// Every logo is a real vendored SVG under src/assets/logos/, committed to the
// repo — nothing is fetched from a CDN at runtime. Re-run
// `scripts/vendor-brand-logos.sh` to add a brand or refresh the set; that
// script records where each file came from.
//
// Slugs cover three namespaces, which is why one flat map suffices:
//   - chat platforms      (internal/gateway CredSpecs): telegram, discord, slack
//   - service connectors  (internal/connectors/providers/*.yaml, 28 files)
//   - coder API providers (internal/coder.APIProviders() Name field)
//
// The file name IS the slug, so adding a logo needs no edit here — drop the SVG
// in and it resolves. `logocoverage.test.ts` asserts every slug the app can
// actually render has a file, so a provider added on the Go side without a logo
// fails the test run rather than silently degrading to a letter tile.

// Eager + ?raw because these are INLINED into the DOM rather than loaded as
// <img>: several marks (OpenAI, Anthropic, GitHub, Notion, Ollama…) are
// published as monochrome fill="currentColor" paths, and currentColor cannot
// cross an <img> boundary — inside an <img> it resolves against that image's
// own document and always comes out black. Inlining is what lets those marks
// take a theme-aware colour from the tile.
const modules = import.meta.glob("../../assets/logos/*.svg", {
  eager: true,
  query: "?raw",
  import: "default",
}) as Record<string, string>;

function slugOf(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1).replace(/\.svg$/, "");
}

export const PROVIDER_LOGOS: Record<string, string> = Object.fromEntries(
  Object.entries(modules).map(([path, svg]) => [slugOf(path), svg]),
);

// A monochrome mark paints itself with currentColor and needs a colour from the
// tile; a full-colour logo carries its own fills and must be left alone.
// Telling them apart is what lets one component render both without washing the
// colour ones out.
export function isMonochrome(svg: string): boolean {
  return svg.includes("currentColor");
}

export function lookupLogo(name: string): string | undefined {
  return PROVIDER_LOGOS[name.trim().toLowerCase()];
}
