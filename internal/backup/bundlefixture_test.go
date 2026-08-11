package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// bundlefixture_test.go extends T-1901's seeded installation (seed_test.go's
// newFixture) with the four *files* a support bundle reads that a backup
// does not: an interfaces(5) file, a corosync.conf, a daemon log, and a
// richer vnprox.toml.
//
// The important design decision is where the secret markers go. T-1901's
// fixture seeds every SecretClass into the store, which is exactly right for
// a backup — a backup contains the store. A bundle does NOT contain the
// store, so scanning a bundle for those markers would pass trivially and
// prove nothing.
//
// So every marker that has a plausible home in something a bundle actually
// reads is planted there as well: the WireGuard private key into
// /etc/network/interfaces, the PVE token and session key into the log, an
// OIDC secret into vnprox.toml, a revert ticket and a federation credential
// into a changeset's ops. bundlePlantings records where each one went, and
// TestBundle_AC1_PlantingsReachWhatTheBundleReads fails if that list ever
// gets thin — because a leak test whose secrets are all somewhere the code
// never looks is decorative.

// bundleControlStore is the CONTROL marker for the store-derived
// collectors: an entirely non-secret changeset title that MUST appear in
// the bundle. If it does not, the changesets collector did not run or the
// scanner does not see its output, and every "secret not found" assertion
// is meaningless.
const bundleControlStore = "BUNDLECONTROL-changeset-title-must-be-findable"

// bundleControlHost is the same idea for the host-network collector: an
// allowlisted interfaces(5) option whose value must survive.
const bundleControlHost = "BUNDLECONTROL-alias-must-be-findable"

// bundleControlLog is the same idea for the log collector: an ordinary log
// line that must survive Scrub intact.
const bundleControlLog = "BUNDLECONTROL-log-line-must-be-findable"

// bundleControlConfig is the same idea for the config collector: an
// allowlisted config value that must survive.
const bundleControlConfig = "/etc/vnprox/BUNDLECONTROL-config-value-must-be-findable.pem"

// bundleControlIncident is the CONTROL marker for T-2804's incident export:
// an ordinary annotation body that MUST appear in the archive. If it does
// not, incident/timeline.json is not in the bundle (or is empty), and every
// "no secret in the incident export" assertion is worthless.
const bundleControlIncident = "BUNDLECONTROL-incident-annotation-must-be-findable"

// incidentPlantings records where each secret marker is planted inside the
// incident document, one per free-text field the export carries. The fields
// are exactly the ones a field allowlist cannot protect: an operator's own
// annotation body, a caveat quoting an error, a diff entry's cluster-supplied
// names.
//
// It is asserted against by TestBundleIncident_PlantingsReachTheDocument, for
// the same reason bundlePlantings is: an export leak test whose secrets are
// nowhere in the document is decorative.
var incidentPlantings = []planting{
	{"pve_ticket", "an operator's annotation body (a pasted ticket)"},
	{"webhook_secret", "the incident title"},
	{"session_key", "a source status detail"},
	{"k8s_kubeconfig", "a derived caveat"},
	{"switch_credential", "a diff entry's entity name"},
	{"federation_credential", "the diff refusal message"},
	{"revert_ticket", "a changeset event's summary"},
	{"wireguard_private_key", "a changed-field name on a diff entry"},
}

// planting records one secret marker deliberately written into something a
// bundle reads.
type planting struct {
	// ClassID is the SecretClass whose marker this is.
	ClassID string
	// Where is the human name of the input it was planted in.
	Where string
}

// bundlePlantings is the list, and it is asserted against rather than
// merely documented.
var bundlePlantings = []planting{
	{"wireguard_private_key", "/etc/network/interfaces (wireguard-private-key option)"},
	{"wireguard_preshared_key", "/etc/network/interfaces (wireguard-preshared-key option)"},
	{"pve_api_token", "the daemon log"},
	{"session_key", "the daemon log"},
	{"pve_ticket", "the daemon log"},
	{"oidc_client_secret", "vnprox.toml ([pve] dev_ticket_password)"},
	{"revert_ticket", "a changeset's ops_json"},
	{"federation_credential", "a changeset's ops_json"},
	{"k8s_kubeconfig", "a changeset's apply_log_json"},
	{"switch_credential", "a changeset's plan_json"},
	{"webhook_secret", "a changeset's title"},
}

//nolint:govet // fieldalignment: a test fixture struct; readability beats packing.
type bundleFixture struct {
	*fixture
	interfacesPath string
	corosyncPath   string
	logPath        string
	kernelPath     string
	osReleasePath  string
	pveDir         string
}

