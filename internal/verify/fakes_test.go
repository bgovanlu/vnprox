package verify

// fakes_test.go builds the healthy baseline every negative case in
// checks_test.go is a single mutation away from.
//
// The baseline matters as much as the mutations. A "this check fails on
// broken input" test proves nothing on its own — a check that always fails
// passes it — so every case below is asserted against a fixture in which the
// same check passes, and TestHealthyClusterPassesEverything is the control
// that keeps that true.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- fake probes -------------------------------------------------------------

type fakeResponse struct {
	err    error
	body   string
	status int
}

// fakeDaemon serves a path -> response map.
//
// An unmapped path deliberately 404s rather than returning an empty success:
// a fake that invents a plausible answer for a route nobody wired up is the
// same defect internal/pvemock's replay server refuses to commit, for the
// same reason.
type fakeDaemon struct {
	responses map[string]fakeResponse
	// rootResponses backs GetRoot (RootProbe, checks_pwa.go): SPA-surface
	// paths outside /api/v1, with headers. An unmapped path 404s, same
	// discipline as responses.
	rootResponses map[string]fakeRootResponse
	// seq lets one path answer differently on successive reads, which is how
	// a state transition (a standby promoting, a changeset rolling back) is
	// modelled without real time passing. The last entry sticks.
	seq   map[string][]string
	posts []string
}

