import { describeCron, describeCronSentence } from "./cron";

// The shapes SpecPanel's narrower parser already covered. They must keep
// reading the same, or moving the translator here changes the designer's spec
// view as a side effect.
test.each([
  ["*/10 * * * *", "every 10 minutes"],
  ["*/1 * * * *", "every minute"],
  ["0 * * * *", "every hour"],
  ["0 9 * * *", "every day at 09:00"],
  ["0 9 * * 1", "every Monday at 09:00"],
])("%s reads as %s", (expr, want) => {
  expect(describeCron(expr)).toBe(want);
});

// The shapes this widening adds.
test.each([
  // A weekday list — the case the request asked for by name.
  ["0 8 * * 1,6", "every Monday and Saturday at 08:00"],
  ["0 8 * * 1,3,5", "every Monday, Wednesday and Friday at 08:00"],
  // Ranges, and the two runs worth naming rather than enumerating.
  ["30 7 * * 1-5", "every weekday at 07:30"],
  ["0 10 * * 0,6", "every weekend day at 10:00"],
  ["0 9 * * mon-wed", "every Monday, Tuesday and Wednesday at 09:00"],
  // Hour lists and hour steps.
  ["0 8,17 * * *", "every day at 08:00 and 17:00"],
  ["0 */6 * * *", "every 6 hours"],
  ["15 */6 * * *", "every 6 hours at :15"],
  // A minute past the hour, which used to fall through to the raw expression.
  ["15 * * * *", "every hour at :15"],
  // Day of month.
  ["0 9 1 * *", "on day 1 of every month at 09:00"],
])("%s reads as %s", (expr, want) => {
  expect(describeCron(expr)).toBe(want);
});

// Cron numbers Sunday as both 0 and 7. Naming it twice would be the sort of
// small wrongness that costs the reader's trust in the whole sentence.
test("Sunday's two spellings collapse to one day", () => {
  expect(describeCron("0 9 * * 0,7")).toBe("every Sunday at 09:00");
});

// The same schedule must produce the same sentence however it was written.
// Source order would render "1,6" and "6,1" as two different schedules.
test("weekday order follows the cron numbering, not the order typed", () => {
  expect(describeCron("0 8 * * 6,1")).toBe(describeCron("0 8 * * 1,6"));
});

// ── Refusals ────────────────────────────────────────────────────────────────
//
// Each of these is a case where a confident sentence would be WRONG, which is
// worse than the raw expression the caller falls back to.

test.each([
  // Out of range: not a schedule at 01:00, an expression we should not guess.
  ["0 25 * * *", "hour above 23"],
  ["70 * * * *", "minute above 59"],
  ["0 9 * * 9", "weekday above 7"],
  ["0 9 32 * *", "day of month above 31"],
  // A step of 0 is not an interval; a step past the field's span matches only
  // its first value, so "*/90" runs hourly rather than every 90 minutes.
  ["*/0 * * * *", "zero step"],
  ["*/90 * * * *", "step wider than the field"],
  // cron ORs day-of-month with day-of-week, and every natural phrasing of that
  // reads as an AND.
  ["0 9 1 * 1", "both day fields restricted"],
  // A bounded interval: "every 10 minutes" would be a lie about a schedule
  // that only runs during the 9am hour.
  ["*/10 9 * * *", "interval bounded to an hour"],
  ["*/10 * * * 1", "interval bounded to a weekday"],
  // Every minute of one hour — a real schedule, just not one of the shapes
  // this module claims.
  ["* 9 * * *", "open minute with a fixed hour"],
  // Restricted month, and two days of the month: both need a sentence shape
  // this module does not have.
  ["0 9 * 3 *", "restricted month"],
  ["0 9 1,15 * *", "two days of the month"],
  // Not five fields, and not cron at all.
  ["0 9 * *", "four fields"],
  ["0 9 * * * *", "six fields"],
  ["", "empty"],
  ["not a cron", "prose"],
  // Half-typed: the schedule card's input is live, so this is what it sees on
  // the way to something valid. Nothing is better than a flash of the wrong
  // reading.
  ["*/", "half-typed step"],
  ["0 9 * * ", "trailing field missing"],
])("%s is refused (%s)", (expr) => {
  expect(describeCron(expr)).toBeNull();
});

test("describeCronSentence wraps the phrase, and passes the refusal through", () => {
  expect(describeCronSentence("*/5 * * * *")).toBe("Runs every 5 minutes.");
  expect(describeCronSentence("nonsense")).toBeNull();
});
