package main

// doctorlive.go wires T-2406's daemon-side self-check: the checks
// `vnproxctl doctor` cannot answer on its own, because they need a credential
// only the running daemon holds.
//
// T-1904 shipped four such checks implemented and tested, all reporting `skip`
// from the CLI. TWO of them are closed here, and the other two are not — with
// the reason recorded rather than papered over, because T-1904's own rule was
// that `skip` is not `pass` and a report that blurs the two is a decoration.
//
//	pve_reachable   CLOSED — the daemon's authenticated client answers it.
//	pve_privileges  CLOSED — same client, via GET /access/permissions.
//	clock_skew      still skips: it needs PVE's own clock, and neither the
//	                current internal/pve client nor internal/pvemock exposes a
//	                server-time surface. Adding one means a transport change
//	                (reading the HTTP Date header) or a new endpoint plus mock
//	                fixtures. Filed as T-2406-followup-01.
//	peer_secret     still skips: it compares the cluster secret ACROSS nodes,
//	                and no peer-API route reports another node's digest. A
//	                probe returning only the LOCAL digest would be WORSE than
//	                skipping — checkPeerSecret reads a one-entry map as
//	                "single-node cluster; nothing to agree with" and would
//	                report PASS on a five-node cluster whose secrets disagree.
//	                It is also unverifiable against anything but a mock on the
//	                single node this project has (roadmap-proven.md's D2).
//	                Filed as T-2406-followup-02.
//
// So `--live` takes a single-node install from 6 of 10 checks answered to 8.

import (
	"context"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/doctor"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// doctorPVEProbe answers doctor's PVEProbe from the daemon's own authenticated
// PVE client — the client whose absence is the entire reason these checks
// skipped from the CLI.
type doctorPVEProbe struct {
	client *pve.Client
}

// Ping reports reachability by making a real authenticated call.
//
// GET /access/permissions is used rather than an unauthenticated liveness
// endpoint on purpose: checkPVEReachable's pass message is "PVE API reachable
// AND the token authenticates", and only an authenticated call establishes the
// second half. A route that answered without the token would let a expired
// credential read as reachable.
//
// The returned server time is deliberately ZERO. checkClockSkew treats a zero
// time as "PVE did not report a server time" and skips — which is accurate,
// and far better than inventing a reference clock. See this file's header.
func (p doctorPVEProbe) Ping(ctx context.Context) (time.Time, error) {
	if p.client == nil {
		return time.Time{}, fmt.Errorf("vnproxd: no PVE client configured")
	}
	if _, err := p.client.Permissions(ctx); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, nil
}

// Privileges returns the flat set of privilege names the configured token
// holds anywhere in the ACL tree.
//
// Flattened across paths deliberately: doctor asks "does this token hold
// Sys.Audit at all", not "on which path". A per-path answer would be more
// precise and much harder to act on, and the remediation doctor prints
// (`pveum acl modify / ...`) grants at the root anyway.
func (p doctorPVEProbe) Privileges(ctx context.Context) ([]string, error) {
	if p.client == nil {
		return nil, fmt.Errorf("vnproxd: no PVE client configured")
	}
	perms, err := p.client.Permissions(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, 16)
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, privs := range perms {
		for name, granted := range privs {
			if !granted {
				continue
			}
			// PVE reports a literal "*" for a full grant on a path (and
			// internal/pvemock reproduces that). Expanding it to every
			// privilege vnprox uses is correct — a holder of "*" holds them
			// all — and taking the list from internal/auth rather than
			// restating it is what stops the two drifting.
			if name == "*" {
				for _, rp := range auth.RequiredPrivileges() {
					add(rp.Name)
				}
				continue
			}
			add(name)
		}
	}
	return out, nil
}

// doctorLiveRunner is the api.DoctorLiveService implementation.
type doctorLiveRunner struct {
	env   doctor.Env
	facts doctor.Facts
}

func (r doctorLiveRunner) RunLive(ctx context.Context) []doctor.Result {
	return doctor.RunLive(ctx, r.facts, r.env)
}

// newDoctorLiveRunner builds the runner from the daemon's PVE client and the
// few facts the live checks read (the API URL and the token path, both only
// for building an actionable remediation string).
//
// Env.Peers is deliberately nil — see this file's header for why a local-only
// digest probe would be worse than a skip.
func newDoctorLiveRunner(pveAPIURL, pveTokenFile string, client *pve.Client) doctorLiveRunner {
	return doctorLiveRunner{
		facts: doctor.Facts{PVEAPIURL: pveAPIURL, PVETokenFile: pveTokenFile},
		env:   doctor.Env{PVE: doctorPVEProbe{client: client}},
	}
}
