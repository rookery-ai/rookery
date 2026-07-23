// A curated emoji set for the KB icon picker — deliberately in-house (no
// emoji-mart / full-Unicode dependency) so the picker stays a small, dependency-
// free popover. Each entry carries search keywords so the picker's filter box
// can match on intent ("book", "money", "warn") not just the glyph.
//
// If the full Unicode set is ever wanted, swapping this file for emoji-mart is
// the documented escape hatch (see the KB file-tree UX spec).

export type EmojiEntry = { emoji: string; keywords: string };

export type EmojiGroup = { name: string; emojis: EmojiEntry[] };

export const emojiGroups: EmojiGroup[] = [
  {
    name: "Documents",
    emojis: [
      { emoji: "📄", keywords: "page document file note" },
      { emoji: "📝", keywords: "memo note write edit" },
      { emoji: "📋", keywords: "clipboard list tasks" },
      { emoji: "📑", keywords: "bookmark tabs sections" },
      { emoji: "📔", keywords: "notebook journal diary" },
      { emoji: "📕", keywords: "book red closed" },
      { emoji: "📗", keywords: "book green" },
      { emoji: "📘", keywords: "book blue" },
      { emoji: "📙", keywords: "book orange" },
      { emoji: "📚", keywords: "books library stack knowledge" },
      { emoji: "📖", keywords: "open book read reference" },
      { emoji: "🗒️", keywords: "notepad spiral list" },
      { emoji: "🗓️", keywords: "calendar schedule date" },
      { emoji: "📅", keywords: "calendar date day" },
      { emoji: "🧾", keywords: "receipt invoice bill" },
      { emoji: "📃", keywords: "page curl document" },
    ],
  },
  {
    name: "Folders & files",
    emojis: [
      { emoji: "📁", keywords: "folder directory closed" },
      { emoji: "📂", keywords: "folder open directory" },
      { emoji: "🗂️", keywords: "dividers folders organize index" },
      { emoji: "🗃️", keywords: "card box file archive" },
      { emoji: "🗄️", keywords: "cabinet drawers archive" },
      { emoji: "📦", keywords: "box package archive storage" },
      { emoji: "🏷️", keywords: "label tag" },
      { emoji: "📌", keywords: "pin pinned important" },
      { emoji: "📍", keywords: "location pin place" },
      { emoji: "🔖", keywords: "bookmark save" },
      { emoji: "🗑️", keywords: "trash delete bin" },
    ],
  },
  {
    name: "Work & projects",
    emojis: [
      { emoji: "💼", keywords: "briefcase work business job" },
      { emoji: "📊", keywords: "chart bars analytics stats" },
      { emoji: "📈", keywords: "chart up growth trend" },
      { emoji: "📉", keywords: "chart down decline" },
      { emoji: "🎯", keywords: "target goal objective aim" },
      { emoji: "🚀", keywords: "rocket launch ship fast" },
      { emoji: "🛠️", keywords: "tools build fix maintenance" },
      { emoji: "⚙️", keywords: "gear settings config" },
      { emoji: "🧩", keywords: "puzzle piece module component" },
      { emoji: "🗺️", keywords: "map roadmap plan" },
      { emoji: "📎", keywords: "paperclip attach" },
      { emoji: "🔧", keywords: "wrench tool fix" },
      { emoji: "🧰", keywords: "toolbox tools kit" },
      { emoji: "💡", keywords: "idea lightbulb tip insight" },
      { emoji: "🔑", keywords: "key access secret credential" },
      { emoji: "🔒", keywords: "lock secure private" },
    ],
  },
  {
    name: "Tech & automation",
    emojis: [
      { emoji: "🤖", keywords: "robot bot agent automation ai" },
      { emoji: "💻", keywords: "laptop computer code dev" },
      { emoji: "🖥️", keywords: "desktop monitor computer" },
      { emoji: "⌨️", keywords: "keyboard type input" },
      { emoji: "🧠", keywords: "brain memory think smart" },
      { emoji: "🔌", keywords: "plug connect integration" },
      { emoji: "🛰️", keywords: "satellite signal network" },
      { emoji: "📡", keywords: "antenna signal broadcast" },
      { emoji: "🗂️", keywords: "database index records" },
      { emoji: "💾", keywords: "save disk floppy storage" },
      { emoji: "🔗", keywords: "link chain url reference" },
      { emoji: "⚡", keywords: "fast lightning power quick" },
      { emoji: "🐛", keywords: "bug issue defect debug" },
      { emoji: "✅", keywords: "check done complete pass" },
      { emoji: "❌", keywords: "cross fail no error" },
      { emoji: "⭐", keywords: "star favorite important" },
    ],
  },
  {
    name: "Communication",
    emojis: [
      { emoji: "💬", keywords: "chat message speech talk" },
      { emoji: "📣", keywords: "megaphone announce broadcast" },
      { emoji: "📢", keywords: "loudspeaker announce" },
      { emoji: "✉️", keywords: "email envelope mail message" },
      { emoji: "📧", keywords: "email mail inbox" },
      { emoji: "📨", keywords: "incoming mail message" },
      { emoji: "📬", keywords: "mailbox inbox notification" },
      { emoji: "🔔", keywords: "bell notification alert remind" },
      { emoji: "📞", keywords: "phone call contact" },
      { emoji: "👥", keywords: "people team users group" },
      { emoji: "🤝", keywords: "handshake deal agreement partner" },
      { emoji: "🗣️", keywords: "speaking voice talk" },
    ],
  },
  {
    name: "Signals & status",
    emojis: [
      { emoji: "🔥", keywords: "fire hot urgent trending" },
      { emoji: "⚠️", keywords: "warning caution alert" },
      { emoji: "🚨", keywords: "siren alert emergency urgent" },
      { emoji: "❗", keywords: "important exclamation" },
      { emoji: "❓", keywords: "question help unknown" },
      { emoji: "💰", keywords: "money finance budget cost" },
      { emoji: "💳", keywords: "card payment billing" },
      { emoji: "🏆", keywords: "trophy win achievement award" },
      { emoji: "🎉", keywords: "party celebrate done launch" },
      { emoji: "❤️", keywords: "heart love favorite" },
      { emoji: "🌟", keywords: "star sparkle highlight" },
      { emoji: "🏁", keywords: "finish flag done complete" },
    ],
  },
  {
    name: "Nature & misc",
    emojis: [
      { emoji: "🌱", keywords: "seedling grow new sprout" },
      { emoji: "🌳", keywords: "tree nature growth" },
      { emoji: "☀️", keywords: "sun day weather" },
      { emoji: "🌙", keywords: "moon night" },
      { emoji: "🌍", keywords: "earth world global" },
      { emoji: "☕", keywords: "coffee break morning" },
      { emoji: "🍽️", keywords: "food meal dinner recipe" },
      { emoji: "🏠", keywords: "home house main" },
      { emoji: "🧭", keywords: "compass navigate direction guide" },
      { emoji: "🎨", keywords: "art design creative palette" },
      { emoji: "🎵", keywords: "music note audio" },
      { emoji: "📷", keywords: "camera photo image picture" },
    ],
  },
];

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
