import { render, screen, fireEvent } from "@testing-library/react";
import { Composer } from "./Composer";

// While a turn runs, Send becomes Stop: the box stays disabled (nothing to type
// into a turn in flight) and the one live control is the way out of it.
test("busy with onStop shows an enabled Stop button in place of Send", () => {
  const onStop = vi.fn();
  render(<Composer onSend={vi.fn()} busy onStop={onStop} />);
  expect(screen.getByRole("textbox")).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
  const stop = screen.getByRole("button", { name: "Stop" });
  expect(stop).toBeEnabled();
  fireEvent.click(stop);
  expect(onStop).toHaveBeenCalledTimes(1);
});

// The designers pass no onStop and must keep the disabled Send they have today.
test("busy without onStop keeps a disabled Send", () => {
  render(<Composer onSend={vi.fn()} busy />);
  expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
});

test("idle with onStop still shows Send", () => {
  render(<Composer onSend={vi.fn()} onStop={vi.fn()} />);
  expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
});
