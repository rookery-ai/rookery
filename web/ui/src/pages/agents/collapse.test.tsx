import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import {
  CollapsibleChecklist,
  visibleKeys,
  type ChecklistItem,
} from "./CollapsibleChecklist";

// The ranking rule, tested directly — it is the part of this component with
// real behaviour, and driving it through the DOM would only obscure which
// clause produced a given row.

function items(...specs: [key: string, priority: number][]): ChecklistItem[] {
  return specs.map(([key, priority]) => ({ key, label: key, priority }));
}

// Case 1 of the three the request describes: something is attached, so that is
// what a collapsed panel shows.
test("attached rows are shown, and fill the panel before anything else", () => {
  const pool = items(["a", 1], ["b", 1], ["c", 1], ["d", 1], ["e", 1], ["f", 1]);
  const got = visibleKeys(pool, new Set(["f"]), 5);

  expect(got.has("f")).toBe(true);
  expect(got.size).toBe(5);
});

// Case 2: nothing attached, so the owner's own items (priority 0) come first.
// This is what makes one ranking cover all three clauses instead of three
// branches that can disagree.
test("with nothing attached, the workspace's own items are preferred", () => {
  const pool = [
    ...items(["core1", 1], ["core2", 1], ["core3", 1], ["core4", 1], ["core5", 1]),
    ...items(["mine1", 0], ["mine2", 0]),
  ];
  const got = visibleKeys(pool, new Set(), 5);

  expect(got.has("mine1")).toBe(true);
  expect(got.has("mine2")).toBe(true);
  expect(got.size).toBe(5);
});

// Case 3: nothing attached and nothing of the owner's own — fall through to
// whatever is left, still capped.
test("with neither, it falls through to the built-in pool and still caps", () => {
  const pool = items(["c1", 1], ["c2", 1], ["c3", 1], ["c4", 1], ["c5", 1], ["c6", 1], ["c7", 1]);
  const got = visibleKeys(pool, new Set(), 5);

  expect(got.size).toBe(5);
  expect(got.has("c1")).toBe(true);
  expect(got.has("c7")).toBe(false);
});

// The deliberate departure from a flat cap. A checkbox panel that hides a
// CHECKED box misreports what the agent is bound to, which is worse than being
// long — so an agent with more attachments than the cap shows all of them.
test("more attachments than the cap shows every one of them", () => {
  const pool = items(["a", 1], ["b", 1], ["c", 1], ["d", 1], ["e", 1], ["f", 1], ["g", 1]);
  const got = visibleKeys(pool, new Set(["a", "b", "c", "d", "e", "f"]), 5);

  expect(got.size).toBe(6);
  for (const k of ["a", "b", "c", "d", "e", "f"]) expect(got.has(k)).toBe(true);
  expect(got.has("g")).toBe(false);
});

// ── Rendering ───────────────────────────────────────────────────────────────

function Harness({
  pool,
  initial = [],
}: {
  pool: ChecklistItem[];
  initial?: string[];
}) {
  const [checked, setChecked] = useState(new Set(initial));
  return (
    <>
      <CollapsibleChecklist
        items={pool}
        checked={checked}
        onToggle={(k) =>
          setChecked((prev) => {
            const next = new Set(prev);
            if (next.has(k)) next.delete(k);
            else next.add(k);
            return next;
          })
        }
        noun="skills"
      />
      {/* Stands in for the card's Save: what it would send is the Set, not the
          rendered rows. */}
      <output data-testid="would-save">{[...checked].sort().join(",")}</output>
    </>
  );
}

test("a long pool collapses to five rows behind a View all toggle", async () => {
  const pool = items(["a", 1], ["b", 1], ["c", 1], ["d", 1], ["e", 1], ["f", 1], ["g", 1]);
  render(<Harness pool={pool} />);

  expect(screen.getAllByRole("checkbox")).toHaveLength(5);

  await userEvent.click(screen.getByRole("button", { name: /view all 7 skills/i }));
  expect(screen.getAllByRole("checkbox")).toHaveLength(7);

  await userEvent.click(screen.getByRole("button", { name: /show fewer/i }));
  expect(screen.getAllByRole("checkbox")).toHaveLength(5);
});

test("a pool that already fits offers no toggle", () => {
  render(<Harness pool={items(["a", 1], ["b", 1])} />);

  expect(screen.getAllByRole("checkbox")).toHaveLength(2);
  expect(screen.queryByRole("button", { name: /view all/i })).toBeNull();
});

// Collapsing is display-only. The tempting implementation filters the pool
// before the checked Set is built, which drops bindings the user never touched
// — silently, since the panel looks correct either way.
test("a row checked while expanded survives collapsing, and still saves", async () => {
  const pool = items(["a", 1], ["b", 1], ["c", 1], ["d", 1], ["e", 1], ["f", 1], ["g", 1]);
  // g is beyond the cap, so it is not rendered at all until the panel expands.
  render(<Harness pool={pool} initial={[]} />);
  expect(screen.queryByRole("checkbox", { name: "g" })).toBeNull();

  await userEvent.click(screen.getByRole("button", { name: /view all 7 skills/i }));
  await userEvent.click(screen.getByRole("checkbox", { name: "g" }));
  await userEvent.click(screen.getByRole("button", { name: /show fewer/i }));

  // Collapsing changed what is rendered and nothing else: g is attached now,
  // so it is kept, and the set the card would PUT is exactly what was ticked.
  expect(screen.getByRole("checkbox", { name: "g" })).toBeChecked();
  expect(screen.getAllByRole("checkbox")).toHaveLength(5);
  expect(screen.getByTestId("would-save").textContent).toBe("g");
});

// A section whose rows are all hidden must not leave its heading behind — an
// empty "Your skills" label reads as "you have none", which is the opposite of
// what it would mean.
test("a section with no visible rows renders no heading", async () => {
  const pool: ChecklistItem[] = [
    ...["c1", "c2", "c3", "c4", "c5"].map((k) => ({
      key: k, label: k, section: "Core", priority: 0,
    })),
    { key: "mine", label: "mine", section: "Your skills", priority: 1 },
  ];
  render(<Harness pool={pool} />);

  expect(screen.queryByText("Your skills")).toBeNull();
  expect(screen.getByText("Core")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: /view all 6 skills/i }));
  expect(screen.getByText("Your skills")).toBeInTheDocument();
});
