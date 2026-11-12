export type Theme = "light" | "dark" | "system";

const KEY = "camhive.theme";

export function readTheme(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch {
    /* storage may be unavailable */
  }
  return "system";
}

export function applyTheme(t: Theme, matchDark = () => window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false): void {
  const dark = t === "dark" || (t === "system" && matchDark());
  document.documentElement.classList.toggle("dark", dark);
  try {
    localStorage.setItem(KEY, t);
  } catch {
    /* ignore */
  }
}
