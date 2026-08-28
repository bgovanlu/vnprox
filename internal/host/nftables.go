// SPDX-License-Identifier: Apache-2.0

// nftables.go implements T-3904's compiled-ruleset inspector: parsing
// `nft -j list ruleset` output into typed Go values. Like frr.go's
// ParseBGPSummary/ParseEVPNVNI and mdb.go's ParseMDB, this is a pure
// function over already-fetched bytes (Real fetches them via exec in
// netlink_linux.go; FixtureReader delegates to pvemock) so both
// production and fixture data flow through one parser.
//
// This is a READ-ONLY inspector. It never stages, validates, or applies
// anything — the permanent boundary docs/features.md's "still out of
// scope" section states ("vnprox configures the existing pve-firewall and
// never installs its own nftables ruleset") applies in full here: this
// file has no write path of any kind, and none should ever be added to
// it.
//
// Evidence-grounded, not documentation-modeled (CLAUDE.md's rule):
// planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt
// captured, read-only, against a real PVE 9.2.4 host (pvecube). The
// load-bearing finding that transcript documents: PVE 9.2.4 ships TWO
// firewall compile engines — the legacy Perl `pve-firewall` (compiles to
// iptables) and the newer Rust `proxmox-firewall` (compiles to nftables,
// still labeled "tech preview" by PVE's own schema, opt-in per host via
// host.fw's `nftables: 1` option, default off). On the evidence host,
// nftables is NOT the effective engine (the force-disable flag file for
// proxmox-firewall was present, and no host.fw/cluster.fw sets
// `nftables: 1`) — iptables is what would actually populate if the
// firewall were turned on. This file only ever reads nftables output
// (the card's explicit deliverable); a node running the legacy iptables
// engine simply has no PVE-authored nftables tables to show, and callers
// (internal/api/nftables.go) surface that as an explicit, honest empty
// state rather than presenting it as "no firewall configured."
//
// Table/chain-name recognition (pveBuiltinChains, pveTableNames below) is
// grounded in `strings` output against the actual installed
// proxmox-firewall 1.2.3 binary (evidence file §4, sha256 pinned there) —
// not invented, not copied from documentation. What the evidence file
// could NOT confirm (no populated ruleset was ever observed, since
// enabling the firewall to produce one would have mutated a live
// production host) is the exact per-rule expression shape and whether/how
// a compiled rule is tagged back to the pve-firewall/vnprox rule that
// produced it. This parser is therefore deliberately generic and tolerant
// at the rule/expression level: it extracts whatever a standard nftables
// JSON rule object exposes (nftables' own upstream wire format, not a PVE
// invention — libnftables-json(5)) — verdict, protocol, addresses, ports,
// interface names, and any comment — without assuming any of those fields
// carries a PVE-specific meaning beyond what upstream nft itself defines.

package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// NftTable is one nftables table (address family + name), e.g. "inet
// proxmox-firewall" or "bridge proxmox-firewall-guests" — the two real
// table names proxmox-firewall 1.2.3 creates (evidence file §4).
type NftTable struct {
	Family string
	Name   string
}

// String renders "<family> <name>", nft's own table-identity syntax.
func (t NftTable) String() string { return t.Family + " " + t.Name }

// IsPVEAuthored reports whether this table is one proxmox-firewall itself
// creates, per the evidence file's confirmed table-name list — the only
// tables this inspector can honestly attribute to PVE's compiled firewall
// output rather than to some unrelated nftables user on the same host
// (docs/features.md's permanent boundary: vnprox never installs its own
// ruleset, so any *other* table found here was not put there by vnprox
// either — it is simply out of scope for this inspector, not hidden, see
// NftRuleset.OtherTables).
func (t NftTable) IsPVEAuthored() bool {
	switch t.String() {
	case "inet proxmox-firewall", "bridge proxmox-firewall-guests":
		return true
	default:
		return false
	}
}

