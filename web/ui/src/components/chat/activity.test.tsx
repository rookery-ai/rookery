import { render, screen, fireEvent, act } from "@testing-library/react";
import { ActivityCard } from "./ActivityCard";

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-07-17T10:00:00Z"));
});

afterEach(() => {
  vi.useRealTimers();
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
