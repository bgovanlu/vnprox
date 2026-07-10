package pve

import "context"

// Permissions is the decoded result of GET /access/permissions: PVE ACL
// path -> privilege name -> granted. Real PVE reports grants as 0/1 ints;
// this type stores them as bool for ergonomic use by internal/auth's
// capability derivation (docs/security.md "vnprox-enforced" authorization
// layer, internal/auth/caps.go).
//
// internal/pvemock implements this endpoint by reporting the fixture
// user's flat privilege list at the root path "/" (including a literal
// "*" wildcard where a fixture uses one). Real PVE returns a per-path ACL
// tree and enumerates concrete privilege names rather than a wildcard, so
// while this method is written and tested against the documented
// contract, the exact real-PVE response shape (path granularity, wildcard
// handling) still needs hardware validation.
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