// pveBuiltinChains are the fixed set of chains proxmox-firewall's own
// installer creates regardless of any user-authored rule (evidence file
// §4's `add chain ...` list) — PVE's own protection/plumbing chains
// (default policy, anti-spoofing, ICMP/NDP/DHCP allow-lists, the
// before-bridge/do-reject dispatch chains, ...). A rule found inside one
// of these is never attributed to a specific vnprox-authored FwRule: it
// exists whether or not the operator wrote any rule at all, so guessing a
// match would be actively misleading (CLAUDE.md: "a wrong attribution
// here is worse than none").
var pveBuiltinChains = map[string]bool{
	"input": true, "output": true, "forward": true,
	"host-bridge-input": true, "host-bridge-output": true,
	"accept-management": true, "allow-icmp": true,
	"allow-ndp-in": true, "allow-ndp-out": true, "before-bridge": true,
	"block-invalid-tcp": true, "block-ndp-in": true, "block-ndp-out": true,
	"block-smurfs": true, "block-synflood": true,
	"default-in": true, "default-out": true, "do-reject": true,
	"log-drop-invalid-tcp": true, "log-drop-smurfs": true,
	// bridge proxmox-firewall-guests table's own fixed chains.
	"vm-in": true, "vm-out": true, "pre-vm-in": true, "pre-vm-out": true,
	"allow-dhcp-in": true, "allow-dhcp-out": true,
	"block-dhcp-in": true, "block-dhcp-out": true,
	"allow-ra-out": true, "block-ra-out": true,
}

// IsPVEBuiltinChain reports whether name is one of proxmox-firewall's own
// fixed protection/plumbing chains (see pveBuiltinChains' doc comment) —
// present on every node running the nftables engine regardless of any
// user-authored rule, so a rule found there is never guessed to have come
// from a specific vnprox FwRule.
func IsPVEBuiltinChain(name string) bool { return pveBuiltinChains[name] }

// NftChain is one chain within an NftTable.
type NftChain struct {
	// Hook/Priority/Policy are only set for a base chain (one attached to
	// a netfilter hook, e.g. "input"/"forward"); a regular (non-base)
	// chain — most of proxmox-firewall's own chains, used as jump/goto
	// targets — leaves these empty, matching nft's own JSON convention of
	// simply omitting the fields rather than emitting a sentinel.
	Table    NftTable
	Name     string
	Type     string
	Hook     string
	Priority string
	Policy   string
}

// NftRule is one rule within an NftChain. Expr carries the rule's raw,
// unmodified nft JSON expression array (json.RawMessage) so the UI can
// always fall back to showing exactly what nft reported, even for an
// expression shape this parser's best-effort field extraction below does
// not recognize — no information is ever silently dropped.
//
// Field order below (strings, then the fixed-size Table struct and Expr
// slice, then Handle/Log) satisfies golangci-lint's fieldalignment check
// rather than following topic grouping — see the field doc comments below
// for the conceptual grouping.
type NftRule struct {
	Chain   string
	Comment string
	// Verdict is the rule's terminal action when recognizable: "accept",
	// "drop", "reject", "continue", "return", "jump <chain>", or
	// "goto <chain>". Empty when no verdict statement was recognized in
	// Expr (e.g. a rule that only updates a counter/log and falls through
	// to the chain's own policy).
	Verdict string
	// Proto is the matched layer-4 protocol when a single one is
	// unambiguously matched ("tcp", "udp", "icmp", "icmpv6", ...), else
	// empty.
	Proto string
	// SrcAddr/DstAddr/SrcPort/DstPort are best-effort, human-readable
	// renderings of any address/port match found in Expr (e.g.
	// "10.0.0.0/24", "22", "1024-2048") — empty when no such match exists
	// in this rule (a protocol-only or interface-only rule is common and
	// legitimate, not a parse failure).
	SrcAddr string
	DstAddr string
	SrcPort string
	DstPort string
	// IIfname/OIfname are input/output interface-name matches, when
	// present (nft's `iifname`/`oifname` match, or the bridge-family
	// `meta ibrname`/`obrname` equivalents).
	IIfname string
	OIfname string
	Table   NftTable
	// Expr is the rule's full, unmodified nft JSON expression array —
	// always populated, regardless of how much of it the fields above
	// could interpret. See this file's doc comment: no populated real
	// ruleset was available to confirm every expression shape
	// proxmox-firewall emits, so this raw form is the honest fallback.
	Expr   json.RawMessage
	Handle int
	// Log is true when the rule includes a `log` statement.
	Log bool
}

// NftRuleset is a full parsed `nft -j list ruleset` document.
type NftRuleset struct {
	Tables []NftTable
	Chains []NftChain
	Rules  []NftRule
}

// PVETables returns only the tables IsPVEAuthored recognizes.
func (rs NftRuleset) PVETables() []NftTable {
	var out []NftTable
	for _, t := range rs.Tables {
		if t.IsPVEAuthored() {
			out = append(out, t)
		}
	}
	return out
}

// --- wire shapes (nft -j list ruleset's own JSON schema) -------------------

type nftDoc struct {
	Nftables []json.RawMessage `json:"nftables"`
}

