// SPDX-License-Identifier: Apache-2.0

// Package lacphash implements the Linux bonding driver's xmit_hash_policy
// algorithms as pure functions: given a flow's tuple and a bond's ordered,
// eligible slave list, which slave index the kernel's transmit path would
// select. T-4110 (LACP hash visualizer) needs this to predict which bond
// member a given flow hashes to.
//
// # Hardware-flagged (CLAUDE.md)
//
// T-4110 is explicitly hardware-flagged, under CLAUDE.md's "needs real NICs
// or a physical switch" category: which slave a flow actually lands on
// depends on the *local* kernel's hash (this package's business) landing on
// a slave the *switch's own, independent* LACP hash also assigns to the
// same aggregate member — a property of two black boxes agreeing, that no
// amount of software alone can prove. This package computes exactly and
// only the local, software-observable half: the kernel's own transmit-hash
// decision. Whether a real switch's hash agrees, and whether the predicted
// distribution matches an aggregate under real multi-flow traffic on real
// NICs, is unverified and is the subject of this task's
// planning/reports/needs-hardware-validation.md entry.
//
// # Source of the algorithms
//
// Documentation/networking/bonding.rst, the "xmit_hash_policy" bond option
// (upstream Linux kernel documentation for the bonding driver — the same
// document internal/change/validate_advisory.go's layer3+4-on-802.3ad
// advisory already cites). This package was written from that document's
// stated formulas, not from reading bond_main.c's C source directly (no
// kernel source tree is available in this environment) and not from
// observing a real 802.3ad aggregate under traffic (no hardware is
// available). Two things follow from that, stated plainly rather than
// silently:
//
//   - The formulas below are believed correct to the documented algorithm's
//     *description*, not verified bit-for-bit against a specific kernel
//     version's actual integer arithmetic (the shift/fold order and use of
//     reciprocal-scale hashing has evolved across kernel versions
//     historically). This package's own table-driven tests check that its
//     arithmetic is internally consistent and deterministic — they cannot,
//     and do not claim to, prove this matches any one running kernel's
//     bond_main.c. That gap is exactly what the needs-hardware-validation
//     entry names.
//   - layer2 and layer2+3 require the flow's Ethernet source/destination
//     MAC addresses. internal/flow.Record (T-1002's sFlow/NetFlow/IPFIX/
//     conntrack pipeline) never carries MAC addresses — sflow.go's raw
//     packet-header decoder explicitly skips over both MAC fields without
//     storing them, and NetFlow/IPFIX never carry L2 at all. So Predict
//     (predict.go), which is driven by internal/flow.Record data, can
//     compute layer3+4/encap3+4 predictions from real flow data but can
//     never compute layer2/layer2+3 predictions from it. That is reported
//     back to the caller explicitly (Prediction.UnclassifiedReason), never
//     silently defaulted to a guessed MAC or dropped without explanation.
package lacphash
