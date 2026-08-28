// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// trigger.go is T-4101's pure half of "arm a bounded capture from an
// anomaly": deriving the pcap/BPF filter that scopes a capture to exactly
// what an Anomaly is about. This package stays a pure statistics library —
// it does not import internal/capture and never starts anything — the
// composition root (cmd/vnproxd) is what turns an Anomaly plus this filter
// into a capture.StartRequest and calls capture.Coordinator.Start, matching
// how internal/findings/adapt_baseline.go already adapts an Anomaly into a
// Finding without capture ever entering this package's dependency graph.

// CaptureFilter derives the pcap/BPF filter (internal/capture.ValidateFilter
// syntax) that narrows a capture to the traffic this Anomaly is actually
// about, so an anomaly-armed capture (T-4101) records the flow that
// triggered it rather than everything crossing the target. An empty return
// means no narrower filter is derivable for this class/subject — the caller
// captures everything on the scoped target instead (still bounded by
// capture's own server-enforced caps, unchanged either way).
//
//   - new_port: Subject is "proto/port" (PortKey.String, e.g. "tcp/6667").
//     The protocol qualifier is deliberately dropped — flow's proto naming
//     doesn't line up 1:1 with pcap's keyword set (e.g. an unnamed IP
//     protocol renders as a bare number, which capture.ValidateFilter would
//     reject as an unrecognized keyword) — so the filter is "port <port>",
//     valid for every case and still exactly the port that was new.
//   - new_subnet: Subject is a CIDR (e.g. "10.9.0.0/24"); the filter is
//     "net <cidr>".
//   - volume_spike: Subject is a hour bucket, not a filterable primitive —
//     returns "".
func (a Anomaly) CaptureFilter() string {
	switch a.Class {
	case ClassNewPort:
		if port, ok := parsePortSubject(a.Subject); ok {
			return fmt.Sprintf("port %d", port)
		}
		return ""
	case ClassNewSubnet:
		if isCIDRSubject(a.Subject) {
			return "net " + a.Subject
		}
		return ""
	default:
		return ""
	}
}

// parsePortSubject extracts the port number from a "proto/port" subject
// string (PortKey.String's format). ok is false for anything that doesn't
// parse as exactly that shape.
func parsePortSubject(subject string) (port int, ok bool) {
	_, portStr, found := strings.Cut(subject, "/")
	if !found {
		return 0, false
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 {
		return 0, false
	}
	return p, true
}

// isCIDRSubject reports whether subject parses as a CIDR — new_subnet's
// Subject always is (peerSubnet only ever produces one), but this stays
// defensive rather than assuming the shape.
func isCIDRSubject(subject string) bool {
	_, _, err := net.ParseCIDR(subject)
	return err == nil
}
