/// <reference types="node" />
import { render, screen } from "@testing-library/react";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { SearchInput } from "./search-input";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((e) => {
    const p = path.join(dir, e);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(e) && !/\.test\.tsx?$/.test(e) ? [p] : [];
  });
}

// The attributes are the whole fix. Chrome's password manager wrote the saved
// account email into the connections page's filter box — once via
// username/password pairing with the Connect wizard's secret field, and once
// via the single-username-field heuristic on the OAuth callback's full page
// load. Both narrowed the page to "No services match <email>", which read as a
// broken redirect. type="search" is what makes the box ineligible for both.
test("SearchInput opts out of browser and password-manager autofill", () => {
  render(<SearchInput aria-label="Search things" />);
  const box = screen.getByRole("searchbox", { name: "Search things" });
  expect(box).toHaveAttribute("type", "search");
  expect(box).toHaveAttribute("autocomplete", "off");
  expect(box).toHaveAttribute("data-1p-ignore");
  expect(box).toHaveAttribute("data-lpignore", "true");
});

test("SearchInput keeps caller classes alongside its own", () => {
  render(<SearchInput aria-label="Search things" className="w-56 pl-8" />);
  const box = screen.getByRole("searchbox", { name: "Search things" });
  expect(box.className).toContain("w-56");
  expect(box.className).toContain("appearance-none");
});

/**
 * The source text of every self-closing `<Input … />` in a file.
 *
 * A regexp cannot do this: an `onChange={(e) => …}` prop contains a `>`, so
 * the obvious `<Input[^>]*>` stops mid-element and silently reports a field as
 * clean because it never reached the attribute being checked. Scanning to the
 * `/>` that closes the element is the only way to see all of its props.
 */
function inputElements(src: string): string[] {
  const out: string[] = [];
  for (const m of src.matchAll(/<Input\b/g)) {
    const end = src.indexOf("/>", m.index);
    if (end !== -1) out.push(src.slice(m.index, end + 2));
  }
  return out;
}

// A raw <Input> used as a search box is the bug re-introduced. Every filter
// input in the app goes through SearchInput so the opt-out cannot be forgotten
// at one call site — which is exactly how this shipped.
test("no search box is a raw Input", () => {
  const offenders = sources(srcRoot)
    .map((f) => [path.relative(srcRoot, f), readFileSync(f, "utf8")] as const)
    .filter(([, src]) =>
      inputElements(src).some((el) => /aria-label="Search/i.test(el)),
    )
    .map(([f]) => f);
  expect(offenders).toEqual([]);
});

// Chrome ignores autocomplete="off" on password fields and pairs them with a
// nearby text input it fills as the username. "new-password" opts out.
//
// Scoped to the connections surfaces on purpose. The rule is right for a
// THIRD-PARTY secret (an OAuth client secret, an API key) but wrong for the
// owner's own account/master password — Login, LockScreen, ChangePassword,
// Workspaces, OwnerGate, SetupWizard and the master-password section of
// settings SHOULD stay autofill-eligible, because there the password manager
// is doing its job. The same treatment is applied by hand to the other
// third-party secret fields (secrets, backup, coder key, search keys); only
// here is it pinned, because this is where the bug was reported.
test("connections secret fields use new-password, not off", () => {
  const offenders = sources(path.join(srcRoot, "pages/connections"))
    .map((f) => [path.relative(srcRoot, f), readFileSync(f, "utf8")] as const)
    .flatMap(([f, src]) =>
      inputElements(src)
        .filter(
          (el) =>
            /type=(["'])password\1|type=\{[^}]*password/.test(el) &&
            !/autoComplete=(["'])new-password\1|autoComplete=\{[^}]*new-password/.test(
              el,
            ),
        )
        .map(() => f),
    );
  expect(offenders).toEqual([]);
});
