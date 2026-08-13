// Package demo implements T-2801's built-in demo mode: `vnproxd --demo`
// runs the whole product against an embedded synthetic cluster, with no
// Proxmox VE endpoint and no network access.
//
// # What "no network access" means here, structurally
//
// A demo daemon still binds its own HTTPS listener — it is a web UI, there
// is nothing to demo otherwise. What it must never do is reach *out*: to a
// PVE API, to a cluster peer, to anything. That is enforced by
// construction rather than by configuration:
//
//   - The PVE client's transport is [Mode.Transport], an http.RoundTripper
//     that dispatches straight into an in-process *pvemock.Server. It
//     holds no net.Dialer and opens no socket; a request for any host at
//     all is answered by the fixture or refused. There is no code path
//     from a demo PVE client to a TCP connection, so no configuration
//     mistake can produce one.
//   - The host reader is [Mode.HostReader], the same fixture's
//     host.FixtureReader. A demo daemon does not read the demo user's own
//     netlink, conntrack table, or lldpd.
//   - [APIURL] is a deliberately unresolvable name. Nothing dials it; it
//     exists because internal/pve requires a syntactically valid base URL
//     to build request paths against, and a real-looking address in that
//     field would be an invitation to assume it is reachable.
//
// # The demo dataset
//
// dataset/cluster.yaml and dataset/flows.yaml are checked-in fixtures,
// embedded here. They are versioned, reviewable and usable as a test
// corpus — the card's requirement, and the reason there is no runtime
// generator. See each file's own header for what it contains and why.
//
// # What this package does NOT do
//
// It does not implement the write-refusal itself. "Every mutating API
// returns a would-have result and touches nothing" is enforced in
// internal/api's demo middleware, in front of every route, because that is
// the only place with a complete view of the mutating surface. This
// package supplies the world; internal/api supplies the read-only-ness.
package demo
