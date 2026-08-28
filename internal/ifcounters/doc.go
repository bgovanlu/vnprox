// SPDX-License-Identifier: Apache-2.0

// Package ifcounters implements T-4013's read-only SNMP switch-counter
// poller: port errors, discards, and utilization from LLDP-discovered
// switches, painted on map edges. Read-only end to end.
//
// # Never a second discovery mechanism
//
// Service.Tick's only source of "which switches exist" is
// internal/topology.Service.LLDPNeighbors() — the same current-state LLDP
// neighbor set the Ports table (internal/topology/ports.go) and the map
// itself already render from. There is no separate switch-discovery scan:
// a switch this daemon has never seen an LLDP neighbor relationship with
// can never become an SNMP poll target, no matter what is configured in
// switch_snmp_targets (internal/store/switch_snmp_targets.go) — Service.Tick
// only ever dials a target whose ChassisID also appears in the current
// LLDPNeighbors() set this tick.
//
// # Never touches internal/switchdrv
//
// This package (and internal/snmp beneath it) does not import
// internal/switchdrv and calls none of its methods, SetPortConfig included
// — see noswitchdrv_test.go for the source-scan assertion, the same shape
// as internal/snmp's noset_test.go. The read path here and switchdrv's
// guarded-push path share no call edge, which is T-4013's card's second
// acceptance criterion.
//
// # Honest states
//
// Every neighbor-derived edge Tick produces a Result for lands in exactly
// one of four states (see State): StateNotConfigured (no operator opt-in
// for this switch's chassis — the common default, since opt-in is
// per-switch and off by default), StateUnreachable (opted in, but the SNMP
// poll itself failed — timeout, refused, wrong community), StateNoCounters
// (the switch answered, but this specific port's counters could not be
// correlated or came back as one of RFC 3416's exception values), and
// StateOK (real counters). These are never collapsed into a single
// "no data" signal — CLAUDE.md's instruction for this card, and
// docs/features/monitoring.md's general "distinguish absence of a check
// from a failed check" convention (see e.g. mtuprobe's own "no verified
// badge, not a stale/zero value" precedent, though that package only has
// two states where this one needs three).
//
// # Credentials
//
// A per-switch v2c community string is a credential (internal/snmp's own
// doc comment). It is stored encrypted at rest exactly like every other
// sealed credential in this codebase — switch_snmp_targets.community_enc,
// AES-256-GCM via internal/store's SessionCipher, the identical primitive
// switches.credentials_enc already uses (internal/store/switches.go) — and
// is registered in internal/backup/secrets.go's secret-class inventory so
// TestSecretClasses_CoverEverySealedColumn enforces that registration
// mechanically rather than by review discipline alone. It is never returned
// by the GET /snmp/targets API response, never appears in a log line
// (internal/snmp.Client holds it only as raw bytes passed directly to the
// UDP wire encoder — see that package's TestClient_CommunityNeverAppearsInPublicError),
// and "community" is in internal/redact's secret-term vocabulary as
// belt-and-braces for the unstructured paths (a Scrub'd error message, a
// support-bundle field) that vocabulary already covers for every other
// credential class.
package ifcounters
