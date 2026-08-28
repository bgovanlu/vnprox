// SPDX-License-Identifier: Apache-2.0

import clsx from "clsx";
import { useHelpStore } from "./store";
import { getHelpTopic } from "./registry";

export interface HelpAnchorProps {
  /** A registered topic id. Every anchor in the tree is checked against the
   * registry by coverage.test.ts, so a typo here fails the suite rather
   * than shipping a `?` that opens nothing. */
  topic: string;
  className?: string;
}

/** The inline "?" affordance a feature module puts next to a panel
 * heading. Opens the help drawer directly at that panel's topic.
 *
 * Deliberately not a Tooltip: a tooltip can hold a sentence, and the
 * surfaces this marks (the path simulator's verdicts, microsegmentation's
 * four buckets, what unattended rollback actually covers) need paragraphs
 * and cross-links. */
export function HelpAnchor({ topic, className }: HelpAnchorProps) {
  const openHelp = useHelpStore((s) => s.openHelp);
  const registered = getHelpTopic(topic);
  const label = registered ? `Help: ${registered.title}` : "Help";

  return (
    <button
      type="button"
      onClick={() => {
        openHelp(topic);
      }}
      aria-label={label}
      title={label}
      className={clsx(
        "inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full border text-[0.7rem] font-semibold leading-none",
        "border-border-strong text-fg-subtle hover:border-outline hover:text-fg",
        "dark:border-slate-600 dark:text-slate-400 dark:hover:border-slate-400 dark:hover:text-slate-100",
        "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2",
        className,
      )}
    >
      ?
    </button>
  );
}
