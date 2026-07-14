// Inline validation for an interface rename (issue #2), mirroring the
// change engine's own rule (internal/change.ifaceNameRe / maxIfaceNameLen):
// a leading alphanumeric, then alphanumerics and `.-_` (dots appear in VLAN
// sub-interface names like "vmbr0.100"), at most 15 characters (IFNAMSIZ-1).
const IFACE_NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
export const IFACE_NAME_MAX = 15;

/** Returns a hard-error string for an invalid new interface name, or
 * undefined when it's acceptable. `current` (the existing name) makes an
 * unchanged name an error so the dialog can't draft a no-op rename. */
export function ifaceNameError(name: string, current?: string): string | undefined {
  const n = name.trim();
  if (n === "") return "Enter a new name.";
  if (current !== undefined && n === current) return "That's already the current name.";
  if (n.length > IFACE_NAME_MAX) return `Interface names can be at most ${String(IFACE_NAME_MAX)} characters.`;
  if (!IFACE_NAME_RE.test(n)) return "Use letters, digits, and .-_ only, starting with a letter or digit — no spaces or slashes.";
  return undefined;
}
