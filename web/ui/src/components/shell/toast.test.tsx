import { render, screen, fireEvent, act } from "@testing-library/react";
import { ToastProvider, ToastHost, useToast, type ToastAction } from "./Toast";

function Harness({ message, action }: { message: string; action?: ToastAction }) {
  const { toast } = useToast();
  return <button onClick={() => toast({ message, action })}>go</button>;
}

// The live region intentionally mirrors the visible toast's text (that's
// the whole point — a screen reader announces it), so a plain getByText
// matches both. Scope to the visible toast body (a <span>, not the
// aria-live <div>) to disambiguate.
function toastBody(text: string) {
  return screen.getByText(text, { selector: "span" });
}
function queryToastBody(text: string) {
  return screen.queryByText(text, { selector: "span" });
}

function wrap(ui: React.ReactNode) {
  return render(
    <ToastProvider>
      {ui}
      <ToastHost />
    </ToastProvider>,
  );
}

// Interactions below use fireEvent, not userEvent, because they run under
// fake timers: userEvent's internal pointer machinery relies on its own
// setTimeout calls, and awaiting it while vi.useFakeTimers() is active
// deadlocks (matching the established pattern in
// search/CommandPalette.test.tsx and pages/kb/search.test.tsx).
afterEach(() => {
  vi.useRealTimers();
});

test("toast renders, auto-dismisses after 5s by default, and announces politely", () => {
  vi.useFakeTimers();
  wrap(<Harness message="Saved" />);

  fireEvent.click(screen.getByRole("button", { name: /go/i }));
  expect(toastBody("Saved")).toBeInTheDocument();

  const live = document.querySelector('[aria-live="polite"]');
  expect(live).toHaveTextContent("Saved");

  act(() => {
    vi.advanceTimersByTime(5000);
  });
  expect(queryToastBody("Saved")).not.toBeInTheDocument();
});

test("the live region is present before any toast fires (always mounted, not conditionally)", () => {
  wrap(<Harness message="Saved" />);
  const live = document.querySelector('[aria-live="polite"]');
  expect(live).toBeInTheDocument();
  expect(live).toHaveTextContent("");
});

test("action button fires onClick then dismisses the toast", () => {
  vi.useFakeTimers();
  const onClick = vi.fn();
  wrap(<Harness message="Deleted" action={{ label: "Undo", onClick }} />);

  fireEvent.click(screen.getByRole("button", { name: /go/i }));
  expect(toastBody("Deleted")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Undo" }));
  expect(onClick).toHaveBeenCalledOnce();
  expect(queryToastBody("Deleted")).not.toBeInTheDocument();
});

test("the dismiss (x) button removes the toast WITHOUT firing the action", () => {
  vi.useFakeTimers();
  const onClick = vi.fn();
  wrap(<Harness message="Deleted" action={{ label: "Undo", onClick }} />);

  fireEvent.click(screen.getByRole("button", { name: /go/i }));
  fireEvent.click(screen.getByRole("button", { name: /dismiss/i }));

  expect(onClick).not.toHaveBeenCalled();
  expect(queryToastBody("Deleted")).not.toBeInTheDocument();
});

test("toast() returns a dismiss function that removes the toast early", () => {
  vi.useFakeTimers();
  let dismiss: (() => void) | undefined;

  function H() {
    const { toast } = useToast();
    return (
      <button
        onClick={() => {
          dismiss = toast({ message: "Early" });
        }}
      >
        go
      </button>
    );
  }
  wrap(<H />);

  fireEvent.click(screen.getByRole("button", { name: /go/i }));
  expect(toastBody("Early")).toBeInTheDocument();

  act(() => {
    dismiss?.();
  });
  expect(queryToastBody("Early")).not.toBeInTheDocument();
});

test("a custom durationMs overrides the 5000ms default", () => {
  vi.useFakeTimers();

  function H() {
    const { toast } = useToast();
    return <button onClick={() => toast({ message: "Quick", durationMs: 1000 })}>go</button>;
  }
  wrap(<H />);

  fireEvent.click(screen.getByRole("button", { name: /go/i }));
  act(() => {
    vi.advanceTimersByTime(999);
  });
  expect(toastBody("Quick")).toBeInTheDocument();

  act(() => {
    vi.advanceTimersByTime(1);
  });
  expect(queryToastBody("Quick")).not.toBeInTheDocument();
});

test("multiple toasts stack, and the live region tracks the newest message", () => {
  vi.useFakeTimers();

  function H() {
    const { toast } = useToast();
    return (
      <>
        <button onClick={() => toast({ message: "First" })}>first</button>
        <button onClick={() => toast({ message: "Second" })}>second</button>
      </>
    );
  }
  wrap(<H />);

  fireEvent.click(screen.getByRole("button", { name: "first" }));
  fireEvent.click(screen.getByRole("button", { name: "second" }));

  expect(toastBody("First")).toBeInTheDocument();
  expect(toastBody("Second")).toBeInTheDocument();
  const live = document.querySelector('[aria-live="polite"]');
  expect(live).toHaveTextContent("Second");
});

test("an error-variant toast is visually distinguished from the default toast", () => {
  vi.useFakeTimers();

  function H() {
    const { toast } = useToast();
    return (
      <>
        <button onClick={() => toast({ message: "Plain" })}>plain</button>
        <button onClick={() => toast({ message: "Failed", variant: "error" })}>err</button>
      </>
    );
  }
  wrap(<H />);

  fireEvent.click(screen.getByRole("button", { name: "plain" }));
  fireEvent.click(screen.getByRole("button", { name: "err" }));

  const plainToast = toastBody("Plain").closest("div");
  const errorToast = toastBody("Failed").closest("div");
  expect(errorToast?.className).toContain("destructive");
  expect(plainToast?.className).not.toContain("destructive");
});

test("useToast throws when rendered outside a ToastProvider", () => {
  function Lonely() {
    useToast();
    return null;
  }
  // Suppress the expected React error-boundary console.error noise for this
  // negative-path assertion.
  const spy = vi.spyOn(console, "error").mockImplementation(() => {});
  expect(() => render(<Lonely />)).toThrow(/useToast must be used within a ToastProvider/);
  spy.mockRestore();
});
