// SPDX-License-Identifier: Apache-2.0

import { Moon, Sun } from "lucide-react";
import { useThemeStore } from "../store/theme";
import { Button } from "../components/Button";
import { Tooltip } from "../components/Tooltip";

// T-3403: swapped the ☀/☽ text glyphs for lucide icons to match the rest of
// the top bar's icon-button group; the label logic (and the aria-label,
// still the accessible name) is unchanged.
export function ThemeToggle() {
  const theme = useThemeStore((s) => s.theme);
  const toggleTheme = useThemeStore((s) => s.toggleTheme);
  const label = theme === "dark" ? "Switch to light theme" : "Switch to dark theme";

  return (
    <Tooltip content={label}>
      <Button variant="ghost" size="sm" onClick={toggleTheme} aria-label={label}>
        {theme === "dark" ? <Sun aria-hidden className="h-4 w-4" /> : <Moon aria-hidden className="h-4 w-4" />}
      </Button>
    </Tooltip>
  );
}
