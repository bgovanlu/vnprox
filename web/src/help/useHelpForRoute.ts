// SPDX-License-Identifier: Apache-2.0

import { useCallback } from "react";
import { useLocation } from "react-router-dom";
import { useHelpStore } from "./store";
import { helpTopicForPath } from "./routeTopics";

/** Returns a callback that opens the help drawer on whatever screen the
 * user is currently looking at. Used by the top bar's Help button and by
 * the `F1` binding — both of which mean "help with *this*", not "open the
 * manual somewhere". */
export function useHelpForRoute(): () => void {
  const { pathname } = useLocation();
  const openHelp = useHelpStore((s) => s.openHelp);
  return useCallback(() => {
    openHelp(helpTopicForPath(pathname));
  }, [openHelp, pathname]);
}
