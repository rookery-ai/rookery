import { render, screen } from "@testing-library/react";

test("renders the app root", async () => {
  window.history.pushState({}, "", "/login");
  const { default: App } = await import("./App");
  render(<App />);
  expect(await screen.findByText(/log in/i)).toBeInTheDocument();
});
