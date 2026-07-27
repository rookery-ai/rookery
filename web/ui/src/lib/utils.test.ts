import { formatMessageTime } from "./utils";

// 2026-07-26T12:00:00Z is a Sunday at noon UTC.
const ISO = "2026-07-26T12:00:00Z";

test("formats as short weekday + 24-hour time in the given IANA zone", () => {
  // Tokyo is UTC+9 → 21:00 the same Sunday.
  const out = formatMessageTime(ISO, "Asia/Tokyo");
  expect(out).toContain("Sun");
  expect(out).toContain("21:00");
});

test("a different zone yields a different clock time", () => {
  expect(formatMessageTime(ISO, "UTC")).toContain("12:00");
});

// profile.Timezone is a free-text settings field, so "CEST"/"UTC+2"/"" are all
// reachable. Intl throws RangeError on those; a throw during render would blank
// every bubble in the chat, so the formatter must absorb it.
test("an invalid zone falls back to browser-local instead of throwing", () => {
  const local = formatMessageTime(ISO);
  expect(() => formatMessageTime(ISO, "CEST")).not.toThrow();
  expect(formatMessageTime(ISO, "CEST")).toBe(local);
  expect(formatMessageTime(ISO, "")).toBe(local);
});

test("an unparseable date renders nothing rather than 'Invalid Date'", () => {
  expect(formatMessageTime("not a date")).toBe("");
  expect(formatMessageTime("")).toBe("");
});
