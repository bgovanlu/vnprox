package capture

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// maxFilterLen bounds a submitted BPF filter string's raw length — a
// generous ceiling (a hand-written pcap filter is at most a few primitives)
// that rejects an abusive/garbage blob before any per-token work.
const maxFilterLen = 1024

// DefaultMaxFilterInstructions is the default ceiling on a filter's
// estimated instruction count (roughly its primitive/token count) — the
// "compiles to more than a configured instruction-count ceiling" rejection
// the card calls for, approximated without a libpcap compiler (stdlib-only).
// Configurable via [capture] max_filter_instructions.
const DefaultMaxFilterInstructions = 64

// bpfKeywords is the closed set of pcap-filter primitive/qualifier keywords
// this validator recognizes as safe. A bare alphabetic token that is not one
// of these (and not a number/IP/CIDR/operator) is rejected — which is what
// turns an injection attempt like "tcp and rm" into a hard error before the
// filter can reach a capture process.
var bpfKeywords = map[string]bool{
	"host": true, "net": true, "port": true, "portrange": true,
	"src": true, "dst": true, "and": true, "or": true, "not": true,
	"tcp": true, "udp": true, "icmp": true, "icmp6": true, "igmp": true,
	"arp": true, "rarp": true, "ip": true, "ip6": true, "ether": true,
	"vlan": true, "mpls": true, "proto": true, "gateway": true,
	"broadcast": true, "multicast": true, "less": true, "greater": true,
	"len": true, "inbound": true, "outbound": true,
}

// bpfComparators is the set of comparison/arithmetic operators a pcap filter
// may contain (e.g. `ip[0] & 0xf != 5`, `len > 100`). `&&`/`||` are
// deliberately NOT accepted — pcap's `and`/`or` keywords express the same
// thing, and rejecting the `&`/`|` characters outright keeps the shell-unsafe
// surface minimal.
var bpfComparators = map[string]bool{
	"<": true, ">": true, "=": true, "==": true, "!=": true, "<=": true, ">=": true,
}

// filterRuneAllowed reports whether r may appear in a submitted filter.
// Everything outside this set (notably ';', '`', '$', '|', '&', '\\',
// quotes, and control characters) is rejected: none is needed to express a
// pcap filter, and each is a classic shell/exec-injection vector against the
// real capture backend.
func filterRuneAllowed(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == ' ', r == '\t':
		return true
	case r == '.', r == ':', r == '/', r == '-', r == '_':
		return true
	case r == '(', r == ')', r == '[', r == ']':
		return true
	case r == '<', r == '>', r == '=', r == '!':
		return true
	default:
		return false
	}
}

// ValidateFilter checks a submitted pcap/BPF filter expression against the
// server-side policy: within the length ceiling, containing only characters
// valid in a pcap filter, within maxInstr estimated instructions, and made
// up only of recognized keywords / numbers / IPs / CIDRs / operators. An
// empty filter is valid (it means "capture everything on the scoped
// interface"). It returns an ErrInvalidFilter-wrapped error otherwise; the
// caller must reject the request without invoking any capture process.
//
// This is a conservative syntactic gate, not a full libpcap compile (which
// would need a C dependency, out of scope per CLAUDE.md) — the on-hardware
// engine additionally compiles the filter with libpcap before use; see the
// package doc comment / needs-hardware-validation. The gate is intentionally
// strict: a filter it cannot positively recognize is rejected, never passed
// through.
func ValidateFilter(filter string, maxInstr int) error {
	if maxInstr <= 0 {
		maxInstr = DefaultMaxFilterInstructions
	}
	trimmed := strings.TrimSpace(filter)
	if trimmed == "" {
		return nil
	}
	if len(filter) > maxFilterLen {
		return fmt.Errorf("%w: filter is %d bytes, over the %d-byte ceiling", ErrInvalidFilter, len(filter), maxFilterLen)
	}
	for _, r := range filter {
		if !filterRuneAllowed(r) {
			return fmt.Errorf("%w: disallowed character %q", ErrInvalidFilter, string(r))
		}
	}

	tokens := tokenizeFilter(trimmed)
	if len(tokens) > maxInstr {
		return fmt.Errorf("%w: filter has %d primitives, over the %d-instruction ceiling", ErrInvalidFilter, len(tokens), maxInstr)
	}
	for _, tok := range tokens {
		if err := validateToken(tok); err != nil {
			return err
		}
	}
	return nil
}

// tokenizeFilter splits a filter into bare tokens, treating parentheses and
// brackets as their own delimiters so "tcp and(port 80)" and
// "tcp and ( port 80 )" tokenize identically.
func tokenizeFilter(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '(' || r == ')'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// validateToken accepts a single filter token if it is a recognized keyword,
// a comparison operator, a decimal/hex number, an IP address, a CIDR, or a
// bracketed byte-offset access ("ip[0]", "tcp[13]"). Anything else is an
// unknown token and rejected.
func validateToken(tok string) error {
	// Strip a bracketed byte-access suffix, e.g. "ip[0]" -> "ip", "tcp[13:2]"
	// -> "tcp" (the offset/size inside brackets is bounded by the charset
	// check already; only its host keyword needs to be known).
	base := tok
	if i := strings.IndexByte(base, '['); i >= 0 {
		if !strings.HasSuffix(base, "]") {
			return fmt.Errorf("%w: malformed byte access %q", ErrInvalidFilter, tok)
		}
		base = base[:i]
	}
	lower := strings.ToLower(base)
	switch {
	case lower == "":
		return nil
	case bpfKeywords[lower]:
		return nil
	case bpfComparators[base]:
		return nil
	case isNumber(base):
		return nil
	case net.ParseIP(base) != nil:
		return nil
	case isCIDR(base):
		return nil
	default:
		return fmt.Errorf("%w: unrecognized token %q", ErrInvalidFilter, tok)
	}
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		_, err := strconv.ParseUint(s[2:], 16, 64)
		return err == nil
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func isCIDR(s string) bool {
	_, _, err := net.ParseCIDR(s)
	return err == nil
}
