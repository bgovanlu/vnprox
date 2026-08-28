// SPDX-License-Identifier: Apache-2.0

package snmp

import "testing"

// FuzzDecodeMessage exercises DecodeMessage against arbitrary byte strings —
// CLAUDE.md's "fuzz the decoder if practical" for this card: a GetResponse
// datagram is network input from a device vnprox does not control, and this
// repo has a `fuzz` CI job (scripts/ci-local.sh's job_fuzz) that runs every
// Fuzz* function in the tree for a bounded corpus pass. The only property
// under test is "never panic, always return either a Message or an error" —
// DecodeMessage's own doc comment states that as its contract, and every
// bound in ber.go/pdu.go/value.go exists to make it true.
func FuzzDecodeMessage(f *testing.F) {
	// Seed with real, valid encodings so the fuzzer starts from structurally
	// interesting inputs rather than pure noise.
	oid := MustParseOID("1.3.6.1.2.1.2.2.1.14.1")
	if raw, err := EncodeGetRequest([]byte("public"), 1, []OID{oid}); err == nil {
		f.Add(raw)
	}
	if raw, err := EncodeGetBulkRequest([]byte("public"), 1, 0, 10, []OID{oid}); err == nil {
		f.Add(raw)
	}
	f.Add(buildGetResponse(1, tagCounter32, []byte{0, 0, 0, 42}, oid))
	f.Add(buildGetResponse(1, tagNoSuchInstance, nil, oid))
	f.Add([]byte{})
	f.Add([]byte{0x30, 0x00})
	f.Add([]byte{0x30, 0x84, 0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("DecodeMessage panicked on input %x: %v", data, r)
			}
		}()
		_, _ = DecodeMessage(data)
	})
}
