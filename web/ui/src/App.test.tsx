import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders the app root", async () => {
  // Set the URL to /app/login for the router
  Object.defineProperty(window, "location", {
    value: new URL("http://localhost/app/login"),
    writable: true,
  });
  render(<App />);
  expect(await screen.findByText(/login/i)).toBeInTheDocument();
});
