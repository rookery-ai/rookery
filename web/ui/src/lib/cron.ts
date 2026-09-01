// Turning a cron expression into a sentence.
//
// THE RULE THIS MODULE IS BUILT AROUND, inherited from the narrower version
// that lived in SpecPanel: a plausible-but-wrong plain-language schedule is
// worse than showing the raw cron, because the user has no way to tell it is
// wrong. So `describeCron` returns null for anything it cannot prove, and every
// caller falls back to the expression itself. Widening the covered shapes is
// exactly the moment that rule gets lost — each branch below re-checks its
// values against the field's real range before emitting a word.
//
// It is deliberately not a general cron parser. It recognises an enumerated set
// of field shapes and refuses everything else.

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

// Cron accepts three-letter day names as well as numbers.
const WEEKDAY_NAMES: Record<string, number> = {
  sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6,
};

type Field =
  | { kind: "all" }
  | { kind: "step"; step: number }
  | { kind: "list"; values: number[] };

// parseField reads ONE cron field, or returns null if it is anything this
// module has not proven it understands. Out-of-range values are a refusal, not
// a clamp: "0 25 * * *" is not a schedule at 01:00, it is an expression whose
// meaning we should not guess at.
function parseField(
  spec: string,
  min: number,
  max: number,
  names?: Record<string, number>,
): Field | null {
  const s = spec.trim().toLowerCase();
  if (s === "*") return { kind: "all" };

  const step = s.match(/^\*\/(\d+)$/);
  if (step) {
    const n = Number(step[1]);
    // A step of 0 is not an interval. A step larger than the field's span only
    // ever matches its first value — "*/90" in a 0-59 minute field runs
    // HOURLY, not every 90 minutes — so describing it as an interval would be
    // confidently wrong.
    if (n < 1 || n > max - min + 1) return null;
    return { kind: "step", step: n };
  }

  const values = new Set<number>();
  for (const part of s.split(",")) {
    const range = part.match(/^([a-z0-9]+)-([a-z0-9]+)$/);
    if (range) {
      const lo = readValue(range[1]!, names);
      const hi = readValue(range[2]!, names);
      if (lo === null || hi === null || lo > hi) return null;
      if (lo < min || hi > max) return null;
      for (let v = lo; v <= hi; v++) values.add(v);
      continue;
    }
    const v = readValue(part, names);
    if (v === null || v < min || v > max) return null;
    values.add(v);
  }
  if (values.size === 0) return null;
  return { kind: "list", values: [...values].sort((a, b) => a - b) };
}

function readValue(tok: string, names?: Record<string, number>): number | null {
  if (names && tok in names) return names[tok]!;
  if (!/^\d+$/.test(tok)) return null;
  return Number(tok);
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

// "a", "a and b", "a, b and c" — the shape English actually uses, so a
// three-day schedule does not read like a CSV row.
function joinWords(parts: string[]): string {
  if (parts.length <= 1) return parts[0] ?? "";
  return `${parts.slice(0, -1).join(", ")} and ${parts.at(-1)}`;
}

function times(hours: number[], minute: number): string {
  return joinWords(hours.map((h) => `${pad(h)}:${pad(minute)}`));
}

// Weekdays are named in the order the cron field's OWN numbering implies —
// Sunday first, because cron numbers Sunday 0. "1,6" and "6,1" are the same
// schedule, so rendering them as two different sentences would make an
// identical schedule read as two.
function dayNames(values: number[]): string {
  // Cron accepts both 0 and 7 for Sunday, so fold before naming or a schedule
  // written "0,7" would say Sunday twice.
  const folded = [...new Set(values.map((v) => v % 7))].sort((a, b) => a - b);
  const key = folded.join(",");
  if (key === "1,2,3,4,5") return "every weekday";
  if (key === "0,6") return "every weekend day";
  return `every ${joinWords(folded.map((d) => WEEKDAYS[d]!))}`;
}

/**
 * describeCron turns a 5-field cron expression into a lowercase phrase like
 * "every 10 minutes" or "every Monday and Saturday at 08:00", or returns null
 * when it cannot prove a reading.
 */
export function describeCron(expr: string): string | null {
  const parts = expr.trim().split(/\s+/);
  if (parts.length !== 5) return null;

  const minute = parseField(parts[0]!, 0, 59);
  const hour = parseField(parts[1]!, 0, 23);
  const dom = parseField(parts[2]!, 1, 31);
  const month = parseField(parts[3]!, 1, 12);
  const dow = parseField(parts[4]!, 0, 7, WEEKDAY_NAMES);
  if (!minute || !hour || !dom || !month || !dow) return null;

  // A restricted month makes the sentence a different shape ("in March and
  // September"), and nothing in this product generates one. Refusing is
  // cheaper than prose nobody will read closely.
  if (month.kind !== "all") return null;

  // When BOTH day-of-month and day-of-week are restricted, cron ORs them —
  // "0 9 1 * 1" fires on the 1st AND on every Monday. Every natural phrasing
  // of that reads as an AND, so this is precisely the case where confident
  // prose would be wrong. Refuse it.
  if (dom.kind !== "all" && dow.kind !== "all") return null;

  // "every N minutes" — the whole rest of the expression must be open, or the
  // interval is bounded to certain hours or days and the phrase is a lie.
  if (minute.kind === "step") {
    if (hour.kind !== "all" || dom.kind !== "all" || dow.kind !== "all") return null;
    return minute.step === 1 ? "every minute" : `every ${minute.step} minutes`;
  }

  // Every remaining shape names a specific minute, so a minute field that is
  // still open ("* 9 * * *" — every minute of the 9am hour) is out of scope.
  if (minute.kind !== "list" || minute.values.length !== 1) return null;
  const m = minute.values[0]!;

  // "every N hours", at a fixed minute past.
  if (hour.kind === "step") {
    if (dom.kind !== "all" || dow.kind !== "all") return null;
    const every = hour.step === 1 ? "every hour" : `every ${hour.step} hours`;
    return m === 0 ? every : `${every} at :${pad(m)}`;
  }

  // Hourly: the hour field is open, so only the minute distinguishes it.
  if (hour.kind === "all") {
    if (dom.kind !== "all" || dow.kind !== "all") return null;
    return m === 0 ? "every hour" : `every hour at :${pad(m)}`;
  }

  const hs = hour.values;

  if (dow.kind === "list") return `${dayNames(dow.values)} at ${times(hs, m)}`;

  if (dom.kind === "list") {
    if (dom.values.length !== 1) return null; // "the 1st and 15th" — not proven
    return `on day ${dom.values[0]} of every month at ${times(hs, m)}`;
  }

  return `every day at ${times(hs, m)}`;
}

/**
 * describeCronSentence is the card-facing form: a capitalised sentence saying
 * what the schedule does, or null when the expression cannot be described.
 */
export function describeCronSentence(expr: string): string | null {
  const phrase = describeCron(expr);
  if (!phrase) return null;
  return `Runs ${phrase}.`;
}
