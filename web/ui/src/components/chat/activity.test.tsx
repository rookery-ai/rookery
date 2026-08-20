import { render, screen, fireEvent, act } from "@testing-library/react";
import { ActivityCard } from "./ActivityCard";

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-07-17T10:00:00Z"));
});

afterEach(() => {
  vi.useRealTimers();
});

// The card carries `overflow-hidden` (for its rounded corners), and per CSS
// Flexbox 4.5 an item whose overflow is not `visible` gets an automatic minimum
// size of ZERO instead of a content-based one. So as a direct flex child of a
// full ChatScroll it is the ONE child flex is allowed to compress — measured in
// Chromium at 77px with room to spare and 2px once the transcript filled the
// container. Two pixels is the top and bottom border: the owner reported it as
// "a line and no box with the tool calls at all".
//
// Message bubbles are unaffected because their overflow is visible, which is
// why the reply still arrived normally while the progress card vanished, and
// why the agent designer never showed this — it wraps the card in a plain div,
// so that div is the flex item and the card is not.
//
// jsdom has no layout engine, so this can only assert the declaration. The
// behaviour is asserted in scripts/verify-chat-progress-layout.py.
test("never shrinks: it is a flex child that would otherwise collapse to its border", () => {
  render(
    <ActivityCard title="Working" lines={["🔧 read_file(a.md)"]} status="live" startedAt={Date.now()} />,
  );
  expect(screen.getByTestId("activity-card").className).toContain("shrink-0");
});

test("renders title and lines in arrival order", () => {
  render(
    <ActivityCard
      title="Building your agent…"
      lines={["⚙️ Preparing workspace…", "🔧 run_script(...)"]}
      status="live"
      startedAt={Date.now()}
      collapsible
    />,
  );
  expect(screen.getByText("Building your agent…")).toBeInTheDocument();
  expect(screen.getByText("⚙️ Preparing workspace…")).toBeInTheDocument();
  expect(screen.getByText("🔧 run_script(...)")).toBeInTheDocument();
});

test("ticks elapsed mm:ss while live", () => {
  const startedAt = Date.now();
  render(
    <ActivityCard title="Building your agent…" lines={[]} status="live" startedAt={startedAt} />,
  );
  expect(screen.getByTestId("activity-elapsed")).toHaveTextContent("0:00");

  act(() => {
    vi.advanceTimersByTime(62_000);
  });
  expect(screen.getByTestId("activity-elapsed")).toHaveTextContent("1:02");
});

test("stops ticking once status is done", () => {
  const startedAt = Date.now();
  const { rerender } = render(
    <ActivityCard title="Building your agent…" lines={[]} status="live" startedAt={startedAt} />,
  );
  act(() => {
    vi.advanceTimersByTime(5_000);
  });
  expect(screen.getByTestId("activity-elapsed")).toHaveTextContent("0:05");

  rerender(
    <ActivityCard title="Building your agent…" lines={[]} status="done" startedAt={startedAt} />,
  );
  act(() => {
    vi.advanceTimersByTime(10_000);
  });
  // Elapsed freezes at the point status stopped being live, not the wall clock.
  expect(screen.getByTestId("activity-elapsed")).toHaveTextContent("0:05");
});

test("unmount clears the tick interval (not redundant with the status-change test above — that one exercises the effect's dep-change cleanup, this one exercises unmount cleanup)", () => {
  const clearSpy = vi.spyOn(globalThis, "clearInterval");
  const { unmount } = render(
    <ActivityCard title="Building your agent…" lines={[]} status="live" startedAt={Date.now()} />,
  );
  const callsBeforeUnmount = clearSpy.mock.calls.length;
  unmount();
  expect(clearSpy.mock.calls.length).toBeGreaterThan(callsBeforeUnmount);
  clearSpy.mockRestore();
});

test("collapsible: collapsed shows only the last line, expanded shows all", () => {
  render(
    <ActivityCard
      title="Building your agent…"
      lines={["✓ step one", "✓ step two", "🔧 doing a thing"]}
      status="live"
      startedAt={Date.now()}
      collapsible
    />,
  );
  // Starts expanded per the mockup (all lines visible).
  expect(screen.getByText("✓ step one")).toBeInTheDocument();
  expect(screen.getByText("🔧 doing a thing")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("activity-toggle"));
  expect(screen.queryByText("✓ step one")).not.toBeInTheDocument();
  expect(screen.getByText("🔧 doing a thing")).toBeInTheDocument(); // last line only

  fireEvent.click(screen.getByTestId("activity-toggle"));
  expect(screen.getByText("✓ step one")).toBeInTheDocument();
});

test("without collapsible there is no toggle and all lines always show", () => {
  render(
    <ActivityCard
      title="Building your agent…"
      lines={["✓ step one", "🔧 doing a thing"]}
      status="live"
      startedAt={Date.now()}
    />,
  );
  expect(screen.queryByTestId("activity-toggle")).not.toBeInTheDocument();
  expect(screen.getByText("✓ step one")).toBeInTheDocument();
  expect(screen.getByText("🔧 doing a thing")).toBeInTheDocument();
});

test("checkmark lines are tinted ok", () => {
  render(
    <ActivityCard
      title="Building your agent…"
      lines={["✓ Connected to Gmail", "🔧 Testing…"]}
      status="live"
      startedAt={Date.now()}
    />,
  );
  expect(screen.getByText("✓ Connected to Gmail")).toHaveClass("text-ok");
  expect(screen.getByText("🔧 Testing…")).not.toHaveClass("text-ok");
});

test("error status tints the card danger", () => {
  render(
    <ActivityCard
      title="Building your agent…"
      lines={["✓ step one", "Something went wrong"]}
      status="error"
      startedAt={Date.now()}
    />,
  );
  expect(screen.getByTestId("activity-card")).toHaveClass("border-danger");
  expect(screen.getByTestId("activity-status-dot")).toHaveClass("bg-danger");
});

test("live status dot pulses, done status dot is solid green", () => {
  const { rerender } = render(
    <ActivityCard title="Building your agent…" lines={[]} status="live" startedAt={Date.now()} />,
  );
  expect(screen.getByTestId("activity-status-dot")).toHaveClass("animate-pulse");
  expect(screen.getByTestId("activity-status-dot")).toHaveClass("bg-ok");

  rerender(
    <ActivityCard title="Building your agent…" lines={[]} status="done" startedAt={Date.now()} />,
  );
  expect(screen.getByTestId("activity-status-dot")).not.toHaveClass("animate-pulse");
  expect(screen.getByTestId("activity-status-dot")).toHaveClass("bg-ok");
});
