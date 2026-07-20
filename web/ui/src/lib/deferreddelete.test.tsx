import { render, screen, fireEvent, act } from "@testing-library/react";
import { ToastProvider, ToastHost } from "@/components/shell/Toast";
import { useDeferredDelete } from "./useDeferredDelete";

// The toast host renders a toast's message in two places (a visually-hidden
// aria-live announcer and the visible toast body) — scope to the visible
// <span> to disambiguate, matching the established helper in
// components/shell/toast.test.tsx.
function toastBody(text: string | RegExp) {
  return screen.getByText(text, { selector: "span" });
}

type HarnessProps = {
  commit: (id: string) => Promise<unknown>;
  onRestore: (id: string) => void;
};

// Minimal item list standing in for an inbox/reminder row: "schedule" hides
// the row via the hook's `pending` set (what the caller is expected to
// filter its rendered list on), "flush" simulates a navigation-away.
function Harness({ commit, onRestore }: HarnessProps) {
  const { schedule, flushAll, pending } = useDeferredDelete({ commit, onRestore });
  const items = [{ id: "m1", body: "m1 body" }];
  return (
    <div>
      {items
        .filter((i) => !pending.has(i.id))
        .map((i) => (
          <p key={i.id}>{i.body}</p>
        ))}
      <button onClick={() => schedule("m1", "Message deleted")}>delete m1</button>
      <button onClick={() => flushAll()}>flush</button>
    </div>
  );
}

function wrap(ui: React.ReactNode) {
  return render(
    <ToastProvider>
      {ui}
      <ToastHost />
    </ToastProvider>,
  );
}

afterEach(() => {
  vi.useRealTimers();
});

test("delete hides row and does NOT call the API within the window", () => {
  vi.useFakeTimers();
  const commit = vi.fn();
  const onRestore = vi.fn();
  wrap(<Harness commit={commit} onRestore={onRestore} />);

  expect(screen.getByText("m1 body")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));

  expect(screen.queryByText("m1 body")).not.toBeInTheDocument();
  expect(commit).not.toHaveBeenCalled();

  // Still within the 5s window — no commit yet.
  act(() => {
    vi.advanceTimersByTime(4999);
  });
  expect(commit).not.toHaveBeenCalled();
});

test("undo cancels the call entirely", () => {
  vi.useFakeTimers();
  const commit = vi.fn();
  const onRestore = vi.fn();
  wrap(<Harness commit={commit} onRestore={onRestore} />);

  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));
  fireEvent.click(screen.getByRole("button", { name: "Undo" }));

  // Undo brought the row back immediately.
  expect(screen.getByText("m1 body")).toBeInTheDocument();

  act(() => {
    vi.advanceTimersByTime(10000);
  });
  expect(commit).not.toHaveBeenCalled();
});

test("expiry commits", () => {
  vi.useFakeTimers();
  const commit = vi.fn();
  const onRestore = vi.fn();
  wrap(<Harness commit={commit} onRestore={onRestore} />);

  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));
  act(() => {
    vi.advanceTimersByTime(5000);
  });

  expect(commit).toHaveBeenCalledWith("m1");
  expect(commit).toHaveBeenCalledTimes(1);
});

test("failed commit restores the row and shows an error toast", async () => {
  vi.useFakeTimers();
  const commit = vi.fn().mockRejectedValueOnce(new Error("boom"));
  const onRestore = vi.fn();
  wrap(<Harness commit={commit} onRestore={onRestore} />);

  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));

  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });

  expect(onRestore).toHaveBeenCalledWith("m1");
  expect(toastBody(/couldn't delete/i)).toBeInTheDocument();
  expect(screen.getByText("m1 body")).toBeInTheDocument();
});

test("flushAll commits pending deletes immediately (navigation/unmount)", () => {
  vi.useFakeTimers();
  const commit = vi.fn();
  const onRestore = vi.fn();
  wrap(<Harness commit={commit} onRestore={onRestore} />);

  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));
  expect(commit).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "flush" }));

  expect(commit).toHaveBeenCalledWith("m1");
});

test("scheduling the same id twice does not orphan a timer that double-commits", () => {
  vi.useFakeTimers();
  const commit = vi.fn();
  const onRestore = vi.fn();
  wrap(<Harness commit={commit} onRestore={onRestore} />);

  // Two schedule() calls for the same id (e.g. a second delete click racing
  // a flush). Before the fix, the first timer is left in place but
  // unreferenced once the map entry is overwritten by the second.
  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));
  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));

  fireEvent.click(screen.getByRole("button", { name: "flush" }));
  expect(commit).toHaveBeenCalledTimes(1);

  // Advance past the 5s window so the orphaned first timer, if it still
  // exists, fires and calls commit a second time.
  act(() => {
    vi.advanceTimersByTime(5000);
  });

  expect(commit).toHaveBeenCalledTimes(1);
});

test("unmounting the component (route change away) flushes a pending delete", () => {
  vi.useFakeTimers();
  const commit = vi.fn();
  const onRestore = vi.fn();
  const { unmount } = wrap(<Harness commit={commit} onRestore={onRestore} />);

  fireEvent.click(screen.getByRole("button", { name: "delete m1" }));
  expect(commit).not.toHaveBeenCalled();

  unmount();

  expect(commit).toHaveBeenCalledWith("m1");
});
