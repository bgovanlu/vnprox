// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"errors"
	"strconv"
)

// ErrMalformed is wrapped by every decode failure caused by a response that
// does not follow BER/SNMP encoding rules (bad tag, length lying about the
// bytes available, an OID with an invalid sub-identifier encoding, ...).
// Distinguished from ErrTruncated so a caller/test can tell "the device sent
// garbage" apart from "the datagram was cut short".
var ErrMalformed = errors.New("snmp: malformed response")

// ErrTruncated is wrapped by decode failures caused by running out of bytes
// mid-structure — the honest "not enough data" case, as opposed to
// ErrMalformed's "the data present is not valid".
var ErrTruncated = errors.New("snmp: truncated response")

// ErrUnsupportedPDUType is returned by the encoder for any PDUType this
// package does not itself declare — see doc.go and noset_test.go. It is the
// single rejection path every undeclared PDU type (the write PDU type RFC
// 3416 defines included) hits.
var ErrUnsupportedPDUType = errors.New("snmp: unsupported PDU type")

// ErrRequestIDMismatch is returned when a response's request-id does not
// match the request that solicited it — a spoofed or stale UDP datagram
// arriving on the same ephemeral port, or a response to a previous timed-out
// request arriving late.
var ErrRequestIDMismatch = errors.New("snmp: response request-id does not match request")

// ErrVersionMismatch is returned when a response declares an SNMP version
// other than v2c (1) — this client only ever sends v2c requests, so a
// differently-versioned reply is not a response to anything it asked.
var ErrVersionMismatch = errors.New("snmp: response is not SNMPv2c")

// ErrAgentError is returned when a response PDU's error-status is non-zero —
// the agent (switch) understood the request but refused or failed to answer
// it (e.g. genErr, noSuchName). ErrorStatus/ErrorIndex on the returned
// *AgentError name which.
var ErrAgentError = errors.New("snmp: agent returned an error status")

// AgentError carries the error-status/error-index pair an agent's response
// PDU reported, wrapping ErrAgentError so callers can errors.Is() it while
// still recovering the detail with errors.As.
type AgentError struct {
	Status int32
	Index  int32
}

func (e *AgentError) Error() string {
	return "snmp: agent error-status=" + strconv.FormatInt(int64(e.Status), 10) +
		" error-index=" + strconv.FormatInt(int64(e.Index), 10)
}

func (e *AgentError) Unwrap() error { return ErrAgentError }
