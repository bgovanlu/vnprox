package collect_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/sim"
)

// benchFixture is the largest SDN fixture (EVPN zone, five zone types, DHCP
// subnets) — the stress target the card names for the 10k-sim benchmark.
const benchFixture = "../../testdata/clusters/evpn-lab.yaml"

// randomRequests builds a fixed pool of simulation requests over a fixture's
// endpoints: guest↔guest, guest↔external, and IP-literal endpoints.
func randomRequests(snap inventory.Snapshot, n int) []sim.Request {
	nics := guestNics(snap)
	protos := []string{"tcp", "udp", "icmp"}
	ports := []int{22, 80, 443, 53, 3306, 8080, 0}
	rng := rand.New(rand.NewSource(42))

	endpoint := func() sim.Endpoint {
		switch rng.Intn(3) {
		case 0:
			if len(nics) > 0 {
				return sim.Endpoint{Kind: sim.EndpointGuestNic, NicRef: nics[rng.Intn(len(nics))].GetRef()}
			}
			return sim.Endpoint{Kind: sim.EndpointExternal}
		case 1:
			return sim.Endpoint{Kind: sim.EndpointIP, IP: fmt.Sprintf("192.168.50.%d", rng.Intn(255))}
		default:
			return sim.Endpoint{Kind: sim.EndpointExternal}
		}
	}
	reqs := make([]sim.Request, n)
	for i := range reqs {
		reqs[i] = sim.Request{Src: endpoint(), Dst: endpoint(), Proto: protos[rng.Intn(len(protos))], Port: ports[rng.Intn(len(ports))]}
	}
	return reqs
}

// TestSim_10kUnder5s is AC4's throughput bar: 10k random simulations on the
// largest fixture complete in well under 5s. Engine construction (index
// build) happens once; the timed loop is pure Simulate calls.
func TestSim_10kUnder5s(t *testing.T) {
	snap := convergedSnapshot(t, benchFixture)
	engine := sim.NewEngine(sim.Input{Inventory: snap})
	reqs := randomRequests(snap, 10000)

	start := time.Now()
	for _, r := range reqs {
		_ = engine.Simulate(r)
	}
	elapsed := time.Since(start)
	t.Logf("10000 simulations in %s (%.1f k sims/s)", elapsed, 10.0/elapsed.Seconds())
	if elapsed > 5*time.Second {
		t.Fatalf("10k simulations took %s, want < 5s", elapsed)
	}
}

// BenchmarkSimulate reports per-simulation cost over the largest fixture.
func BenchmarkSimulate(b *testing.B) {
	snap := convergedSnapshot(b, benchFixture)
	engine := sim.NewEngine(sim.Input{Inventory: snap})
	reqs := randomRequests(snap, 1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.Simulate(reqs[i%len(reqs)])
	}
}
