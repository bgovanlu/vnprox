package inventory

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentPollersAndReaders is acceptance criterion #1: 4 concurrent
// pollers (from different sources, two of them observing the SAME real-world
// objects to exercise cross-source merge) plus 8 concurrent snapshot readers
// run continuously. Intended to be run under `go test -race`. Zero races,
// zero panics.
//
// Duration defaults to 3s (kept short so `go test ./...` stays fast) and is
// 500ms under -short; set INVENTORY_STRESS_SECONDS to run the full 30s soak
// the acceptance criterion calls for.
func TestConcurrentPollersAndReaders(t *testing.T) {
	dur := 3 * time.Second
	if testing.Short() {
		dur = 500 * time.Millisecond
	}
	if s := os.Getenv("INVENTORY_STRESS_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			dur = time.Duration(n) * time.Second
		}
	}

	g := NewGraph()
	base := buildScaleModel()
	base.applyAll(g) // seed so readers always see a populated graph

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	var wg sync.WaitGroup
	var applyCount, snapCount int64

	// Poller helper: repeatedly ApplyPoll with an iteration-varying field so
	// deltas and rebuilds actually happen.
	poller := func(source Source, scope Scope, gen func(i int64) []Entity) {
		defer wg.Done()
		var i int64
		for ctx.Err() == nil {
			g.ApplyPoll(source, scope, gen(i))
			atomic.AddInt64(&applyCount, 1)
			i++
		}
	}

	node0, node1 := base.nodes[0], base.nodes[1]

	// P1: host-netlink runtime view of node0's L2 (mutating MTU + link).
	wg.Add(1)
	go poller(SourceHostNetlink, Scope{Node: node0}, func(i int64) []Entity {
		return []Entity{
			&PhysNic{Ref: Ref{Kind: KindPhysNic, Node: node0, ID: "eno1"}, Name: "eno1", MTU: 1500 + int(i%2), LinkUp: i%2 == 0},
			&Bridge{Ref: Ref{Kind: KindBridge, Node: node0, ID: "vmbr0"}, Name: "vmbr0", Virt: BridgeLinux, PortNames: []string{"bond0"}, MTU: 1500 + int(i%3), VlanAware: true},
			&Bond{Ref: Ref{Kind: KindBond, Node: node0, ID: "bond0"}, Name: "bond0", Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, ActiveSlave: pick2("eno1", "eno2", i)},
		}
	})

	// P2: PVE declared view of the SAME node0 objects (cross-source merge on
	// the same Refs, concurrently with P1).
	wg.Add(1)
	go poller(SourcePVENetwork, Scope{Node: node0}, func(i int64) []Entity {
		return []Entity{
			&Bridge{Ref: Ref{Kind: KindBridge, Node: node0, ID: "vmbr0"}, Name: "vmbr0", Virt: BridgeLinux, MTUDeclared: 9000, Comments: "c" + strconv.FormatInt(i%4, 10)},
			&PhysNic{Ref: Ref{Kind: KindPhysNic, Node: node0, ID: "eno1"}, Name: "eno1", MTUDeclared: 1500},
		}
	})

	// P3: host-netlink for a different node.
	wg.Add(1)
	go poller(SourceHostNetlink, Scope{Node: node1}, func(i int64) []Entity {
		return []Entity{
			&Bridge{Ref: Ref{Kind: KindBridge, Node: node1, ID: "vmbr0"}, Name: "vmbr0", Virt: BridgeLinux, MTU: 1500, VlanAware: i%2 == 0},
		}
	})

	// P4: guests cluster-wide, attaching to node0/node1 bridges & VNets
	// (attachment resolution under concurrency).
	wg.Add(1)
	go poller(SourcePVEGuest, Scope{Kinds: []Kind{KindGuest, KindGuestNic}}, func(i int64) []Entity {
		var es []Entity
		for k := 0; k < 20; k++ {
			node := base.nodes[k%2]
			vmid := strconv.Itoa(200 + k)
			es = append(es,
				&Guest{Ref: Ref{Kind: KindGuest, Node: node, ID: vmid}, VMID: 200 + k, Node: node, Status: pick2("running", "stopped", i)},
				&GuestNic{Ref: Ref{Kind: KindGuestNic, Node: node, ID: vmid + "/net0"}, Guest: Ref{Kind: KindGuest, Node: node, ID: vmid}, Key: "net0", TargetName: "vmbr0", Vid: int(10 + i%5)},
			)
		}
		return es
	})

	// 8 readers: snapshot + full traversal, asserting basic invariants.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				s := g.Snapshot()
				for ref := range mapKeysOf(s) {
					e, ok := s.Get(ref)
					if !ok {
						t.Errorf("Get(%v) missing though listed", ref)
						return
					}
					if e.GetRef() != ref {
						t.Errorf("entity ref mismatch: key %v, entity %v", ref, e.GetRef())
						return
					}
					_ = e.fieldMap()
					_, _ = s.Provenance(ref)
					_ = s.EdgesOf(ref)
				}
				_ = s.Edges()
				atomic.AddInt64(&snapCount, 1)
			}
		}()
	}

	wg.Wait()
	t.Logf("stress: dur=%v applies=%d snapshots=%d entities=%d",
		dur, atomic.LoadInt64(&applyCount), atomic.LoadInt64(&snapCount), g.Snapshot().Len())
	if applyCount == 0 || snapCount == 0 {
		t.Fatal("stress test did no work")
	}
}

func pick2(a, b string, i int64) string {
	if i%2 == 0 {
		return a
	}
	return b
}

// mapKeysOf returns the set of Refs in a snapshot (readers use only exported
// accessors; this iterates All() to get keys without touching internals).
func mapKeysOf(s Snapshot) map[Ref]struct{} {
	out := make(map[Ref]struct{}, s.Len())
	for _, e := range s.All() {
		out[e.GetRef()] = struct{}{}
	}
	return out
}
