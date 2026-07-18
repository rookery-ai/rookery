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
  localStorage.removeItem("sa-theme");
  const { getByTestId } = render(
    <ThemeProvider>
      <Probe />
    </ThemeProvider>,
  );
  expect(getByTestId("btn").textContent).toBe("system");
  act(() => getByTestId("btn").click());
  expect(localStorage.getItem("sa-theme")).toBe("dark");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
});
