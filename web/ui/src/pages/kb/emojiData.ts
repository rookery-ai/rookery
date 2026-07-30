import { generatedEmojiGroups } from "./emojiData.generated";

// The emoji set behind the KB icon picker.
//
// This file owns the TYPES and the search, and re-exports the full Unicode set
// from emojiData.generated.ts (built by scripts/gen-emoji.mjs from vendored
// Unicode data). It used to hold a hand-curated ~160-emoji table, whose own
// header named emoji-mart as the escape hatch if the full set was ever wanted —
// but a ~200 KB runtime dependency in a binary-embedded SPA, with its own
// styling to theme, is a poor trade for data. Generating at build time gets all
// 1906 emoji with zero runtime dependencies, which is why the escape hatch was
// not taken.

export type EmojiEntry = { emoji: string; keywords: string };

export type EmojiGroup = { name: string; emojis: EmojiEntry[] };

// Grouped by the 9 standard Unicode categories (Smileys & Emotion, People &
// Body, Animals & Nature, Food & Drink, Travel & Places, Activities, Objects,
// Symbols, Flags) — the order users already know from every other picker.
export const emojiGroups: EmojiGroup[] = generatedEmojiGroups;

// filterEmojis returns entries whose keywords or glyph match the query. Empty
// query → the whole grouped set flattened is handled by the caller (it renders
// groups); this is only used when a query is present.
export function filterEmojis(query: string): EmojiEntry[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const seen = new Set<string>();
  const out: EmojiEntry[] = [];
  for (const g of emojiGroups) {
    for (const e of g.emojis) {
      if (seen.has(e.emoji)) continue;
      if (e.keywords.includes(q) || e.emoji === q) {
        seen.add(e.emoji);
        out.push(e);
      }
    }
  }
  return out;
}
