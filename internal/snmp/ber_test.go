// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"bytes"
	"errors"
	"testing"
)

func TestAppendReadLength_RoundTrip(t *testing.T) {
	cases := []int{0, 1, 0x7f, 0x80, 0xff, 0x100, 0x1000, maxMessageSize}
	for _, n := range cases {
		var buf []byte
		buf = appendLength(buf, n)
		got, consumed, err := readLength(buf)
		if err != nil {
			t.Fatalf("readLength(%d) round trip: %v", n, err)
		}
		if got != n {
			t.Errorf("readLength round trip: got %d, want %d", got, n)
		}
		if consumed != len(buf) {
			t.Errorf("readLength consumed %d, want %d", consumed, len(buf))
		}
	}
}

func TestReadLength_Bounds(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"indefinite", []byte{0x80}},
		{"five-octet-length", []byte{0x85, 1, 1, 1, 1, 1}},
		{"truncated-long-form", []byte{0x82, 0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := readLength(tt.in)
			if err == nil {
				t.Fatalf("readLength(%v) succeeded, want error", tt.in)
			}
		})
	}
}

func TestReadTLV_DeclaredLengthExceedsBuffer(t *testing.T) {
	// tag=0x02 (INTEGER), length says 10 bytes follow, only 1 is present.
	_, _, _, err := readTLV([]byte{0x02, 0x0a, 0x01})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("readTLV with over-long declared length = %v, want ErrMalformed", err)
	}
}

func TestEncodeDecodeInteger_RoundTrip(t *testing.T) {
	cases := []int32{0, 1, -1, 127, 128, -128, -129, 32767, -32768, 1 << 30, -(1 << 30), 2147483647, -2147483648}
	for _, v := range cases {
		var buf []byte
		buf = appendInteger(buf, tagInteger, v)
		tag, content, rest, err := readTLV(buf)
		if err != nil {
			t.Fatalf("readTLV(%d): %v", v, err)
		}
		if tag != tagInteger || len(rest) != 0 {
			t.Fatalf("readTLV(%d): tag=0x%02x rest=%v", v, tag, rest)
		}
		got, err := decodeSignedInt(content)
		if err != nil {
			t.Fatalf("decodeSignedInt(%d): %v", v, err)
		}
		if got != int64(v) {
			t.Errorf("round trip %d -> %d", v, got)
		}
	}
}

func TestDecodeSignedInt_TooWide(t *testing.T) {
	_, err := decodeSignedInt(make([]byte, 9))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("decodeSignedInt(9 bytes) = %v, want ErrMalformed", err)
	}
}

func TestDecodeUnsignedInt(t *testing.T) {
	cases := []struct {
		content []byte
		want    uint64
	}{
		{[]byte{0x00}, 0},
		{[]byte{0xff}, 0xff},
		{[]byte{0x00, 0xff, 0xff, 0xff, 0xff}, 0xffffffff}, // padded Counter32 at max
		{[]byte{0x01, 0x00}, 0x100},
	}
	for _, tt := range cases {
		got, err := decodeUnsignedInt(tt.content)
		if err != nil {
			t.Fatalf("decodeUnsignedInt(%v): %v", tt.content, err)
		}
		if got != tt.want {
			t.Errorf("decodeUnsignedInt(%v) = %d, want %d", tt.content, got, tt.want)
		}
	}
}

func TestDecodeUnsignedInt_TooWide(t *testing.T) {
	_, err := decodeUnsignedInt(make([]byte, 10))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("decodeUnsignedInt(10 bytes) = %v, want ErrMalformed", err)
	}
}

func TestOID_EncodeDecode_RoundTrip(t *testing.T) {
	cases := []string{
		"1.3.6.1.2.1.2.2.1.14",       // ifInErrors
		"1.3.6.1.2.1.2.2.1.14.1",     // ifInErrors.1 (instance)
		"1.3.6.1.2.1.31.1.1.1.6.128", // ifHCInOctets.128
		"0.0",
		"2.999.1",
	}
	for _, s := range cases {
		oid, err := ParseOID(s)
		if err != nil {
			t.Fatalf("ParseOID(%q): %v", s, err)
		}
		content := encodeOIDContent(oid)
		got, err := decodeOID(content)
		if err != nil {
			t.Fatalf("decodeOID round trip for %q: %v", s, err)
		}
		if !got.Equal(oid) {
			t.Errorf("OID round trip: %q -> %s, want %s", s, got, oid)
		}
	}
}

func TestDecodeOID_Bounds(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, err := decodeOID(nil)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("decodeOID(nil) = %v, want ErrMalformed", err)
		}
	})
	t.Run("truncated-continuation", func(t *testing.T) {
		// A single byte with the continuation bit set and nothing after it.
		_, err := decodeOID([]byte{0x80})
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("decodeOID(truncated) = %v, want ErrTruncated", err)
		}
	})
	t.Run("subid-too-wide", func(t *testing.T) {
		// 6 continuation bytes in one group: exceeds the 5-byte cap.
		_, err := decodeOID([]byte{0x81, 0x81, 0x81, 0x81, 0x81, 0x01})
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("decodeOID(too-wide subid) = %v, want ErrMalformed", err)
		}
	})
}

func TestParseOID_Bounds(t *testing.T) {
	tests := []string{"", "1", "1.2.3.notanumber", "1.-2.3"}
	for _, s := range tests {
		if _, err := ParseOID(s); err == nil {
			t.Errorf("ParseOID(%q) succeeded, want error", s)
		}
	}
	// Too many components.
	long := "1.3"
	for i := 0; i < maxOIDComponents; i++ {
		long += ".1"
	}
	if _, err := ParseOID(long); err == nil {
		t.Errorf("ParseOID with %d components succeeded, want error", maxOIDComponents+2)
	}
}

func TestOID_HasPrefix(t *testing.T) {
	base := MustParseOID("1.3.6.1.2.1.2.2.1.14")
	row := base.Append(1)
	if !row.HasPrefix(base) {
		t.Errorf("%s should have prefix %s", row, base)
	}
	other := MustParseOID("1.3.6.1.2.1.2.2.1.20")
	if row.HasPrefix(other) {
		t.Errorf("%s should not have prefix %s", row, other)
	}
}

func TestAppendOctetString(t *testing.T) {
	var buf []byte
	buf = appendOctetString(buf, []byte("public"))
	tag, content, rest, err := readTLV(buf)
	if err != nil {
		t.Fatalf("readTLV: %v", err)
	}
	if tag != tagOctetString || !bytes.Equal(content, []byte("public")) || len(rest) != 0 {
		t.Fatalf("got tag=0x%02x content=%q rest=%v", tag, content, rest)
	}
}
