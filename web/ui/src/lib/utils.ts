import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// Compact relative-time label for session list rows: "2m ago" / "3h ago" /
// "Jul 12" once it's more than a week old. Falls back to "" for unparsable
// input rather than "Invalid Date ago".
export function timeAgo(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ""
  const diffSec = Math.floor((Date.now() - then) / 1000)
  if (diffSec < 60) return "just now"
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return `${diffDay}d ago`
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

// formatShortDate renders an ISO timestamp as a short local date+time,
// e.g. "Jul 23, 15:04" — the small "kept timestamp" shown under a chat's
// content-derived title.
export function formatShortDate(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ""
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  })
}

// formatMessageTime renders a chat message's timestamp as "Sun, 21:00" — a
// short weekday plus a 24-hour clock, no seconds and no date, which is what a
// per-message footer needs (the day is enough context inside one conversation).
//
// `timeZone` is the workspace profile's timezone, a FREE-TEXT settings field:
// it can legitimately hold "", "CEST" or "UTC+2", none of which Intl accepts —
// it throws RangeError. This runs during render for every bubble, so a throw
// would blank the whole conversation. Hence the try/catch fallback to the
// browser's own zone, mirroring Go's profile.LoadLocation, which likewise
// degrades (to UTC) rather than failing on an unparseable zone.
export function formatMessageTime(iso: string, timeZone?: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ""
  const opts: Intl.DateTimeFormatOptions = {
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }
  if (timeZone) {
    try {
      return d.toLocaleString(undefined, { ...opts, timeZone })
    } catch {
      // Not a valid IANA zone — fall through to browser-local below.
    }
  }
  return d.toLocaleString(undefined, opts)
}
