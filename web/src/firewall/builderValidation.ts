// Pure client-side validation for the rule builder row (docs/features/
// firewall.md §2: "builder row: direction, action, source/dest ..., proto/
// ports, interface, macro picker ..., log level, comment"). This mirrors
// (a client-side-fast-feedback subset of) internal/change's schema
// validators (validate_schema.go's schemaFwDirection/schemaFwAction/
// schemaFwMacro) so obviously-invalid input never round-trips to the
// server just to bounce back — the server validators remain the source of
// truth; this is UX, not a security boundary.
export interface RuleBuilderFormValues {
  direction: string;
  action: string;
  proto: string;
  macro: string;
  dport: string;
}

// "forward" (T-3103) is hardware-captured at cluster/node/vnet scope
// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt); the server's own
// schemaFwDirectionForTarget additionally rejects it at guest scope
// specifically — this client-side check stays scope-blind like the rest of
// this file (UX only, not a security boundary, per this file's own doc
// comment), so a guest-scope "forward" rule still round-trips to the server
// to get that specific rejection with its explanation.
const VALID_DIRECTIONS = new Set(["in", "out", "forward", "group"]);
const VALID_ACTIONS = new Set(["ACCEPT", "DROP", "REJECT"]);
const VALID_PROTOS = new Set(["", "tcp", "udp", "icmp"]);

/**
 * Validates one builder-row draft, returning a list of human-readable
 * problems (empty = ready to stage). `knownMacros` is the macro catalog
 * names (GET /firewall/objects' `macros`), so an unknown macro name is
 * caught before it round-trips.
 */
export function validateRuleBuilder(form: RuleBuilderFormValues, knownMacros: ReadonlySet<string>): string[] {
  const errors: string[] = [];

  if (!VALID_DIRECTIONS.has(form.direction)) {
    errors.push("Direction must be in, out, forward, or a security-group reference.");
  }

  if (form.direction === "group") {
    if (!form.action) {
      errors.push("Choose a security group to reference.");
    }
  } else if (!VALID_ACTIONS.has(form.action)) {
    errors.push("Action must be ACCEPT, DROP, or REJECT.");
  }

  if (form.macro && !knownMacros.has(form.macro)) {
    errors.push(`"${form.macro}" is not a known macro.`);
  }
  if (form.macro && (form.proto || form.dport)) {
    errors.push("A macro already implies its own proto/ports — clear proto/port or remove the macro.");
  }
  if (form.proto && !VALID_PROTOS.has(form.proto)) {
    errors.push("Proto must be tcp, udp, or icmp.");
  }

  return errors;
}
