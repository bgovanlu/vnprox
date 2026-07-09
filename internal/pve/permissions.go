package pve

import "context"

// Permissions is the decoded result of GET /access/permissions: PVE ACL
// path -> privilege name -> granted. Real PVE reports grants as 0/1 ints;
// this type stores them as bool for ergonomic use by internal/auth's
// capability derivation (docs/security.md "vnprox-enforced" authorization
// layer, internal/auth/caps.go).
//
// internal/pvemock (T-004) does not implement this endpoint: its
// fixture-defined UserSpec.Privileges is a single flat, non-path-scoped
// list (checked via session.hasPrivilege, no path awareness at all) rather
// than real PVE's per-path ACL tree, and its router has no
// "/access/permissions" route at all. Calling this method against the mock
// therefore fails with a 404 (*ErrPVERequest). This method is still
// implemented against the documented real-PVE contract — see
// internal/auth's package doc and T-105's completion report for how its
// tests work around the gap without inventing mock behavior or modifying
// internal/pvemock.
type Permissions map[string]map[string]bool

// Permissions calls GET /access/permissions: the effective, resolved ACL
// privilege set for the currently authenticated user (ticket or token
// auth), one entry per PVE ACL path the user has any grant on (e.g. "/",
// "/nodes/pve1", "/sdn"). internal/auth calls this once at login and again
// on its hourly re-derivation timer.
func (c *Client) Permissions(ctx context.Context) (Permissions, error) {
	var wire map[string]map[string]int
	if err := c.do(ctx, "GET", "/access/permissions", requestParams{}, &wire); err != nil {
		return nil, err
	}
	out := make(Permissions, len(wire))
	for path, privs := range wire {
		m := make(map[string]bool, len(privs))
		for priv, v := range privs {
			m[priv] = v != 0
		}
		out[path] = m
	}
	return out, nil
}
