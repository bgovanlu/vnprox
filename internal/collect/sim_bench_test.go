package collect_test

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/perfbudget"
	"github.com/bgovanlu/vnprox/internal/sim"
)

// benchFixture is the largest SDN fixture (EVPN zone, five zone types, DHCP
// subnets) — the stress target the card names for the 10k-sim benchmark.
const benchFixture = "../../testdata/clusters/evpn-lab.yaml"

// perfSite is this file's own path, as perf/budgets.json spells it. It is what
// ties the budgets in that file to the code that measures them: every budget
// naming this site must be measured here, and perfbudget.Missing fails the
// test if one stops being.
const perfSite = "internal/collect/sim_bench_test.go"

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

// engineBuildsPerSample is how many sim.NewEngine calls one sample of
// sim.engine_build_us aggregates. One build is ~0.16 ms on the reference host,
// where the clock's resolution and a single GC cycle are most of what gets
// measured; a hundred of them is a number that means something.
const engineBuildsPerSample = 100

// TestPerfBudgets_Sim is T-2506's Go-side measurement site.
//
// It replaces TestSim_10kUnder5s, which asserted T-503 AC4's product bar of
// "10k simulations in well under 5s" against a workload that actually takes
// ~75 ms — 67x of headroom, i.e. an assertion that could only ever have caught
// a catastrophe. The bar itself has not been dropped: the budget below is 150
// ms, which clears T-503's 5 s by 33x while being able to see a 2x regression.
//
// Every number it enforces comes from perf/budgets.json, which is also what
// docs/performance.md section 13 states and what web/e2e/scale.spec.ts reads
// for the browser-side budgets. The report is printed on every run, passing or
// not (AC5), because a budget sitting at 4% headroom is the thing worth
// knowing before it breaks.
func TestPerfBudgets_Sim(t *testing.T) {
	file, err := perfbudget.LoadRepo()
	if err != nil {
		t.Fatalf("loading performance budgets: %v", err)
	}
	machine, err := perfbudget.Detect(file)
	if err != nil {
		t.Fatalf("calibrating this machine: %v", err)
	}

	snap := convergedSnapshot(t, benchFixture)
	reqs := randomRequests(snap, 10000)

	var results []perfbudget.Result

	build, err := file.ByID("sim.engine_build_us")
	if err != nil {
		t.Fatalf("%v", err)
	}
	buildResult, err := perfbudget.Measure(build, machine, func(int) (float64, error) {
		start := time.Now()
		for i := 0; i < engineBuildsPerSample; i++ {
			e := sim.NewEngine(sim.Input{Inventory: snap})
			runtime.KeepAlive(e)
		}
		return float64(time.Since(start).Microseconds()) / engineBuildsPerSample, nil
	})
	if err != nil {
		t.Fatalf("measuring %s: %v", build.ID, err)
	}
	results = append(results, buildResult)

	wall, err := file.ByID("sim.simulate_10k_wall_ms")
	if err != nil {
		t.Fatalf("%v", err)
	}
	engine := sim.NewEngine(sim.Input{Inventory: snap})
	// One discarded pass: the first run over a fresh engine pays for cold
	// instruction cache and a heap that has not reached its working set, and a
	// first-sample-inflated median would report a regression that is not there.
	for _, r := range reqs {
		_ = engine.Simulate(r)
	}
	wallResult, err := perfbudget.Measure(wall, machine, func(sample int) (float64, error) {
		// No-op unless this binary was built with the `perfslow` tag; see
		// perfslow_off_test.go.
		perfSlowConfigure(t, sample)
		start := time.Now()
		for _, r := range reqs {
			_ = engine.Simulate(r)
		}
		return float64(time.Since(start).Microseconds()) / 1000, nil
	})
	if err != nil {
		t.Fatalf("measuring %s: %v", wall.ID, err)
	}
	results = append(results, wallResult)

	t.Logf("\n%s", perfbudget.Report(results, machine))

	// A budget that quietly stops being measured reads exactly like a budget
	// that passes. Same failure shape as a spec file in no shard (T-2505).
	if err := perfbudget.Missing(file.ForSite(perfSite), results); err != nil {
		t.Errorf("%v", err)
	}
	if err := perfbudget.Check(results); err != nil {
		t.Errorf("%v", err)
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
