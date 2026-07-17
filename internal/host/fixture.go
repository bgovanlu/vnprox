package host

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// wrapFixtureErr maps pvemock.ErrNotFound (returned for an unknown node)
// onto this package's own ErrNotFound sentinel, pvemock.ErrFRRUnavailable
// (returned for a node whose fixture declares no FRR state at all) onto
// this package's own ErrFRRUnavailable sentinel, and pvemock.
// ErrCorosyncUnavailable onto this package's own ErrCorosyncUnavailable
// sentinel (T-803), so callers can use errors.Is(err, host.ErrNotFound)/
// errors.Is(err, host.ErrFRRUnavailable)/errors.Is(err,
// host.ErrCorosyncUnavailable) without depending on pvemock's error values
// directly; any other error is wrapped with context as-is.
func wrapFixtureErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pvemock.ErrNotFound) {
		return fmt.Errorf("host: fixture: %w: %w", ErrNotFound, err)
	}
	if errors.Is(err, pvemock.ErrFRRUnavailable) {
		return fmt.Errorf("host: fixture: %w: %w", ErrFRRUnavailable, err)
	}
	if errors.Is(err, pvemock.ErrCorosyncUnavailable) {
		return fmt.Errorf("host: fixture: %w: %w", ErrCorosyncUnavailable, err)
	}
	return fmt.Errorf("host: fixture: %w", err)
}

// pvemockReader is the method set FixtureReader needs from
// *pvemock.FixtureHostReader. Declared here (rather than depending on the
// concrete type directly in every method) purely for testability with a
// hand-rolled stand-in; production callers always pass an actual
// *pvemock.FixtureHostReader (see NewFixtureReader).
type pvemockReader interface {
	InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)
	Links(ctx context.Context, node string) ([]pvemock.LinkState, error)
	LLDP(ctx context.Context, node string) ([]byte, error)
	Stats(ctx context.Context, node string) (map[string]pvemock.IfaceStats, error)
	FRRBGPSummary(ctx context.Context, node string) ([]byte, error)
	FRREVPNVNI(ctx context.Context, node string) ([]byte, error)
	DHCPLeases(ctx context.Context, node string) ([]byte, error)
	Services(ctx context.Context, node string) (map[string]bool, error)
	CorosyncStatus(ctx context.Context, node string) ([]byte, error)
}

// FixtureReader adapts a *pvemock.FixtureHostReader (T-004's YAML
// fixture-backed HostReader) into this package's Reader interface, so
// tests anywhere in vnprox can drive host.Reader-shaped code against the
// same cluster fixtures the mock PVE server itself uses — one consistent
// view of the world (see internal/pvemock/hostreader.go's doc comment).
//
// It is deliberately thin: pvemock.LinkState only carries what
// docs/data-model.md's PhysNic/Bond/Bridge contract needs at the surface
// (name, kind, mac, driver, speed, duplex, mtu, members, link state) —
// there is no /proc/net/bonding or netlink bridge-VLAN dump behind a
// fixture, since fixtures describe declared intent and simple runtime
// facts, not full kernel state. FixtureReader recovers the bond
// mode/xmit-hash-policy and bridge VLAN-awareness/VLAN-ID detail that
// *is* expressed in the fixture's rendered interfaces(5) text by parsing
// that text with this package's own ParseInterfaces — the same parser
// Real's callers would use — rather than reaching into pvemock's
// unexported fixture state. Addresses and per-port VLAN membership are left
// empty: they are live-runtime-only concepts fixtures do not model at that
// granularity. Bond slave MII status/active-slave detail is approximated as
// "every member is up and active" when the underlying link itself reports
// LinkUp, since fixtures do not carry independent per-slave failure state.
//
// FDB is the one exception (T-306): a bridge's forwarding table is
// naturally expressible as declared fixture data ("these MACs were learned
// on these ports"), so pvemock.LinkInfo.FDB is fixture-declared directly
// and passed through by convertFixtureFDB below, rather than being left
// empty like the other live-runtime-only fields.
type FixtureReader struct {
	r pvemockReader
}

// NewFixtureReader wraps an existing *pvemock.FixtureHostReader.
func NewFixtureReader(r *pvemock.FixtureHostReader) *FixtureReader {
	return &FixtureReader{r: r}
}

var _ Reader = (*FixtureReader)(nil)

// InterfacesFile implements Reader by delegating directly.
func (f *FixtureReader) InterfacesFile(ctx context.Context, node string, includePending bool) (string, error) {
	s, err := f.r.InterfacesFile(ctx, node, includePending)
	if err != nil {
		return "", wrapFixtureErr(err)
	}
	return s, nil
}

