// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"embed"
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// datasetFS is the checked-in demo corpus. Embedded rather than read from
// testdata/ at run time for the obvious reason (a shipped binary has no
// repository next to it) and a less obvious one: an embedded fixture cannot
// be swapped for a different cluster by whoever runs the binary, so "this
// is the demo dataset" stays a true statement about a released build.
//
//go:embed dataset/cluster.yaml dataset/flows.yaml
var datasetFS embed.FS

const (
	// ClusterFixturePath / FlowsFixturePath are the dataset's paths inside
	// datasetFS. Exported so a caller that wants to read the corpus itself
	// (T-2802's tour script, a test using it as a corpus) names the same
	// files this package does rather than a second copy of the strings.
	ClusterFixturePath = "dataset/cluster.yaml"
	FlowsFixturePath   = "dataset/flows.yaml"

	// APIURL is the base URL a demo daemon's PVE clients are built with.
	//
	// `.invalid` is reserved by RFC 2606 and guaranteed never to resolve.
	// Nothing in demo mode dials it — Mode.Transport answers every request
	// in-process — but if a future code path ever bypassed that transport,
	// this is what it would try to reach, and "cannot resolve" is a much
	// better failure than "connected to whatever is on localhost:8006".
	APIURL = "http://demo-cluster.invalid"

	// TicketUsername / TicketPassword are the fixture's own built-in
	// superuser (see dataset/cluster.yaml's `users:`), used both by the
	// daemon's collectors and by an operator logging in to the demo UI.
	TicketUsername = "root@pam"
	TicketPassword = "vnprox-mock"
)

// Dataset is the loaded demo corpus: one pvemock fixture describing the
// synthetic cluster, and the flow samples replayed into the flow ring.
type Dataset struct {
	Fixture *pvemock.Fixture
	Flows   FlowCorpus
}

// LoadDataset parses and validates the embedded corpus.
//
// It returns an error rather than panicking even though the input is
// compile-time-embedded and therefore cannot vary at run time: the failure
// this guards is a bad edit to a checked-in fixture, and a test asserting
// that LoadDataset succeeds is a cheaper way to catch that than a panic in
// a released binary.
func LoadDataset() (*Dataset, error) {
	raw, err := datasetFS.ReadFile(ClusterFixturePath)
	if err != nil {
		return nil, fmt.Errorf("demo: reading embedded %s: %w", ClusterFixturePath, err)
	}
	var fx pvemock.Fixture
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	// Same strictness pvemock.LoadFixture applies: an unknown key is a typo
	// in a fixture nobody will otherwise notice, not an extension point.
	dec.KnownFields(true)
	if decErr := dec.Decode(&fx); decErr != nil {
		return nil, fmt.Errorf("demo: parsing embedded %s: %w", ClusterFixturePath, decErr)
	}
	if valErr := fx.Validate(); valErr != nil {
		return nil, fmt.Errorf("demo: validating embedded %s: %w", ClusterFixturePath, valErr)
	}

	flowsRaw, err := datasetFS.ReadFile(FlowsFixturePath)
	if err != nil {
		return nil, fmt.Errorf("demo: reading embedded %s: %w", FlowsFixturePath, err)
	}
	flows, err := parseFlowCorpus(flowsRaw)
	if err != nil {
		return nil, err
	}

	return &Dataset{Fixture: &fx, Flows: flows}, nil
}

// Mode is one demo daemon's synthetic world: the in-process PVE server, the
// transport that reaches it without a socket, and the fixture-backed host
// reader. Built once at startup by New and shared by every client the
// daemon constructs.
type Mode struct {
	dataset *Dataset
	server  *pvemock.Server
	host    *host.FixtureReader
}

// New loads the embedded dataset and builds the demo world around it.
func New(logger *slog.Logger) (*Mode, error) {
	ds, err := LoadDataset()
	if err != nil {
		return nil, err
	}
	var opts []pvemock.Option
	if logger != nil {
		opts = append(opts, pvemock.WithLogger(logger))
	}
	srv := pvemock.NewServer(ds.Fixture, opts...)
	return &Mode{
		dataset: ds,
		server:  srv,
		host:    host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
	}, nil
}

// Dataset returns the loaded corpus.
func (m *Mode) Dataset() *Dataset { return m.dataset }

// HostReader is the fixture-backed host.Reader a demo daemon's collectors
// use instead of host.NewReal(). Returning the concrete type rather than
// the interface keeps the "this is a fixture, not your machine" fact
// visible at the call site.
func (m *Mode) HostReader() *host.FixtureReader { return m.host }

// ClusterName is the synthetic cluster's name, for log lines and banners.
func (m *Mode) ClusterName() string { return m.dataset.Fixture.Cluster.Name }
