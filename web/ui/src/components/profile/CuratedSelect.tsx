import { useMemo } from "react";
import type { Option } from "./options";

// CuratedSelect is the profile fields' shared dropdown, used by both the
// Settings profile section and the setup wizard so the two offer the same
// choices. Kept a native <select> to match FolderSelect and to keep the
// browser's own type-ahead — with ~400 timezones, typing "Eur" to jump is the
// difference between usable and not.
//
// The load-bearing behaviour is `value` preservation: these fields were free
// text before, so an existing profile can hold anything ("Skopje, North
// Macedonia", "Europe/Skopje", "direct but friendly"). A stored value that
// isn't in the list is added as an option and stays selected, so opening
// Settings can never silently blank a saved preference — it would be saved
// back as "" on the next submit, losing data the user never touched.
export function CuratedSelect({
  id,
  value,
  onChange,
  options,
  placeholder = "Not set",
  className,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
  options: Option[];
  placeholder?: string;
  className?: string;
}) {
  const merged = useMemo(() => {
    if (!value) return options;
    if (options.some((o) => o.value === value)) return options;
    return [{ value, label: `${value} (current)` }, ...options];
  }, [options, value]);

  return (
    <select
      id={id}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={
        className ??
        "h-9 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
      }
    >
      <option value="">{placeholder}</option>
      {merged.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}
