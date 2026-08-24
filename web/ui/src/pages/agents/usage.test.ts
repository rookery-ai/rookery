import { expect, test } from "vitest";
import { formatCostUSD } from "./AgentDetailPage";

// A run in this product costs on the order of $0.0002. Two decimal places would
// render every one of them as "$0.00" — a rounding that reads as a claim the
// agent is free, which is the common case here rather than an edge case.
test("a sub-cent charge is not rounded away to zero", () => {
  expect(formatCostUSD(0.000228)).toBe("$0.000228");
});

// Where cents matter, "$12.34" is what someone checking a bill expects to read.
test("an amount where cents matter uses two decimals", () => {
  expect(formatCostUSD(12.5)).toBe("$12.50");
});

// A genuine zero is still a zero — the small-amount branch must not swallow it.
test("a genuine zero renders as $0.00", () => {
  expect(formatCostUSD(0)).toBe("$0.00");
});

// Must agree with vault.FormatCostUSD, which renders the same figure into the
// run's knowledge-base note. Two precisions for one run would make the panel and
// the note disagree about what it cost, and a number nobody can cross-check is
// worse than no number.
test("the boundary matches the Go formatter's", () => {
  expect(formatCostUSD(0.009999)).toBe("$0.009999");
  expect(formatCostUSD(0.01)).toBe("$0.01");
});
