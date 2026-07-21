package microseg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// nasGuestRef / nasRulesetRef are the fixture NAS guest and its firewall
// ruleset. The guest ref is what flow SrcRef/DstRef carry; the ruleset ref is
// the "guest/<kind>/<vmid>" target Stage emits ops against.
func nasSubject() Subject {
	return Subject{
		GuestRef:   inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"},
		RulesetRef: GuestRulesetRef("pve1", "qemu", "100"),
	}
}

const (
	// baseEpoch is 2024-01-01T00:00:00Z, hour-aligned (matches T-1601's own
	// corpora), so wall-clock hour buckets fall cleanly.
	baseEpoch  = int64(1704067200)
	daySeconds = int64(86400)
)

// nasCorpus builds a spike-free, anomaly-free 14-day NAS-guest flow corpus:
// two inbound services (SMB tcp/445, NFS tcp/2049) from many distinct clients
// inside 10.0.0.0/24, and two outbound services (DNS udp/53, HTTPS tcp/443) to
// fixed peers. Every day places one flow per service at the same hour with
// identical bytes, so no wall-clock hour ever spikes against its own
// hour-of-day baseline — Detect over this corpus's own training window raises
// nothing (the property T-1601 AC1 guarantees, reproduced here so the NAS
// golden's observed-good set is the whole corpus).
func nasCorpus() []flow.Record {
	const guest = "guest:pve1:100"
	var recs []flow.Record
	for d := int64(0); d < 14; d++ {
		at := baseEpoch + d*daySeconds + 12*3600
		clientSMB := clientIP(10 + int(d)) // 10.0.0.10 .. 10.0.0.23
		clientNFS := clientIP(30 + int(d)) // 10.0.0.30 .. 10.0.0.43
		recs = append(recs,
			// inbound: clients -> NAS service ports
			flow.Record{Node: "pve1", SrcIP: clientSMB, DstIP: "10.0.0.5", DstRef: guest, Source: flow.SourceConntrack, At: at, Bytes: 1000, Packets: 1, SrcPort: 40000, DstPort: 445, Proto: 6},
			flow.Record{Node: "pve1", SrcIP: clientNFS, DstIP: "10.0.0.5", DstRef: guest, Source: flow.SourceConntrack, At: at + 1, Bytes: 1000, Packets: 1, SrcPort: 40001, DstPort: 2049, Proto: 6},
			// outbound: NAS -> upstream services
			flow.Record{Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.20", SrcRef: guest, Source: flow.SourceConntrack, At: at + 2, Bytes: 1000, Packets: 1, SrcPort: 40002, DstPort: 53, Proto: 17},
			flow.Record{Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.30", SrcRef: guest, Source: flow.SourceConntrack, At: at + 3, Bytes: 1000, Packets: 1, SrcPort: 40003, DstPort: 443, Proto: 6},
		)
	}
	return recs
}

// nasHeldout is a separate day's corpus: the same four legitimate services
// (which must dry-run wouldAllow against a policy learned from nasCorpus) plus
// exactly two flows to services never observed in training (a new outbound
// MySQL to a new subnet, a new inbound HTTP-alt from a new subnet), which must
// dry-run wouldBlock — the bounded, traceable would-have-blocked tail AC3 asks
// for.
func nasHeldout() []flow.Record {
	const guest = "guest:pve1:100"
	at := baseEpoch + 20*daySeconds + 12*3600
	return []flow.Record{
		// legitimate, covered
		{Node: "pve1", SrcIP: clientIP(50), DstIP: "10.0.0.5", DstRef: guest, Source: flow.SourceConntrack, At: at, Bytes: 1000, Packets: 1, DstPort: 445, Proto: 6},
		{Node: "pve1", SrcIP: clientIP(51), DstIP: "10.0.0.5", DstRef: guest, Source: flow.SourceConntrack, At: at + 1, Bytes: 1000, Packets: 1, DstPort: 2049, Proto: 6},
		{Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.20", SrcRef: guest, Source: flow.SourceConntrack, At: at + 2, Bytes: 1000, Packets: 1, DstPort: 53, Proto: 17},
		{Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.30", SrcRef: guest, Source: flow.SourceConntrack, At: at + 3, Bytes: 1000, Packets: 1, DstPort: 443, Proto: 6},
		// NEVER-observed: must be wouldBlock
		{Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.9.0.5", SrcRef: guest, Source: flow.SourceConntrack, At: at + 4, Bytes: 1000, Packets: 1, DstPort: 3306, Proto: 6},
		{Node: "pve1", SrcIP: "10.8.0.5", DstIP: "10.0.0.5", DstRef: guest, Source: flow.SourceConntrack, At: at + 5, Bytes: 1000, Packets: 1, DstPort: 8080, Proto: 6},
	}
}

func clientIP(last int) string {
	return "10.0.0." + itoa(last)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// loadT1601Corpus reads one of T-1601's shipped flow-baseline corpora
// (internal/baseline/testdata), the reuse this card's fixture-family entry
// requires — the planner's tests read the SAME corpus format baseline detection
// does, never a re-derived copy.
func loadT1601Corpus(t *testing.T, name string) baseline.Corpus {
	t.Helper()
	path := filepath.Join("..", "baseline", "testdata", name)
	data, err := os.ReadFile(path) //nolint:gosec // fixed test fixture path
	if err != nil {
		t.Fatalf("reading T-1601 corpus %s: %v", name, err)
	}
	c, err := baseline.ParseCorpus(data)
	if err != nil {
		t.Fatalf("parsing T-1601 corpus %s: %v", name, err)
	}
	return c
}

// recentByClass returns the one Recent flow in a T-1601 corpus that the given
// injected anomaly class targets, guarded by the corpus's own Injected metadata
// so the reuse can't silently drift if T-1601 reorders its corpus.
func recentByClass(t *testing.T, c baseline.Corpus, class string) flow.Record {
	t.Helper()
	var subject string
	for _, inj := range c.Injected {
		if inj.Class == class {
			subject = inj.Subject
		}
	}
	if subject == "" {
		t.Fatalf("corpus %s has no injected anomaly of class %s", c.Name, class)
	}
	for _, r := range c.Recent {
		switch class {
		case "new_port":
			if (baseline.PortKey{Proto: r.Proto, Port: r.DstPort}).String() == subject {
				return r
			}
		case "new_subnet":
			if sn, ok := baseline.PeerSubnet(peerOf(r, c.Ref), DefaultSubnetPrefixV4, DefaultSubnetPrefixV6); ok && sn == subject {
				return r
			}
		case "volume_spike":
			if baseline.HourSubject(r.At) == subject {
				return r
			}
		}
	}
	t.Fatalf("corpus %s: no recent flow matching injected %s subject %q", c.Name, class, subject)
	return flow.Record{}
}

func peerOf(r flow.Record, ref string) string {
	if r.SrcRef == ref {
		return r.DstIP
	}
	return r.SrcIP
}

// ruleSig is a compact rule signature for golden assertions.
func ruleSig(r inventory.FwRule) string {
	return r.Direction + " " + r.Action + " " + r.Proto + " dport=" + r.Dport + " src=" + r.Source + " dst=" + r.Dest
}
