// Theme store: dark by default (per docs/user-guide.md's ergonomics
// expectation for a NOC-style dashboard), persisted across reloads via
// zustand's `persist` middleware (localStorage).
import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Theme = "dark" | "light";

interface ThemeState {
  theme: Theme;
  toggleTheme: () => void;
  setTheme: (theme: Theme) => void;
}

export const useThemeStore = create<ThemeState>()(
  persist(
    (set) => ({
      theme: "dark",
      toggleTheme: () => {
        set((state) => ({ theme: state.theme === "dark" ? "light" : "dark" }));
      },
      setTheme: (theme) => {
        set({ theme });
      },
    }),
    { name: "vnprox.theme" },
  ),
);

/** Keeps `<html class="dark">` (Tailwind's class-based dark variant, see
 * src/index.css) in sync with the store. Called once from App so every
 * component can just use Tailwind's `dark:` variant. */
export function applyThemeClass(theme: Theme): void {
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.style.colorScheme = theme;
}