func (f *fakeDaemon) Get(_ context.Context, path string) (int, []byte, error) {
	if bodies, ok := f.seq[path]; ok && len(bodies) > 0 {
		body := bodies[0]
		if len(bodies) > 1 {
			f.seq[path] = bodies[1:]
		}
		return 200, []byte(body), nil
	}
	r, ok := f.responses[path]
	if !ok {
		return 404, []byte(`{"error":{"code":"not_found","message":"` + path + ` is not in the fixture"}}`), nil
	}
	if r.err != nil {
		return 0, nil, r.err
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	return status, []byte(r.body), nil
}

// fakeRootResponse is one GetRoot answer: status, selected headers, body.
type fakeRootResponse struct {
	header map[string]string
	err    error
	body   string
	status int
}

func (f *fakeDaemon) GetRoot(_ context.Context, path string) (int, http.Header, []byte, error) {
	r, ok := f.rootResponses[path]
	if !ok {
		return 404, http.Header{}, []byte(path + " is not in the root fixture"), nil
	}
	if r.err != nil {
		return 0, nil, nil, r.err
	}
	h := http.Header{}
	for k, v := range r.header {
		h.Set(k, v)
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	return status, h, []byte(r.body), nil
}

func (f *fakeDaemon) Post(_ context.Context, path string, _ any) (int, []byte, error) {
	f.posts = append(f.posts, path)
	r, ok := f.responses["POST "+path]
	if !ok {
		return 200, []byte(`{"id":"cs-1","status":"staged"}`), nil
	}
	if r.err != nil {
		return 0, nil, r.err
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	return status, []byte(r.body), nil
}

func (f *fakeDaemon) set(path, body string) { f.responses[path] = fakeResponse{body: body} }

type fakeCluster struct {
	ifaces map[string][]Iface
	// afterRollback, when it holds a node, is what that node's *second* read
	// returns. It exists to model the one failure a single-node fixture
	// cannot produce: a distributed rollback that restored one node and left
	// the other in its applied state.
	afterRollback map[string][]Iface
	reads         map[string]int
	nodesErr      error
	ifacesErr     error
	versionErr    error
	version       string
	nodes         []Node
}

func (f *fakeCluster) Nodes(context.Context) ([]Node, error) { return f.nodes, f.nodesErr }
func (f *fakeCluster) PVEVersion(context.Context) (string, error) {
	return f.version, f.versionErr
}
func (f *fakeCluster) Interfaces(_ context.Context, node string) ([]Iface, error) {
	if f.ifacesErr != nil {
		return nil, f.ifacesErr
	}
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[node]++
	if later, ok := f.afterRollback[node]; ok && f.reads[node] > 1 {
		return later, nil
	}
	return f.ifaces[node], nil
}

type fakeHost struct {
	cmds  map[string]string
	errs  map[string]error
	files map[string]string
	// fileSeq is files' equivalent of fakeDaemon.seq: successive reads of one
	// path see successive values, which is how a VF count moving under a
	// provision is modelled.
	fileSeq map[string][]string
	ran     []string
}

func cmdKey(name string, args ...string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

func (f *fakeHost) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	key := cmdKey(name, args...)
	f.ran = append(f.ran, key)
	if err, ok := f.errs[key]; ok {
		return f.cmds[key], err
	}
	out, ok := f.cmds[key]
	if !ok {
		return "", fmt.Errorf("fake host: %q is not on PATH", name)
	}
	return out, nil
}

func (f *fakeHost) ReadFile(_ context.Context, _ string, path string) ([]byte, error) {
	if err, ok := f.errs["file:"+path]; ok {
		return nil, err
	}
	if values, ok := f.fileSeq[path]; ok && len(values) > 0 {
		value := values[0]
		if len(values) > 1 {
			f.fileSeq[path] = values[1:]
		}
		return []byte(value), nil
	}
	raw, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("fake host: no such file %s", path)
	}
	return []byte(raw), nil
}

// --- the healthy baseline ------------------------------------------------------

const (
	fixtureNode  = "pve1"
	fixturePeer  = "pve2"
	fixtureNodeA = "10.10.0.11"
)

func fixtureNodes() []Node {
	return []Node{
		{Name: fixtureNode, Address: fixtureNodeA, Online: true, Local: true},
		{Name: fixturePeer, Address: "10.10.0.12", Online: true},
	}
}

func healthyCluster() *fakeCluster {
	bond := Iface{Name: "bond0", Type: "bond", Method: "manual", BondMode: "802.3ad", Slaves: "eno1 eno2", MTU: 1500, Autostart: true}
	bridge := Iface{Name: "vmbr0", Type: "bridge", Method: "static", Address: fixtureNodeA + "/24", Comments: "mgmt", MTU: 1500, VlanAware: true, Autostart: true}
	eno1 := Iface{Name: "eno1", Type: "eth", Method: "manual", MTU: 1500, Autostart: true}
	return &fakeCluster{
		nodes:   fixtureNodes(),
		version: "cluster pve-cluster-a",
		ifaces: map[string][]Iface{
			fixtureNode: {eno1, bond, bridge},
			fixturePeer: {eno1, bond, bridge},
		},
	}
}

func healthyDaemon() *fakeDaemon {
	now := fixtureNow().Unix()
	return &fakeDaemon{responses: map[string]fakeResponse{
		"/changesets":            {body: `{"items":[{"id":"cs-9","status":"committed","nodes":["pve1"]}]}`},
		"/changesets/cs-9/diff":  {body: `{"steps":[{"op":"bridge.update","node":"pve1"}]}`},
		"/changesets/cs-1":       {body: `{"id":"cs-1","status":"rolled_back"}`},
		"/config":                {body: `{"version":"3.0.4","confirmTimeoutDefaultSec":120}`},
		"/drift":                 {body: `{"items":[{"id":"drift:mtu_consistency|vmbr0","check":"mtu_consistency","severity":"warning","detail":"vmbr0 differs","nodes":["pve1","pve2"]}]}`},
		"/flows":                 {body: fmt.Sprintf(`{"items":[{"at":%d,"node":"pve1","srcIp":"10.10.0.5","dstIp":"10.10.0.9","proto":6,"bytes":4096,"packets":8,"source":"sflow"}]}`, now)},
		"/captures":              {body: `{"items":[{"id":"cap-1","sessions":[{"node":"pve1","iface":"vmbr0","state":"finished","packets":812,"bytes":91234}]}]}`},
		"/lldp":                  {body: `{"items":[{"node":"pve1","localIface":"eno1","protocol":"lldp","chassisName":"sw-core","chassisId":"00:11:22:33:44:55","portId":"Gi1/0/1","mgmtIps":["10.10.0.2"]}]}`},
		"/ipam/external-subnets": {body: `{"items":[{"id":"ext-1","cidr":"192.0.2.0/24","source":"netbox"},{"id":"ext-2","cidr":"198.51.100.0/24","source":"manual"}]}`},
		"/federation/clusters":   {body: `{"items":[{"id":"fc-1","name":"site-b","apiUrl":"https://pve-b:8007"}]}`},
		"/federation/topology": {body: `{"clusters":[{"id":"local","name":"site-a","nodes":[{"name":"pve1"},{"name":"pve2"}]},` +
			`{"id":"fc-1","name":"site-b","nodes":[{"name":"pveb1"}]}],"partial":false,"failedClusters":[]}`},
		"/federation/ipam/conflicts": {body: `{"items":[{"type":"cross_cluster_duplicate_subnet","severity":"warning","ips":["10.20.0.0/24"],` +
			`"message":"overlap","suggestion":"renumber","clusters":["site-a","site-b"]}],"partial":false,"failedClusters":[]}`},
		"/wireguard/tunnels": {body: fmt.Sprintf(`{"items":[{"id":"wg-1","node":"pve1","ifName":"wg0","publicKey":"AAAA","listenPort":51820,`+
			`"addresses":["10.99.0.1/24"],"mtu":1420,"status":{"interfaceUp":true,"peerCount":1},`+
			`"peers":[{"publicKey":"BBBB","endpoint":"203.0.113.7:51820","allowedIps":["10.99.0.2/32"],"external":false,"lastHandshakeUnix":%d,"rxBytes":9001,"txBytes":42,"endpointDrifted":false}]}]}`, now-30)},
		"/ha/status": {body: fmt.Sprintf(`{"role":"active","term":7,"leaseExpiresAt":%d,"replicationLag":2,"replicationDegraded":false}`, now+60)},
		"/certs": {body: `{"inventory":{"scannedAt":"2026-08-10T00:00:00Z","clusterCA":{"subject":"pve-root-ca"},` +
			`"certificates":[{"path":"/etc/pve/nodes/pve1/pve-ssl.pem","node":"pve1"},{"path":"/etc/pve/nodes/pve2/pve-ssl.pem","node":"pve2"}],"errors":[]},"issues":[]}`},
	}, rootResponses: map[string]fakeRootResponse{
		// The SPA surface a real daemon serves (checks_pwa.go): the shell
		// with the T-2901 CSP, the manifest with its registered media type,
		// and the service worker script.
		"/": {header: map[string]string{
			"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; worker-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'",
		}, body: "<!doctype html>"},
		"/manifest.webmanifest": {header: map[string]string{"Content-Type": "application/manifest+json"}, body: `{"name":"vnprox","start_url":"/"}`},
		"/sw.js":                {header: map[string]string{"Content-Type": "text/javascript; charset=utf-8"}, body: "// sw"},
	}}
}

const fixtureSessionKey = "c2Vzc2lvbi1rZXktZm9yLXRlc3RzLW9ubHktbm90LXJlYWw="

// healthyBonding is a real-shaped /proc/net/bonding/bond0 for an 802.3ad bond
// that has negotiated with a switch.
const healthyBonding = `Ethernet Channel Bonding Driver: v6.12.0

Bonding Mode: IEEE 802.3ad Dynamic link aggregation
802.3ad info
LACP active: on
LACP rate: fast

Slave Interface: eno1
MII Status: up
details actor lacp pdu:
    system priority: 65535
    system mac address: aa:bb:cc:dd:ee:01
    port key: 15
details partner lacp pdu:
    system priority: 32768
    system mac address: 00:11:22:33:44:55
    oper key: 1001
    port state: 61
`

func healthyHost() *fakeHost {
	const sriovListing = "/sys/class/net/eno1/device/sriov_totalvfs=8\n/sys/class/net/eno2/device/sriov_totalvfs=8\n"
	return &fakeHost{
		cmds: map[string]string{
			"uname -r": "6.12.0-1-pve\n",
			"sh -c for f in /sys/class/net/*/device/sriov_totalvfs; do [ -e \"$f\" ] && echo \"$f=$(cat $f)\"; done": sriovListing,
			"sh -c ls -1 /etc/pve/nodes/*/pve-ssl.pem 2>/dev/null":                                                   "/etc/pve/nodes/pve1/pve-ssl.pem\n/etc/pve/nodes/pve2/pve-ssl.pem\n",
			"sh -c for d in /sys/class/net/*/device/; do n=${d%/device/}; n=${n##*/}; m=$(cat $d/modalias 2>/dev/null); v=$(cat $d/vendor 2>/dev/null); p=$(cat $d/device 2>/dev/null); echo \"$n $v:$p $m\"; done": "eno1 0x8086:0x1572 pci:v00008086d00001572\n",
			"vnproxctl backup -o json":                   `{"path":"/var/lib/vnprox/backups/b.tar.gz","bytes":652902,"node":"pve1","schemaVersion":48,"includesKeyMaterial":false,"secretClasses":[],"entries":3,"pruned":[],"warnings":[]}`,
			"vnproxctl support-bundle --dry-run -o json": `{"node":"pve1","entries":["env.txt","config.toml","changesets.json"],"redacted":true}`,
			"vnproxctl --version":                        "vnproxctl 3.0.4\n",
			"vnproxctl doctor -o json":                   `{"results":[{"check":"config","status":"pass","detail":"ok"},{"check":"pmxcfs","status":"pass","detail":"ok"}],"summary":{"pass":2,"warn":0,"fail":0,"skip":0}}`,
			"openssl verify -CAfile /etc/pve/pve-root-ca.pem /etc/pve/local/pve-ssl.pem": "/etc/pve/local/pve-ssl.pem: OK\n",
			"openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -ext subjectAltName":     "X509v3 Subject Alternative Name:\n    DNS:pve1, DNS:pve1.localdomain, IP Address:" + fixtureNodeA + "\n",
			"systemctl stop vnprox":  "",
			"systemctl start vnprox": "",
		},
		files: map[string]string{
			"/proc/net/bonding/bond0":                 healthyBonding,
			"/etc/pve/pve-root-ca.pem":                "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n",
			"/etc/vnprox/vnprox.toml":                 "[server]\nlisten = \"0.0.0.0:8007\"\n\n[switches]\nenabled = true\n",
			"/etc/vnprox/keys/session.key":            fixtureSessionKey,
			"/sys/class/net/eno1/device/sriov_numvfs": "0\n",
		},
	}
}

func fixtureNow() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) }

