import type { ReactNode } from "react";

// ── Linkify ──────────────────────────────────────────────────────────────
// Turns bare URLs (and bare domains with a path, e.g. "api.slack.com/apps"
// or "console.cloud.google.com/apis/credentials") appearing in plain-text
// setup-step copy into clickable links — no dependency, a global regex scan
// with match indices rebuilds the text with <a> tags spliced in.
//
// Scheme-prefixed URLs (`https?://...`) are matched first; a bare-domain
// match requires at least one dot-separated label followed by a 2+ letter
// TLD AND a trailing "/path" — a bare "google.com" with no path is left as
// plain text (too easy to false-positive on ordinary prose).
//
// A trailing run of ")", ".", ",", ";" is trimmed off the match (so
// "(see https://x.com/y)." links only "https://x.com/y", not the closing
// parenthesis/sentence punctuation) and re-emitted as plain text after the
// link.
//
// Only http(s) URLs ever become an href: a scheme-prefixed match always
// starts with http(s):// by construction, and a bare-domain match always
// gets an https:// prefix synthesized for its href — there is no code path
// that lets an arbitrary scheme (e.g. "javascript:") become a clickable
// href, and the domain pattern itself can never match a "javascript:..."
// string in the first place (no dot-label + TLD + "/" shape).

const LINK_PATTERN = /(https?:\/\/[^\s]+|(?:[a-z0-9-]+\.)+[a-z]{2,}\/[^\s]+)/gi;
const TRAILING_PUNCT = /[).,;]+$/;

export function Linkify({ text }: { text: string }): ReactNode {
  const nodes: ReactNode[] = [];
  let lastIndex = 0;
  let key = 0;

  for (const match of text.matchAll(LINK_PATTERN)) {
    const raw = match[0];
    const start = match.index ?? 0;
    const trailing = raw.match(TRAILING_PUNCT)?.[0] ?? "";
    const trimmed = trailing ? raw.slice(0, raw.length - trailing.length) : raw;
    if (!trimmed) continue; // punctuation-only match (shouldn't happen, but stay safe)

    if (start > lastIndex) nodes.push(<span key={key++}>{text.slice(lastIndex, start)}</span>);

    const hasScheme = /^https?:\/\//i.test(trimmed);
    const href = hasScheme ? trimmed : `https://${trimmed}`;
    nodes.push(
      <a
        key={key++}
        href={href}
        target="_blank"
        rel="noreferrer"
        className="text-primary underline underline-offset-2"
      >
        {trimmed}
      </a>,
    );
    lastIndex = start + trimmed.length;
  }

  if (lastIndex < text.length) nodes.push(<span key={key++}>{text.slice(lastIndex)}</span>);

  return <>{nodes}</>;
}

export default Linkify;
