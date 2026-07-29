import { createContext, useContext, useEffect, useState } from "react";

type Theme = "light" | "dark" | "system";
const KEY = "rookery-theme";

const Ctx = createContext<{ theme: Theme; setTheme: (t: Theme) => void }>({
  theme: "system",
  setTheme: () => {},
});

function apply(theme: Theme) {
  // classList.toggle's second arg must be a real boolean — passing `undefined`
  // (e.g. when window.matchMedia doesn't exist, as in jsdom test envs) makes
  // it flip current state instead of force-setting it. Coerce explicitly.
  const dark = Boolean(
    theme === "dark" ||
      (theme === "system" &&
        typeof window !== "undefined" &&
        window.matchMedia &&
        window.matchMedia("(prefers-color-scheme: dark)").matches),
  );
  document.documentElement.classList.toggle("dark", dark);
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(
    () => (localStorage.getItem(KEY) as Theme) ?? "system",
  );

  useEffect(() => {
    apply(theme);
    if (typeof window !== "undefined" && window.matchMedia) {
      const mq = window.matchMedia("(prefers-color-scheme: dark)");
      const onChange = () => theme === "system" && apply("system");
      mq.addEventListener("change", onChange);
      return () => mq.removeEventListener("change", onChange);
    }
  }, [theme]);

  const setTheme = (t: Theme) => {
    localStorage.setItem(KEY, t);
    setThemeState(t);
  };

  return <Ctx.Provider value={{ theme, setTheme }}>{children}</Ctx.Provider>;
}

export const useTheme = () => useContext(Ctx);
