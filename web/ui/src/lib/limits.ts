// Formatting shared by the agent page's banner and Home's flags, so the same
// pause reads the same way on both.

// "Mon 1 Dec 00:00" — the day matters as much as the time, because a monthly
// pause lifts days away and a bare time would read as "later today".
export function formatResumes(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const parts = new Intl.DateTimeFormat("en-GB", {
    weekday: "short",
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).formatToParts(d);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
  return `${get("weekday")} ${get("day")} ${get("month")} ${get("hour")}:${get("minute")}`;
}

// The reason arrives as a lowercase fragment ("monthly limit of $30.00 reached").
export function pauseSentence(reason: string, until: string): string {
  const resumes = formatResumes(until);
  return `Paused — ${reason || "a spending limit was reached"}.${resumes ? ` Resumes ${resumes}.` : ""}`;
}
