import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";

const src = readFileSync(join(__dirname, "SetupWizard.tsx"), "utf8");

// The Done screen's two buttons pass /agents/new and /kb, both real routes —
// yet both landed on home. The cause was ordering, not the targets:
//
//   await api.post("/api/v1/setup", { step: 7 });
//   await qc.invalidateQueries({ queryKey: ["session"] });  // refetch lands
//   nav(target);                                            // too late
//
// Awaiting the invalidation resolves only AFTER the session refetch, so
// `needs_setup` was already false while /setup was still the matched route,
// and RequireSetupWorkspace's <Navigate to="/" replace /> fired first. Being
// target-independent is exactly why BOTH buttons appeared to be mislinked.
//
// This is a source guard rather than a behavioural test on purpose. The
// existing destination tests (SetupWizard.test.tsx) mount SetupWizard directly,
// so RequireSetupWorkspace is not in the tree, and their session fixture pins
// needs_setup:true so nothing ever flips — they passed against the bug and
// would pass against it again. Reproducing it needs the real router plus a
// fixture that flips mid-flow; asserting the ordering catches the regression
// that actually reintroduces it: re-adding the `await`.
describe("setup finish navigation", () => {
  const finish = src.slice(src.indexOf("async function finish("));
  const body = finish.slice(0, finish.indexOf("\n  }"));

  test("navigates before invalidating the session", () => {
    const navAt = body.indexOf("nav(target)");
    const invalidateAt = body.indexOf("invalidateQueries");
    expect(navAt).toBeGreaterThan(-1);
    expect(invalidateAt).toBeGreaterThan(-1);
    expect(navAt).toBeLessThan(invalidateAt);
  });

  test("does not await the session invalidation", () => {
    expect(body).not.toMatch(/await\s+qc\.invalidateQueries/);
  });
});
