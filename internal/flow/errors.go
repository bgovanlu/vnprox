package flow

import "errors"

// Sentinel errors, per docs/development.md's Go standards ("sentinel errors
// in each package's errors.go").
var (
	// ErrMalformed is returned (wrapped with context) by a decoder when a
	// datagram's header or a structure it contains is truncated/invalid
	// beyond what defensive parsing can recover from — the listener counts
	// this and moves on, per this package's doc comment; it never panics or
	// blocks on it.
	ErrMalformed = errors.New("flow: malformed or truncated datagram")

	// ErrUnknownTemplate is returned when a NetFlow v9/IPFIX data set
	// arrives for a (exporter, sourceID/domainID, setID) this decoder has
	// not yet cached a template for — real collectors drop such records
	// (they cannot be decoded without the field layout the template
	// declares) rather than erroring the whole datagram; the caller counts
	// this as a defensive-skip, not a hard failure.
	ErrUnknownTemplate = errors.New("flow: data set references an unknown template")

	// ErrUnsupportedVersion is returned when a datagram's version field
	// does not match the decoder it was routed to.
	ErrUnsupportedVersion = errors.New("flow: unsupported protocol version")
)
