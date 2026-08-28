// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"fmt"
	"net"
)

// ValueKind identifies which SNMP SMI type a Value holds.
type ValueKind int

const (
	KindNull ValueKind = iota
	KindInteger
	KindOctetString
	KindObjectIdentifier
	KindIPAddress
	KindCounter32
	KindGauge32 // also used for plain Unsigned32
	KindTimeTicks
	KindOpaque
	KindCounter64
	// NoSuchObject/NoSuchInstance/EndOfMibView are RFC 3416 §4.2.1's
	// exception values: a GetResponse varbind's value slot can hold one of
	// these instead of real data, meaning respectively "this OID is not
	// implemented at all", "this exact instance doesn't exist" (GetBulk/
	// GetNext walked past a table row), and "there is nothing lexically
	// after this OID" (walk termination). internal/ifcounters treats all
	// three as "this port has no counters", per this card's honest-states
	// requirement — see that package's classification.
	KindNoSuchObject
	KindNoSuchInstance
	KindEndOfMibView
)

// Value is a decoded varbind value. Exactly one of the typed fields is
// meaningful, selected by Kind. Fields ordered densest-pointer-first
// (docs/development.md's Go standards): the three slice-backed fields, then
// the pointer-free scalars.
type Value struct {
	IP   net.IP
	Str  []byte
	OID  OID
	Kind ValueKind
	UInt uint64
	Int  int64
}

// IsException reports whether v is one of RFC 3416's three exception values
// rather than real data.
func (v Value) IsException() bool {
	switch v.Kind {
	case KindNoSuchObject, KindNoSuchInstance, KindEndOfMibView:
		return true
	default:
		return false
	}
}

func decodeValue(tag byte, content []byte) (Value, error) {
	switch tag {
	case tagInteger:
		i, err := decodeSignedInt(content)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindInteger, Int: i}, nil
	case tagOctetString:
		return Value{Kind: KindOctetString, Str: append([]byte(nil), content...)}, nil
	case tagNull:
		return Value{Kind: KindNull}, nil
	case tagObjectID:
		oid, err := decodeOID(content)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindObjectIdentifier, OID: oid}, nil
	case tagIPAddress:
		if len(content) != 4 {
			return Value{}, fmt.Errorf("%w: IpAddress is %d octets, want 4", ErrMalformed, len(content))
		}
		ip := make(net.IP, 4)
		copy(ip, content)
		return Value{Kind: KindIPAddress, IP: ip}, nil
	case tagCounter32:
		u, err := decodeUnsignedInt(content)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindCounter32, UInt: u}, nil
	case tagGauge32:
		u, err := decodeUnsignedInt(content)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindGauge32, UInt: u}, nil
	case tagTimeTicks:
		u, err := decodeUnsignedInt(content)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindTimeTicks, UInt: u}, nil
	case tagOpaque:
		return Value{Kind: KindOpaque, Str: append([]byte(nil), content...)}, nil
	case tagCounter64:
		u, err := decodeUnsignedInt(content)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindCounter64, UInt: u}, nil
	case tagNoSuchObject:
		return Value{Kind: KindNoSuchObject}, nil
	case tagNoSuchInstance:
		return Value{Kind: KindNoSuchInstance}, nil
	case tagEndOfMibView:
		return Value{Kind: KindEndOfMibView}, nil
	default:
		return Value{}, fmt.Errorf("%w: unrecognized value tag 0x%02x", ErrMalformed, tag)
	}
}
