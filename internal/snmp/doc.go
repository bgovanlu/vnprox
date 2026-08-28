// SPDX-License-Identifier: Apache-2.0

// Package snmp is a minimal, read-only SNMPv2c client, hand-rolled over the
// standard library rather than a third-party dependency — T-4013's gating
// decision, recorded in full in planning/tasks/T-4013-snmp-dependency-decision.md
// (kept even though the decision landed "stdlib is feasible", per that
// document's own instruction, since it also has to justify the choice it
// didn't make).
//
// # Scope: exactly what T-4013 needs, nothing a general SNMP library would add
//
// This is not "a small SNMP library". It implements precisely three PDU
// types this codebase ever needs to send — GetRequest, GetNextRequest,
// GetBulkRequest — and decodes exactly the response shape a GetResponse PDU
// takes. RFC 3416's write PDU type is not implemented anywhere in this
// package: not a constant naming its wire tag, not an encoder function, not
// a code path that could construct one. That is a structural property, not
// a policy one — see noset_test.go, which asserts it two ways: exhaustively
// (every one of the 256 possible PDU-type byte values is fed to the
// encoder, and every value this package does not itself declare must be
// rejected) and by source scan (no non-test file in this package may
// contain that PDU's name or its wire-tag literal — noset_test.go's own
// doc comment names the exact grep a reviewer can run by hand; this file
// avoids the literal tokens on purpose, so it doesn't trip its own scan).
// There is also no SNMPv3 (no USM, no auth/priv, no engine discovery) and no
// MIB compiler/textual-convention resolution — callers name OIDs as dotted
// strings or numeric components directly; internal/ifcounters is the only
// caller, and it names exactly the IF-MIB OIDs T-4013's card lists.
//
// # Untrusted input
//
// A GetResponse PDU crosses the trust boundary from a network device vnprox
// does not control (CLAUDE.md's "parse defensively" instruction for this
// card). Every decode path here is bounded and panic-safe:
//   - the UDP datagram itself is capped at maxMessageSize bytes;
//   - every BER length field is validated against the bytes actually
//     remaining in the buffer before use — a length that claims more data
//     than the message contains is a decode error, never a slice that reads
//     past the end;
//   - a length field wider than 4 octets, or an OID with more than
//     maxOIDComponents sub-identifiers, or a sub-identifier requiring more
//     continuation bytes than a valid encoding ever needs, is rejected
//     rather than accepted with an unbounded accumulator;
//   - Client.Get/GetBulk recover from any panic in the decode path and
//     return it as an error (belt-and-braces alongside the bounds checks
//     above — see client.go's decodeResponse).
//
// # Credentials
//
// The v2c community string is a bearer credential (anyone who has it can
// query — read-only here, but a real device may also accept it for writes
// over its own management plane, which is exactly why this package never
// logs it; see internal/ifcounters and internal/store for how it is stored
// encrypted at rest and internal/redact for how it is scrubbed from
// anything unstructured that might otherwise carry it).
package snmp