// newBundleFixture builds a complete, deliberately dirty installation.
func newBundleFixture(t *testing.T) *bundleFixture {
	t.Helper()
	base := newFixture(t)
	bf := &bundleFixture{
		fixture:        base,
		interfacesPath: filepath.Join(base.dir, "etc", "network", "interfaces"),
		corosyncPath:   filepath.Join(base.dir, "etc", "pve", "corosync.conf"),
		logPath:        filepath.Join(base.dir, "var", "vnprox.log"),
		kernelPath:     filepath.Join(base.dir, "proc", "osrelease"),
		osReleasePath:  filepath.Join(base.dir, "etc", "os-release"),
		pveDir:         filepath.Join(base.dir, "etc", "pve"),
	}
	for _, d := range []string{
		filepath.Dir(bf.interfacesPath), bf.pveDir, filepath.Dir(bf.kernelPath),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}

	writeFixtureFile(t, bf.kernelPath, "6.8.12-4-pve\n")
	writeFixtureFile(t, bf.osReleasePath, "ID=debian\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")

	// --- /etc/network/interfaces ----------------------------------------
	// A realistic PVE file, plus the two options that make this the most
	// dangerous input a bundle reads: a WireGuard stanza whose private and
	// preshared keys are right there in the clear.
	writeFixtureFile(t, bf.interfacesPath, ""+
		"auto lo\niface lo inet loopback\n\n"+
		"auto eno1\niface eno1 inet manual\n\tmtu 9000\n\n"+
		"auto vmbr0\niface vmbr0 inet static\n"+
		"\taddress 10.0.0.9/24\n"+
		"\tgateway 10.0.0.1\n"+
		"\tbridge-ports eno1\n"+
		"\tbridge-stp off\n"+
		"\tbridge-fd 0\n"+
		"\tbridge-vlan-aware yes\n"+
		"\tbridge-vids 2-4094\n"+
		"\talias "+bundleControlHost+"\n\n"+
		"auto wg0\niface wg0 inet static\n"+
		"\taddress 10.9.0.1/24\n"+
		"\twireguard-private-key "+secretMarkers["wireguard_private_key"]+"\n"+
		"\twireguard-preshared-key "+secretMarkers["wireguard_preshared_key"]+"\n"+
		"\twireguard-listen-port 51820\n")

	// --- corosync.conf ---------------------------------------------------
	// Both peers point at loopback so the reachability probe is a fast,
	// deterministic connection-refused rather than a timeout, and no fixed
	// port is claimed anywhere (the port comes from [server] listen, which
	// the fixture sets to 127.0.0.1:0).
	writeFixtureFile(t, bf.corosyncPath, ""+
		"nodelist {\n"+
		"  node {\n    name: "+seededNode+"\n    nodeid: 1\n    ring0_addr: 127.0.0.1\n  }\n"+
		"  node {\n    name: pve-fixture-2\n    nodeid: 2\n    ring0_addr: 127.0.0.1\n  }\n"+
		"}\n"+
		"totem {\n  cluster_name: fixture\n  version: 2\n}\n")

	// --- the daemon log --------------------------------------------------
	writeFixtureFile(t, bf.logPath, ""+
		"time=2026-07-30T10:00:00Z level=INFO msg=\"vnproxd starting\" version=3.0.3\n"+
		"time=2026-07-30T10:00:01Z level=INFO msg=\""+bundleControlLog+"\"\n"+
		"time=2026-07-30T10:00:02Z level=DEBUG msg=\"pve login\" ticket=PVE:root@pam:68A1B2C3::"+secretMarkers["pve_ticket"]+"\n"+
		"time=2026-07-30T10:00:03Z level=DEBUG msg=\"pve request\" Authorization: PVEAPIToken="+secretMarkers["pve_api_token"]+"\n"+
		"time=2026-07-30T10:00:04Z level=DEBUG msg=\"cipher init\" session_key="+secretMarkers["session_key"]+"\n"+
		"time=2026-07-30T10:00:05Z level=INFO msg=\"collector poll complete\" source=pve duration=412ms\n")

	// --- a richer vnprox.toml -------------------------------------------
	// Rewritten wholesale rather than appended to, because TOML forbids a
	// duplicate table header and seed_test.go's config already opens every
	// section it uses. dev_ticket_password is a genuine credential the
	// config format allows, and it is NOT on the bundle's allowlist —
	// which is the point.
	writeFixtureFile(t, base.configPath, ""+
		"[server]\nlisten = \""+base.listen+"\"\nread_only = false\n\n"+
		"[storage]\ndb_path = \""+base.dbPath+"\"\nsession_key_file = \""+filepath.Join(base.keyDir, "session.key")+"\"\n\n"+
		"[pve]\napi_url = \"https://127.0.0.1:8006/api2/json\"\n"+
		"token_file = \""+filepath.Join(base.keyDir, "pve-token")+"\"\n"+
		"dev_ticket_username = \"root@pam\"\n"+
		"dev_ticket_password = \""+secretMarkers["oidc_client_secret"]+"\"\n\n"+
		"[metrics]\nkey_file = \""+filepath.Join(base.keyDir, "metrics.key")+"\"\n\n"+
		"[blueprint]\nsigning_key_file = \""+filepath.Join(base.keyDir, "blueprint-signing.key")+"\"\n\n"+
		"[oidc]\nclient_secret_file = \""+filepath.Join(base.keyDir, "oidc-client-secret")+"\"\n\n"+
		"[peer]\nca_file = \""+bundleControlConfig+"\"\ntls_trust = \"cluster-ca\"\n\n"+
		"[collect]\npve_interval = \"30s\"\n")

	bf.seedBundleChangesets(t)
	return bf
}

