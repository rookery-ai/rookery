import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders the app root", () => {
  render(<App />);
  expect(screen.getByText(/simple agents/i)).toBeInTheDocument();
});
