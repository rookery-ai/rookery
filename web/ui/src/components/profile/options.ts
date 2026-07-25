// Option lists for the curated profile selects, shared by the Settings profile
// section and the setup wizard so the two can't drift apart.

export type Option = { value: string; label: string };

// Timezones come from the platform's own tzdb rather than a vendored list:
// the value has to survive Go's time.LoadLocation (it drives reminder firing),
// and Intl is the only source guaranteed to agree with the host's zoneinfo.
//
// Intl.supportedValuesOf is not in older Safari, hence the fallback — a short
// spread of common zones is far better than an empty dropdown.
const FALLBACK_TIMEZONES = [
  "UTC",
  "Europe/London",
  "Europe/Berlin",
  "Europe/Skopje",
  "Europe/Moscow",
  "America/New_York",
  "America/Chicago",
  "America/Denver",
  "America/Los_Angeles",
  "America/Sao_Paulo",
  "Asia/Dubai",
  "Asia/Kolkata",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Australia/Sydney",
];

export function timezoneOptions(): Option[] {
  let zones: string[];
  try {
    const supported = (
      Intl as unknown as { supportedValuesOf?: (k: string) => string[] }
    ).supportedValuesOf;
    zones = supported ? supported("timeZone") : FALLBACK_TIMEZONES;
  } catch {
    zones = FALLBACK_TIMEZONES;
  }
  if (!zones.length) zones = FALLBACK_TIMEZONES;
  // Intl lists 418 region zones but no plain "UTC" (and no Etc/* aliases),
  // while Go's time.LoadLocation accepts it and it is a legitimate choice for
  // a server-minded user. Offer it first rather than leaving it unreachable.
  if (!zones.includes("UTC")) zones = ["UTC", ...zones];
  return zones.map((z) => ({ value: z, label: z.replace(/_/g, " ") }));
}

// ISO-3166 alpha-2 codes; the visible names come from Intl.DisplayNames so the
// list stays correctly spelled and localized without shipping a name table.
// The stored VALUE is the country name, not the code — this field is
// descriptive context handed to the LLM, and a bare "MK" reads as noise in a
// prompt where "North Macedonia" reads as a fact.
const COUNTRY_CODES = [
  "AL","AR","AT","AU","BA","BE","BG","BR","CA","CH","CL","CN","CO","CZ","DE","DK",
  "EE","EG","ES","FI","FR","GB","GR","HK","HR","HU","ID","IE","IL","IN","IS","IT",
  "JP","KE","KR","LT","LU","LV","MA","MD","ME","MK","MT","MX","MY","NG","NL","NO",
  "NZ","PE","PH","PK","PL","PT","RO","RS","RU","SA","SE","SG","SI","SK","TH","TR",
  "TW","UA","US","VN","ZA",
];

export function countryOptions(): Option[] {
  let display: Intl.DisplayNames | null = null;
  try {
    display = new Intl.DisplayNames(["en"], { type: "region" });
  } catch {
    display = null;
  }
  return COUNTRY_CODES.map((code) => {
    const label = display?.of(code) ?? code;
    return { value: label, label };
  }).sort((a, b) => a.label.localeCompare(b.label));
}

// Stored as the plain English language name, which is what the profile has
// always held and what the prompt layer injects verbatim.
export const LANGUAGE_OPTIONS: Option[] = [
  "Arabic", "Bulgarian", "Chinese", "Croatian", "Czech", "Danish", "Dutch",
  "English", "Estonian", "Finnish", "French", "German", "Greek", "Hebrew",
  "Hindi", "Hungarian", "Indonesian", "Italian", "Japanese", "Korean",
  "Latvian", "Lithuanian", "Macedonian", "Norwegian", "Polish", "Portuguese",
  "Romanian", "Russian", "Serbian", "Slovak", "Slovenian", "Spanish",
  "Swedish", "Thai", "Turkish", "Ukrainian", "Vietnamese",
].map((l) => ({ value: l, label: l }));

// A small vocabulary on purpose. Tone is injected into the system prompt, and
// a handful of distinct, unambiguous words steer a model far more reliably
// than free text, where "casual but professional, not too chatty" tends to
// cancel itself out.
export const TONE_OPTIONS: Option[] = [
  { value: "direct", label: "Direct — short, no filler" },
  { value: "friendly", label: "Friendly — warm and conversational" },
  { value: "concise", label: "Concise — brief but complete" },
  { value: "detailed", label: "Detailed — thorough explanations" },
  { value: "formal", label: "Formal — professional register" },
  { value: "casual", label: "Casual — relaxed and informal" },
  { value: "encouraging", label: "Encouraging — supportive and positive" },
  { value: "neutral", label: "Neutral — plain and matter-of-fact" },
];
