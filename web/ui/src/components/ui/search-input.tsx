import * as React from "react";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * The app's one search box.
 *
 * It exists to keep every filter input OUT of the browser password manager's
 * reach. A bare `<input type="text">` with no `autocomplete` is autofill-
 * eligible, and Chrome will write the origin's saved account email into it
 * under two separate heuristics:
 *
 *   1. Username/password pairing — when a `type="password"` field is used
 *      anywhere on the page (the connections page's Connect wizard collects a
 *      client secret / API key), Chrome fills the nearest eligible text input
 *      as the "username". The connections search box was that input.
 *   2. Single-username-field — on a full page load (the OAuth callback is a
 *      server redirect back to /connections), a lone unmarked text input gets
 *      filled from saved credentials even with no password field present.
 *
 * Because these are controlled inputs, autofill dispatches a real input event,
 * React's onChange runs, and the filter genuinely narrows to zero matches —
 * the page looked broken ("No services match <your email>") when the only
 * thing wrong was the query.
 *
 * `type="search"` is the fix that actually bites: Chrome excludes search
 * fields from both heuristics. `autoComplete="off"` alone does not (it is
 * widely ignored for password-manager fill). The `data-*-ignore` attributes
 * opt out of 1Password and LastPass, which run their own heuristics.
 *
 * The UA styling that comes with `type="search"` is neutralised here so this
 * renders identically to a plain `Input`.
 */
function SearchInput({
  className,
  ...props
}: Omit<React.ComponentProps<typeof Input>, "type">) {
  return (
    <Input
      type="search"
      autoComplete="off"
      spellCheck={false}
      data-1p-ignore=""
      data-lpignore="true"
      className={cn(
        "appearance-none [&::-webkit-search-cancel-button]:hidden [&::-webkit-search-decoration]:hidden",
        className,
      )}
      {...props}
    />
  );
}

export { SearchInput };
