import { render, screen } from "@testing-library/react";
import { Linkify } from "./linkify";

test("scheme-prefixed URL is linkified with target=_blank + rel=noreferrer", () => {
  render(<Linkify text="Create a Slack app at https://api.slack.com/apps to begin" />);
  const link = screen.getByRole("link", { name: "https://api.slack.com/apps" });
  expect(link).toHaveAttribute("href", "https://api.slack.com/apps");
  expect(link).toHaveAttribute("target", "_blank");
  expect(link.getAttribute("rel")).toContain("noreferrer");
});

test("bare domain with a path is linkified with an https:// prefixed href", () => {
  render(<Linkify text="Go to api.slack.com/apps and create an app" />);
  const link = screen.getByRole("link", { name: "api.slack.com/apps" });
  expect(link).toHaveAttribute("href", "https://api.slack.com/apps");
});

test("bare domain with a deep path (console.cloud.google.com/...) is linkified", () => {
  render(<Linkify text="Open console.cloud.google.com/apis/credentials to make a client" />);
  const link = screen.getByRole("link", { name: "console.cloud.google.com/apis/credentials" });
  expect(link).toHaveAttribute("href", "https://console.cloud.google.com/apis/credentials");
});

test("bare domain with no path is left as plain text", () => {
  render(<Linkify text="Visit google.com for more" />);
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(screen.getByText(/Visit google.com for more/)).toBeInTheDocument();
});

test("trailing parenthesis is trimmed off the link and left as plain text", () => {
  render(<Linkify text="see (https://api.slack.com/apps) for details" />);
  const link = screen.getByRole("link", { name: "https://api.slack.com/apps" });
  expect(link).toHaveAttribute("href", "https://api.slack.com/apps");
  expect(screen.getByText(/for details/)).toBeInTheDocument();
});

test("trailing sentence-dot is trimmed off a bare-domain link", () => {
  render(<Linkify text="Open console.cloud.google.com/apis/credentials." />);
  const link = screen.getByRole("link", { name: "console.cloud.google.com/apis/credentials" });
  expect(link).toHaveAttribute("href", "https://console.cloud.google.com/apis/credentials");
});

test("javascript: is never a link — the pattern can't match a non-domain scheme", () => {
  render(<Linkify text="run javascript:alert(1) here" />);
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  const links = screen.queryAllByRole("link");
  for (const l of links) {
    expect(l.getAttribute("href")).toMatch(/^https?:\/\//);
  }
});