// seedBundleChangesets adds the changeset whose four JSON columns carry
// planted markers, plus the control-titled one.
func (bf *bundleFixture) seedBundleChangesets(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, bf.dbPath)
	if err != nil {
		t.Fatalf("opening fixture store: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := store.NewChangesetRepo(db)
	if insErr := repo.Insert(ctx, store.Changeset{
		ID: "cs-bundle-1", Title: bundleControlStore, Author: "root@pam",
		Status: "committed", Origin: "ui",
		OpsJSON: `[{"type":"wg.peer.add","node":"pve1","params":{` +
			`"publicKey":"peerpub","revertTicket":"` + secretMarkers["revert_ticket"] + `",` +
			`"credential":"` + secretMarkers["federation_credential"] + `",` +
			`"allowedIps":["10.9.0.2/32"]}}]`,
		CreatedAt: 1700000400, UpdatedAt: 1700000500,
	}); insErr != nil {
		t.Fatalf("seeding bundle changeset: %v", insErr)
	}

	if _, execErr := db.Conn().ExecContext(ctx,
		`UPDATE changesets SET plan_json = ?, apply_log_json = ?, findings_json = ? WHERE id = ?`,
		`{"nodes":[{"node":"pve1","switchCredential":"`+secretMarkers["switch_credential"]+`","diff":"+iface wg0"}]}`,
		`{"steps":[{"cmd":"kubectl apply","kubeconfig":"`+secretMarkers["k8s_kubeconfig"]+`"}]}`,
		`[{"code":"mtu_mismatch","severity":"warn","message":"vmbr0 mtu 9000 vs eno1 1500"}]`,
		"cs-bundle-1"); execErr != nil {
		t.Fatalf("seeding bundle changeset json columns: %v", execErr)
	}

	// A second changeset whose *title* carries a planted secret: an
	// operator pasting a webhook secret into a title is not hypothetical,
	// and titles are exactly the field a field-allowlist cannot protect.
	if insErr := repo.Insert(ctx, store.Changeset{
		ID: "cs-bundle-2", Title: "rotate webhook secret=" + secretMarkers["webhook_secret"],
		Author: "root@pam", Status: "draft", Origin: "cli",
		OpsJSON:   `[]`,
		CreatedAt: 1700000600, UpdatedAt: 1700000600,
	}); insErr != nil {
		t.Fatalf("seeding bundle changeset 2: %v", insErr)
	}

	// A finding-event row so findings/events.json is not empty.
	if _, execErr := db.Conn().ExecContext(ctx,
		`INSERT INTO finding_events (finding_id, at, transition) VALUES (?,?,?)`,
		"mtu_mismatch:pve1:vmbr0", 1700000700, "new"); execErr != nil {
		t.Fatalf("seeding finding event: %v", execErr)
	}
}

// options returns BundleOptions wired to this fixture. Every path is inside
// t.TempDir(), so a test never reads the developer's own /etc.
func (bf *bundleFixture) options(t *testing.T, outDir string) BundleOptions {
	t.Helper()
	return BundleOptions{
		ConfigPath: bf.configPath,
		DBPath:     bf.dbPath,
		Listen:     bf.listen,
		KeyPaths: []KeyPathRef{
			{ClassID: "session_key", Path: filepath.Join(bf.keyDir, "session.key")},
			{ClassID: "pve_api_token", Path: filepath.Join(bf.keyDir, "pve-token")},
			{ClassID: "metrics_scrape_token", Path: filepath.Join(bf.keyDir, "metrics.key")},
			{ClassID: "blueprint_signing_key", Path: filepath.Join(bf.keyDir, "blueprint-signing.key")},
			{ClassID: "oidc_client_secret", Path: filepath.Join(bf.keyDir, "oidc-client-secret")},
		},
		OutDir:            outDir,
		Node:              seededNode,
		ToolVersion:       "test",
		InterfacesPath:    bf.interfacesPath,
		CorosyncPath:      bf.corosyncPath,
		PVEDir:            bf.pveDir,
		KernelReleasePath: bf.kernelPath,
		OSReleasePath:     bf.osReleasePath,
		LogSource:         LogSource{Path: bf.logPath},
		Probe:             true,
		ProbeTimeout:      200 * time.Millisecond,
		Now:               fixedNow,
	}
}

// incidentOptions is options() plus T-2804's incident document — the second
// producer AC1's scan runs over. Every free-text field of the document
// carries a planted secret marker (incidentPlantings), plus one control
// marker that must survive.
func (bf *bundleFixture) incidentOptions(t *testing.T, outDir string) BundleOptions {
	t.Helper()
	opts := bf.options(t, outDir)
	opts.Incident = bundleFixtureIncident()
	return opts
}

// bundleFixtureIncident is the seeded incident document, built once here so
// the leak test and the planting-guard test read the same bytes.
func bundleFixtureIncident() *BundleIncident {
	return &BundleIncident{
		ID:          "01JC0INCIDENT0000000000000",
		Title:       "uplink flapping; rotate webhook secret=" + secretMarkers["webhook_secret"],
		Status:      "closed",
		OpenedBy:    "root@pam",
		OpenedAt:    1700000900,
		StartedAt:   1700000400,
		EndedAt:     1700000800,
		ClosedAt:    1700000900,
		Retroactive: true,
		WindowFrom:  1700000400,
		WindowTo:    1700000800,
		EventCount:  3,
		Events: []BundleIncidentEvent{
			{
				At: 1700000410, Source: "annotation", Kind: "note",
				Summary: bundleControlIncident + " — ticket was PVE:root@pam:68A1B2C3::" + secretMarkers["pve_ticket"],
				Actor:   "root@pam",
			},
			{
				At: 1700000500, Source: "changeset", Kind: "changeset.apply",
				Summary:     "changeset.apply cs-bundle-1 revertTicket=" + secretMarkers["revert_ticket"] + " (success)",
				Actor:       "root@pam",
				Result:      "success",
				ChangesetID: "cs-bundle-1",
				Ref:         "bridge:pve1:vmbr0",
			},
			{
				At: 1700000600, Source: "capture", Kind: "started",
				Summary: "capture started on bridge:pve1:vmbr0 (pve1)", Node: "pve1",
				Ref: "bridge:pve1:vmbr0", CaptureID: "cap-1",
			},
		},
		Sources: []BundleIncidentSource{
			{Source: "flow", Status: "error", Count: 0,
				Detail: "querying flow samples failed: dial tcp: session_key=" + secretMarkers["session_key"]},
		},
		Caveats: []string{
			"the point-in-time diff compared /etc/network/interfaces only",
			"k8s overlay context was kubeconfig=" + secretMarkers["k8s_kubeconfig"],
		},
		DiffErrorCode: "",
		DiffError:     "federation credential=" + secretMarkers["federation_credential"] + " could not be used",
		Diff: &BundleIncidentDiff{
			FromAt: 1700000400, ToAt: 1700000800,
			FromSnapshotID: "snap-1", ToSnapshotID: "snap-2",
			Added: 1, Modified: 1, Unattributed: 1,
			ComparedPaths:  []string{"/etc/network/interfaces"},
			OmittedPaths:   []string{"/etc/pve/sdn/zones.cfg"},
			UnmatchedNodes: []string{"pve3 (captured only in to)"},
			Entries: []BundleIncidentDiffEntry{
				{
					Change: "modified", Ref: "iface:pve1:wg0", Kind: "iface", Node: "pve1",
					Name:       "wg0 credential=" + secretMarkers["switch_credential"],
					Attributed: false,
					Fields:     []string{"wireguard-private-key=" + secretMarkers["wireguard_private_key"]},
				},
			},
		},
	}
}

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