// advancingClock returns a Now that moves forward a fixed step per call, so
// the polling checks terminate deterministically under test without any real
// time passing.
func advancingClock(start time.Time, step time.Duration) func() time.Time {
	current := start
	return func() time.Time {
		out := current
		current = current.Add(step)
		return out
	}
}

// healthyDeps is the all-good cluster: two online nodes, a daemon answering
// every documented route with a real-shaped body, and a host whose files and
// commands say what a correctly-configured PVE node's would.
func healthyDeps() Deps {
	return Deps{
		Now:     func() time.Time { return fixtureNow() },
		Wait:    func(context.Context, time.Duration) error { return nil },
		Cluster: healthyCluster(),
		Daemon:  healthyDaemon(),
		Host:    healthyHost(),
		Nodes:   fixtureNodes(),
		Consent: Consent{AllowMock: false, Destructive: false},
	}
}

// destructiveDeps is healthyDeps plus the consent and the write client the
// destructive suite needs, with the polling clock wound forward.
func destructiveDeps() Deps {
	d := healthyDeps()
	daemon, _ := d.Daemon.(*fakeDaemon)
	d.Mutator = daemon
	d.Consent.Destructive = true
	d.Now = advancingClock(fixtureNow(), time.Second)

	// Two state transitions the destructive suite exists to observe, modelled
	// as successive reads rather than as real elapsed time.
	//
	// A VF that was provisioned moves the kernel's counter; a standby that
	// was promoted reports a higher term. Both fixtures are deliberately
	// "the transition happened", so the mutation that removes each of them
	// (checks_test.go) is what proves the check can see its absence.
	host, _ := d.Host.(*fakeHost)
	host.fileSeq = map[string][]string{
		"/sys/class/net/eno1/device/sriov_numvfs": {"0\n", "4\n"},
	}
	daemon.seq = map[string][]string{
		"/ha/status": {
			fmt.Sprintf(`{"role":"active","term":7,"leaseExpiresAt":%d,"replicationLag":2,"replicationDegraded":false}`, fixtureNow().Unix()+60),
			fmt.Sprintf(`{"role":"active","term":8,"leaseExpiresAt":%d,"replicationLag":1,"replicationDegraded":false}`, fixtureNow().Unix()+120),
		},
	}
	return d
}

// discardLog is the logger the check-level tests use: a check's slog output
// is not what is under test, and a wall of it hides the assertion failures
// that are.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// runCheck runs one registered check by id against deps.
func runCheck(t *testing.T, id string, deps Deps) Result {
	t.Helper()
	for _, c := range Checks() {
		if c.ID != id {
			continue
		}
		if deps.Now == nil {
			deps.Now = func() time.Time { return fixtureNow() }
		}
		out := runOne(context.Background(), c, deps, discardLog())
		res := Result{
			ID:           c.ID,
			MatrixRow:    c.MatrixRow,
			Area:         c.Area,
			Suite:        c.Suite,
			Precondition: c.Precondition,
			Status:       out.Status,
			Detail:       out.Detail,
			Evidence:     out.Evidence,
		}
		if out.Status == StatusSkip {
			res.SkipReason = out.Reason
		}
		return res
	}
	t.Fatalf("no check registered with id %q; registered: %s", id, strings.Join(CheckIDs(Checks()), ", "))
	return Result{}
}

func sortedIDs() []string {
	ids := CheckIDs(Checks())
	sort.Strings(ids)
	return ids
}
