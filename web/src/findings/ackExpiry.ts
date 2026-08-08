// T-2402: the one place that turns "how long is this deliberate for" into the
// unix-seconds instant the server stores.
//
// Extracted from AckDialog so the dialog module exports only a component (the
// react-refresh boundary rule), and so the arithmetic can be pinned by a test
// rather than restated in one.

/** Expiry choices offered by the acknowledge dialog, in days. 0 means "until
 * explicitly un-acknowledged". */
export const EXPIRY_CHOICES: { label: string; days: number }[] = [
  { label: "7 days", days: 7 },
  { label: "30 days", days: 30 },
  { label: "90 days", days: 90 },
  { label: "No expiry", days: 0 },
];

export const DEFAULT_EXPIRY_DAYS = 30;

/** Converts a day count into unix seconds, or undefined for "no expiry".
 *
 * Every positive choice yields a FUTURE instant, which is what makes the
 * server's already-in-the-past refusal unreachable from this UI by
 * construction rather than by the client duplicating that check. */
export function expiryFromDays(days: number, now: Date): number | undefined {
  if (days <= 0) return undefined;
  return Math.floor(now.getTime() / 1000) + days * 24 * 60 * 60;
}