type nftTableJSON struct {
	Family string `json:"family"`
	Name   string `json:"name"`
}

type nftChainJSON struct {
	Table    string          `json:"table"`
	Family   string          `json:"family"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Hook     string          `json:"hook"`
	Policy   string          `json:"policy"`
	Priority json.RawMessage `json:"prio"`
}

type nftRuleJSON struct {
	Table   string            `json:"table"`
	Family  string            `json:"family"`
	Chain   string            `json:"chain"`
	Comment string            `json:"comment"`
	Expr    []json.RawMessage `json:"expr"`
	Handle  int               `json:"handle"`
}

// ParseNftRuleset parses `nft -j list ruleset` JSON output (the tolerant,
// versioned wire format nft itself defines — libnftables-json(5)) into a
// flat, sorted NftRuleset. Malformed, truncated, or adversarial input
// never panics: any unexpected internal panic is recovered and returned
// as an error (matching ParseMDB/ParseBGPSummary's convention), and an
// individual malformed table/chain/rule object within an otherwise-
// parseable document is skipped rather than failing the whole parse.
// Empty input, or a document with only a `metainfo` object and no
// tables — the real, observed shape of a disabled-firewall node (evidence
// file §2) — returns a zero-value NftRuleset, nil: "no compiled ruleset"
// is not itself a parse error.
func ParseNftRuleset(raw []byte) (rs NftRuleset, err error) {
	defer func() {
		if r := recover(); r != nil {
			rs, err = NftRuleset{}, fmt.Errorf("host: nftables: parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return NftRuleset{}, nil
	}

	var doc nftDoc
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return NftRuleset{}, fmt.Errorf("host: nftables: parsing ruleset: %w", err)
	}

	for _, item := range doc.Nftables {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(item, &wrapper); err != nil {
			continue // not an object — skip, defensively
		}
		if raw, ok := wrapper["table"]; ok {
			var t nftTableJSON
			if err := json.Unmarshal(raw, &t); err == nil && t.Name != "" {
				rs.Tables = append(rs.Tables, NftTable(t))
			}
			continue
		}
		if raw, ok := wrapper["chain"]; ok {
			var c nftChainJSON
			if err := json.Unmarshal(raw, &c); err == nil && c.Name != "" {
				rs.Chains = append(rs.Chains, NftChain{
					Table:    NftTable{Family: c.Family, Name: c.Table},
					Name:     c.Name,
					Type:     c.Type,
					Hook:     c.Hook,
					Policy:   c.Policy,
					Priority: flexPriority(c.Priority),
				})
			}
			continue
		}
		if raw, ok := wrapper["rule"]; ok {
			var rj nftRuleJSON
			if err := json.Unmarshal(raw, &rj); err == nil && rj.Chain != "" {
				rs.Rules = append(rs.Rules, nftRuleFromJSON(rj))
			}
			continue
		}
		// "metainfo" and any other/future top-level object kind: not
		// relevant to this inspector, skipped rather than erroring — nft's
		// own JSON schema is explicitly versioned and may add object
		// kinds this parser doesn't yet know about.
	}

	sort.Slice(rs.Tables, func(i, j int) bool { return rs.Tables[i].String() < rs.Tables[j].String() })
	sort.Slice(rs.Chains, func(i, j int) bool {
		if rs.Chains[i].Table.String() != rs.Chains[j].Table.String() {
			return rs.Chains[i].Table.String() < rs.Chains[j].Table.String()
		}
		return rs.Chains[i].Name < rs.Chains[j].Name
	})
	// Rules keep nft's own document order within a chain (rule order is
	// significant — it is an ordered evaluation list, not sorted like
	// tables/chains above), stable-sorted only by (table, chain) so rules
	// from the same chain stay contiguous when a document interleaves
	// chains from different tables.
	sort.SliceStable(rs.Rules, func(i, j int) bool {
		if rs.Rules[i].Table.String() != rs.Rules[j].Table.String() {
			return rs.Rules[i].Table.String() < rs.Rules[j].Table.String()
		}
		return rs.Rules[i].Chain < rs.Rules[j].Chain
	})
	return rs, nil
}

// flexPriority renders nft's `prio` field (a JSON number for a numeric
// priority, or a string for a symbolic one like "filter", "filter - 1")
// as plain text, tolerating either encoding the way this package's other
// parsers (flexInt et al., lldp.go) already do for similarly
// inconsistently-encoded fields.
func flexPriority(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

func nftRuleFromJSON(rj nftRuleJSON) NftRule {
	r := NftRule{
		Table:   NftTable{Family: rj.Family, Name: rj.Table},
		Chain:   rj.Chain,
		Comment: rj.Comment,
		Handle:  rj.Handle,
	}
	exprBytes, err := json.Marshal(rj.Expr)
	if err == nil {
		r.Expr = exprBytes
	}
	for _, e := range rj.Expr {
		applyNftExprStatement(&r, e)
	}
	return r
}

// applyNftExprStatement inspects one element of a rule's `expr` array
// (nft's own JSON statement/expression vocabulary — match, verdict,
// counter, log, ...) and fills in whatever NftRule field it can
// recognize. Deliberately conservative: an expr element this function
// does not recognize is simply left uninterpreted (still present
// verbatim in NftRule.Expr) rather than guessed at — see this file's doc
// comment on why no populated real ruleset was available to confirm every
// shape proxmox-firewall emits.
func applyNftExprStatement(r *NftRule, raw json.RawMessage) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 1 {
		return
	}
	for key, val := range obj {
		switch key {
		case "accept", "drop", "continue", "return":
			r.Verdict = key
		case "reject":
			r.Verdict = "reject"
		case "log":
			r.Log = true
		case "jump", "goto":
			var t struct {
				Target string `json:"target"`
			}
			if json.Unmarshal(val, &t) == nil && t.Target != "" {
				r.Verdict = key + " " + t.Target
			}
		case "match":
			applyNftMatch(r, val)
		}
	}
}

// nftMatchJSON is a `match` statement's shape: {left, right, op}, where
// `left` names what's being matched (a payload/meta/ct expression) and
// `right` is the value it's compared against.
type nftMatchJSON struct {
	Left  json.RawMessage `json:"left"`
	Right json.RawMessage `json:"right"`
}

func applyNftMatch(r *NftRule, raw json.RawMessage) {
	var m nftMatchJSON
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	var left map[string]json.RawMessage
	if err := json.Unmarshal(m.Left, &left); err != nil {
		return
	}
	rightStr := nftValueString(m.Right)
	if payload, ok := left["payload"]; ok {
		applyNftPayloadMatch(r, payload, rightStr)
		return
	}
	if meta, ok := left["meta"]; ok {
		applyNftMetaMatch(r, meta, rightStr)
	}
}

type nftPayloadJSON struct {
	Protocol string `json:"protocol"`
	Field    string `json:"field"`
}

func applyNftPayloadMatch(r *NftRule, raw json.RawMessage, value string) {
	var p nftPayloadJSON
	if err := json.Unmarshal(raw, &p); err != nil || value == "" {
		return
	}
	switch p.Protocol {
	case "ip", "ip6":
		switch p.Field {
		case "saddr":
			r.SrcAddr = value
		case "daddr":
			r.DstAddr = value
		}
	case "tcp", "udp":
		r.Proto = p.Protocol
		switch p.Field {
		case "sport":
			r.SrcPort = value
		case "dport":
			r.DstPort = value
		}
	case "icmp", "icmpv6":
		r.Proto = p.Protocol
	}
}

type nftMetaKeyJSON struct {
	Key string `json:"key"`
}

func applyNftMetaMatch(r *NftRule, raw json.RawMessage, value string) {
	var m nftMetaKeyJSON
	if err := json.Unmarshal(raw, &m); err != nil || value == "" {
		return
	}
	switch m.Key {
	case "iifname", "ibrname":
		r.IIfname = value
	case "oifname", "obrname":
		r.OIfname = value
	case "l4proto":
		r.Proto = firstNonEmpty(r.Proto, value)
	}
}

// nftValueString renders a `right`-hand match value (a bare string/number,
// or an object like {"prefix":{"addr":"10.0.0.0","len":24}} or
// {"range":["1024","2048"]}) as a single display string. Falls back to
// the raw JSON text for any shape not specifically recognized, rather
// than dropping the value.
func nftValueString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	var prefix struct {
		Prefix struct {
			Addr string          `json:"addr"`
			Len  json.RawMessage `json:"len"`
		} `json:"prefix"`
	}
	if err := json.Unmarshal(raw, &prefix); err == nil && prefix.Prefix.Addr != "" {
		return prefix.Prefix.Addr + "/" + flexPriority(prefix.Prefix.Len)
	}
	var rng struct {
		Range []json.RawMessage `json:"range"`
	}
	if err := json.Unmarshal(raw, &rng); err == nil && len(rng.Range) == 2 {
		return nftValueString(rng.Range[0]) + "-" + nftValueString(rng.Range[1])
	}
	return string(raw)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