// LLDP implements Reader by delegating directly.
func (f *FixtureReader) LLDP(ctx context.Context, node string) ([]byte, error) {
	b, err := f.r.LLDP(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	return b, nil
}

// FRRBGPSummary implements Reader by delegating directly; wrapFixtureErr
// maps pvemock.ErrFRRUnavailable (a node whose fixture declares no `frr:`
// block at all — T-404's "FRR entirely absent on a node" case) onto this
// package's ErrFRRUnavailable.
func (f *FixtureReader) FRRBGPSummary(ctx context.Context, node string) ([]byte, error) {
	b, err := f.r.FRRBGPSummary(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	return b, nil
}

// FRREVPNVNI implements Reader by delegating directly.
func (f *FixtureReader) FRREVPNVNI(ctx context.Context, node string) ([]byte, error) {
	b, err := f.r.FRREVPNVNI(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	return b, nil
}

// DHCPLeases implements Reader by delegating directly (T-406).
func (f *FixtureReader) DHCPLeases(ctx context.Context, node string) ([]byte, error) {
	b, err := f.r.DHCPLeases(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	return b, nil
}

// Stats implements Reader, converting pvemock.IfaceStats to
// host.IfaceStats (identical field sets, different named types — see the
// Reader doc comment in reader.go on why the two aren't the same type).
func (f *FixtureReader) Stats(ctx context.Context, node string) (map[string]IfaceStats, error) {
	in, err := f.r.Stats(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	out := make(map[string]IfaceStats, len(in))
	for name, s := range in {
		out[name] = IfaceStats{
			RxBytes: s.RxBytes, TxBytes: s.TxBytes,
			RxPackets: s.RxPackets, TxPackets: s.TxPackets,
			RxErrors: s.RxErrors, TxErrors: s.TxErrors,
			RxDropped: s.RxDropped, TxDropped: s.TxDropped,
		}
	}
	return out, nil
}

// CorosyncStatus implements Reader by delegating directly; wrapFixtureErr
// maps pvemock.ErrCorosyncUnavailable (a node whose fixture declares no
// `corosync:` block at all) onto this package's ErrCorosyncUnavailable
// (T-803).
func (f *FixtureReader) CorosyncStatus(ctx context.Context, node string) ([]byte, error) {
	b, err := f.r.CorosyncStatus(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	return b, nil
}

// Services implements Reader by delegating directly.
func (f *FixtureReader) Services(ctx context.Context, node string) (map[string]bool, error) {
	s, err := f.r.Services(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	return s, nil
}

// Links implements Reader: it fetches pvemock's minimal LinkState list,
// then enriches Bond/Bridge/VLAN detail by parsing the same node's
// rendered interfaces(5) file with this package's own AST parser (see the
// FixtureReader doc comment for what is and isn't recoverable this way).
func (f *FixtureReader) Links(ctx context.Context, node string) ([]LinkState, error) {
	links, err := f.r.Links(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}

	var parsed *File
	if raw, err := f.r.InterfacesFile(ctx, node, false); err == nil {
		if pf, err := ParseInterfaces([]byte(raw)); err == nil {
			parsed = pf
		}
	}

	out := make([]LinkState, 0, len(links))
	for _, l := range links {
		out = append(out, convertFixtureLink(l, parsed))
	}
	return out, nil
}

func convertFixtureLink(l pvemock.LinkState, parsed *File) LinkState {
	ls := LinkState{
		Name:      l.Name,
		Kind:      normalizeFixtureKind(l.Kind),
		Mac:       l.Mac,
		Driver:    l.Driver,
		PCIAddr:   l.PCIAddr,
		MTU:       l.MTU,
		LinkUp:    l.LinkUp,
		SpeedMbps: l.SpeedMbps,
		Duplex:    l.Duplex,
		Members:   append([]string(nil), l.Members...),
	}
	if l.LinkUp {
		ls.OperState = "up"
	} else {
		ls.OperState = "down"
	}

	var opts *Entry
	if parsed != nil {
		opts, _ = parsed.Iface(l.Name)
	}

	switch ls.Kind {
	case "bond":
		ls.Bond = fixtureBondDetail(ls.Members, ls.LinkUp, opts)
	case "bridge":
		ls.Bridge = fixtureBridgeDetail(opts, l.FDB)
	case "vlan":
		ls.VlanID, ls.VlanParent = fixtureVlanInfo(l.Name, opts)
	}
	return ls
}

// normalizeFixtureKind maps pvemock's NetIface.Type strings (which mirror
// PVE's own network API vocabulary: "eth", "bridge", "OVSBridge", "bond",
// "vlan", "loopback", ...) onto this package's Kind naming (see
// buildLinkState in netlink_linux.go for the "real" side of the same
// vocabulary).
func normalizeFixtureKind(t string) string {
	switch t {
	case "eth":
		return "physical"
	case "OVSBridge":
		return "bridge"
	case "":
		return "unknown"
	default:
		return t
	}
}

func fixtureBondDetail(members []string, up bool, opts *Entry) *BondDetail {
	bd := &BondDetail{}
	if opts != nil {
		if v, ok := opts.Get("bond-mode"); ok {
			bd.Mode = v
		}
		if v, ok := opts.Get("bond-xmit-hash-policy"); ok {
			bd.XmitHashPolicy = v
		}
		if v, ok := opts.Get("bond-lacp-rate"); ok {
			bd.LACPRate = v
		}
	}
	if up {
		bd.MIIStatus = "up"
	} else {
		bd.MIIStatus = "down"
	}
	for _, name := range members {
		bd.Slaves = append(bd.Slaves, BondSlave{Name: name, MIIStatus: bd.MIIStatus, Active: up})
	}
	if up && len(bd.Slaves) > 0 {
		bd.ActiveSlave = bd.Slaves[0].Name
	}
	return bd
}

func fixtureBridgeDetail(opts *Entry, fdb []pvemock.FDBEntry) *BridgeDetail {
	bd := &BridgeDetail{FDB: convertFixtureFDB(fdb)}
	if opts == nil {
		return bd
	}
	if v, ok := opts.Get("bridge-vlan-aware"); ok {
		bd.VlanAware = v == "yes" || v == "true" || v == "1"
	}
	if v, ok := opts.Get("bridge-stp"); ok {
		bd.STP = v == "on" || v == "yes"
	}
	if v, ok := opts.Get("bridge-vids"); ok {
		bd.VLANs = parseVidRangesText(v)
	}
	return bd
}

// convertFixtureFDB converts pvemock's fixture-declared FDB entries
// (T-306) to this package's FDBEntry shape. Unlike the rest of
// BridgeDetail (see the FixtureReader doc comment above), FDB *is*
// fixture-modelable: docs/features/lldp-discovery.md §4's MAC/FDB browser
// needs deterministic fixture data to test against, and a bridge's
// forwarding table is naturally expressed as declared fixture data (unlike
// e.g. live VLAN-table dumps) since it's just "these MACs were learned on
// these ports" — nil (not empty) when the fixture declares no fdb entries,
// matching the zero-value convention every other optional BridgeDetail
// field already follows.
func convertFixtureFDB(fdb []pvemock.FDBEntry) []FDBEntry {
	if len(fdb) == 0 {
		return nil
	}
	out := make([]FDBEntry, len(fdb))
	for i, e := range fdb {
		out[i] = FDBEntry{
			Mac: e.Mac, Port: e.Port, Vlan: e.Vlan,
			Master: e.Master, Permanent: e.Permanent, Stale: e.Stale,
		}
	}
	return out
}

// parseVidRangesText parses a bridge-vids-style option value ("2-4094",
// "10,20,30", "2-100,200") into VidRanges.
func parseVidRangesText(s string) []VidRange {
	var out []VidRange
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			l, errL := strconv.Atoi(lo)
			h, errH := strconv.Atoi(hi)
			if errL == nil && errH == nil {
				out = append(out, VidRange{Low: l, High: h})
			}
			continue
		}
		if v, err := strconv.Atoi(part); err == nil {
			out = append(out, VidRange{Low: v, High: v})
		}
	}
	return out
}

// fixtureVlanInfo recovers a VLAN sub-interface's VID and parent device.
// The rendered fixture interfaces(5) text carries vlan-raw-device but not
// a vlan-id option (PVE's own render path only ever needs the interface
// name, "parent.VID", to convey the VID), so the VID itself is parsed
// from that Debian-standard naming convention.
func fixtureVlanInfo(name string, opts *Entry) (vid int, parent string) {
	if opts != nil {
		if v, ok := opts.Get("vlan-raw-device"); ok {
			parent = v
		}
	}
	if idx := strings.LastIndexByte(name, '.'); idx >= 0 {
		if v, err := strconv.Atoi(name[idx+1:]); err == nil {
			vid = v
			if parent == "" {
				parent = name[:idx]
			}
		}
	}
	return vid, parent
}
