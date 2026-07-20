import { render, screen } from "@testing-library/react";
import { ContextPaneHeader, ContextSection } from "./ContextPaneParts";

test("header renders title and action slot", () => {
  render(<ContextPaneHeader title="Home" action={<button>+</button>} />);
  expect(screen.getByRole("heading", { name: "Home" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "+" })).toBeInTheDocument();
});

test("section renders an uppercase-styled heading", () => {
  render(
    <ContextSection title="Reminders">
      <p>x</p>
    </ContextSection>,
  );
  expect(screen.getByRole("heading", { name: "Reminders" })).toBeInTheDocument();
});

test("header renders without an action", () => {
  render(<ContextPaneHeader title="Knowledge Base" />);
  expect(screen.getByRole("heading", { name: "Knowledge Base" })).toBeInTheDocument();
});

test("section renders an optional action alongside the heading", () => {
  render(
    <ContextSection title="Inbox" action={<button>Mark all read</button>}>
      <p>x</p>
    </ContextSection>,
  );
  expect(screen.getByRole("heading", { name: "Inbox" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Mark all read" })).toBeInTheDocument();
});

test("section renders children", () => {
  render(
    <ContextSection title="Reminders">
      <p>child content</p>
    </ContextSection>,
  );
  expect(screen.getByText("child content")).toBeInTheDocument();
});
