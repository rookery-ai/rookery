import { render, act } from "@testing-library/react";
import { ThemeProvider, useTheme } from "./theme";

function Probe() {
  const { theme, setTheme } = useTheme();
  return (
    <button data-testid="btn" onClick={() => setTheme("dark")}>
      {theme}
    </button>
  );
}

test("theme defaults to system and persists explicit choice", () => {
  localStorage.removeItem("rookery-theme");
  const { getByTestId } = render(
    <ThemeProvider>
      <Probe />
    </ThemeProvider>,
  );
  expect(getByTestId("btn").textContent).toBe("system");
  act(() => getByTestId("btn").click());
  expect(localStorage.getItem("rookery-theme")).toBe("dark");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
});
