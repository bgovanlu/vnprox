package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/push"
	"github.com/bgovanlu/vnprox/internal/store"
)

// seed_test.go builds the fixture every AC1/AC2/AC4 test in this package
// works against: a store containing at least one row of every kind T-1901's
// objective names (changesets, pre/post snapshots, audit history, layout,
// tenants, blueprint state) AND one instance of every sealed secret class
// in the inventory.
//
// The plaintext of each secret is a distinctive, unique marker string, so
// AC2's scan can look for it in the archive bytes and know exactly which
// class leaked if one ever does.

// secretMarkers maps SecretClass.ID -> the plaintext this fixture seeds for
// it. Every sealed-column and key-file class in the inventory must have an
// entry; TestSeed_CoversEverySecretClass enforces that, so adding a secret
// class without teaching this fixture to seed it fails the suite rather
// than silently narrowing AC2's coverage.
var secretMarkers = map[string]string{
	// Key files (present on disk; only in the archive under --include-keys).
	"session_key":           "SEEDKEY-session-0123456789abcdef", // exactly 32 bytes: a real AES-256 key
	"pve_api_token":         "SEEDMARK-pve-api-token-vnprox-daemon-9f3a",
	"metrics_scrape_token":  "SEEDMARK-metrics-scrape-token-7c21",
	"blueprint_signing_key": "SEEDMARK-blueprint-signing-key-4e88",
	"oidc_client_secret":    "SEEDMARK-oidc-client-secret-b105",

	// Sealed columns (ciphertext in every archive).
	"pve_ticket":              "SEEDMARK-pve-ticket-PVE:root@pam:AAAA",
	"pve_csrf_token":          "SEEDMARK-pve-csrf-BBBB",
	"federation_credential":   "SEEDMARK-federation-credential-CCCC",
	"oidc_pve_credential":     "SEEDMARK-oidc-pve-credential-DDDD",
	"switch_credential":       "SEEDMARK-switch-driver-credential-EEEE",
	"wireguard_private_key":   "SEEDMARK-wireguard-private-key-FFFF",
	"wireguard_preshared_key": "SEEDMARK-wireguard-preshared-key-GGGG",
	"k8s_kubeconfig":          "SEEDMARK-kubeconfig-HHHH",
	"webhook_secret":          "SEEDMARK-webhook-signing-secret-IIII",
	"alert_target_secret":     "SEEDMARK-alert-target-secret-JJJJ",
	"ingress_credential":      "SEEDMARK-ingress-credential-KKKK",
	"revert_ticket":           "SEEDMARK-sealed-revert-ticket-LLLL",
	// The web-push subscription endpoint is stored TWICE in the same row,
	// in two different protected forms (0046's migration doc comment):
	// encrypted (endpoint_enc) and, separately, one-way hashed
	// (endpoint_hash, for lookup without decrypting) — so both classes
	// below seed the SAME underlying marker value, since they cover the
	// same secret in two forms, not two different secrets.
	"push_subscription_endpoint":      "SEEDMARK-push-endpoint-https://push.example.com/send/OOOO",
	"push_subscription_endpoint_hash": "SEEDMARK-push-endpoint-https://push.example.com/send/OOOO",
	"push_subscription_p256dh":        "SEEDMARK-push-p256dh-PPPP",
	"push_subscription_auth":          "SEEDMARK-push-auth-QQQQ",

	// Hashed: seeded as a raw token whose HASH is what lands in the store.
	// AC2 asserts the raw value never appears, which is a property of
	// hashing rather than of this card — asserting it anyway is what keeps
	// the claim honest if that ever changes.
	"api_token": "SEEDMARK-raw-automation-token-MMMM",
	// Same shape: a one-time callback token stored only as a hash.
	"schedule_callback_token": "SEEDMARK-raw-schedule-callback-token-NNNN",

	// External: never collected under any flag, so there is nothing to
	// seed. Present here so the coverage test's map lookup is total.
	"peer_cluster_secret": "",
}

// unsealedMarker is the CONTROL for AC2's scan: a value written to a
// deliberately UNSEALED column (annotations.content, an operator note —
// genuinely not a secret). It must be findable in the archive. If it is
// not, the scanner is broken and every "not found" result above is
// meaningless.
const unsealedMarker = "SEEDMARK-CONTROL-this-one-is-not-a-secret-and-must-be-findable"

// seededNode is the fixture's node name.
const seededNode = "pve-fixture-1"

// fixture is a seeded installation on disk: a store, a config, and a key
// directory.
//
//nolint:govet // fieldalignment: a test fixture struct; readability beats packing.
type fixture struct {
	dir        string
	dbPath     string
	configPath string
	keyDir     string
	keyPaths   []string
	listen     string
}

// newFixture creates a fully-seeded installation under t.TempDir().
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{
		dir:        dir,
		dbPath:     filepath.Join(dir, "var", "vnprox.db"),
		configPath: filepath.Join(dir, "etc", "vnprox.toml"),
		keyDir:     filepath.Join(dir, "etc", "keys"),
		// 127.0.0.1:0 is never bound and never in use, so the liveness
		// check's listen probe is a no-op unless a test deliberately binds
		// something. No fixed port is claimed anywhere in this package.
		listen: "127.0.0.1:0",
	}
	for _, d := range []string{filepath.Dir(f.dbPath), filepath.Dir(f.configPath), f.keyDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}

	// --- key files ------------------------------------------------------
	keys := []struct{ name, id string }{
		{"session.key", "session_key"},
		{"pve-token", "pve_api_token"},
		{"metrics.key", "metrics_scrape_token"},
		{"blueprint-signing.key", "blueprint_signing_key"},
		{"oidc-client-secret", "oidc_client_secret"},
	}
	for _, k := range keys {
		p := filepath.Join(f.keyDir, k.name)
		if err := os.WriteFile(p, []byte(secretMarkers[k.id]), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
		f.keyPaths = append(f.keyPaths, p)
	}

	// --- config ---------------------------------------------------------
	cfg := "" +
		"[server]\nlisten = \"" + f.listen + "\"\n\n" +
		"[storage]\ndb_path = \"" + f.dbPath + "\"\nsession_key_file = \"" + filepath.Join(f.keyDir, "session.key") + "\"\n\n" +
		"[pve]\ntoken_file = \"" + filepath.Join(f.keyDir, "pve-token") + "\"\n\n" +
		"[metrics]\nkey_file = \"" + filepath.Join(f.keyDir, "metrics.key") + "\"\n\n" +
		"[blueprint]\nsigning_key_file = \"" + filepath.Join(f.keyDir, "blueprint-signing.key") + "\"\n\n" +
		"[oidc]\nclient_secret_file = \"" + filepath.Join(f.keyDir, "oidc-client-secret") + "\"\n"
	if err := os.WriteFile(f.configPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	f.seedStore(t)
	return f
}

// sessionKey is the raw AES key the fixture seals everything with.
func (f *fixture) sessionKey(t *testing.T) []byte {
	t.Helper()
	key := []byte(secretMarkers["session_key"])
	if len(key) != store.KeySize {
		t.Fatalf("fixture session key is %d bytes, want %d", len(key), store.KeySize)
	}
	return key
}

//nolint:gocyclo // a linear fixture: one block per table, each independent.
func (f *fixture) seedStore(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, f.dbPath)
	if err != nil {
		t.Fatalf("opening fixture store: %v", err)
	}
	defer func() { _ = db.Close() }()

	cipher, err := store.NewSessionCipher(f.sessionKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	seal := func(id string) []byte {
		sealed, sealErr := cipher.Encrypt([]byte(secretMarkers[id]))
		if sealErr != nil {
			t.Fatalf("sealing %s: %v", id, sealErr)
		}
		return sealed
	}
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seeding %s: %v", what, err)
		}
	}

	// sessions — sealed by the repo itself (it owns the cipher).
	must("session", store.NewSessionRepo(db, cipher).Insert(ctx, store.Session{
		ID: "sess-1", Username: "root@pam", Realm: "pam",
		PVETicket: secretMarkers["pve_ticket"], CSRFToken: secretMarkers["pve_csrf_token"],
		CapsJSON: `{"netRead":true}`, CreatedAt: 1700000000, ExpiresAt: 1700007200,
	}))

	// changesets + the sealed revert ticket (T-1805).
	csRepo := store.NewChangesetRepo(db)
	must("changeset", csRepo.Insert(ctx, store.Changeset{
		ID: "cs-1", Title: "add vmbr9", Author: "root@pam", Status: "committed",
		Origin: "ui", OpsJSON: `[{"type":"net.iface.create","node":"pve1"}]`,
		CreatedAt: 1700000100, UpdatedAt: 1700000200,
	}))
	must("revert ticket", csRepo.SealRevertTicket(ctx, "cs-1", seal("revert_ticket"), 1700007200))

	// snapshots (pre and post) + blob content.
	blobRepo := store.NewBlobRepo(db)
	hash, err := blobRepo.Put(ctx, "auto lo\niface lo inet loopback\n")
	must("blob", err)
	files, err := json.Marshal([]map[string]string{{"node": seededNode, "path": "/etc/network/interfaces", "sha256": hash}})
	must("files json", err)
	snapRepo := store.NewSnapshotRepo(db)
	must("pre snapshot", snapRepo.Insert(ctx, store.Snapshot{
		ID: "snap-pre-1", Kind: "pre", FilesJSON: string(files),
		ChangesetID: sql.NullString{String: "cs-1", Valid: true}, TakenAt: 1700000150,
	}))
	must("post snapshot", snapRepo.Insert(ctx, store.Snapshot{
		ID: "snap-post-1", Kind: "post", FilesJSON: string(files),
		ChangesetID: sql.NullString{String: "cs-1", Valid: true}, TakenAt: 1700000250,
	}))

	// audit history.
	auditRepo := store.NewAuditRepo(db)
	for i, action := range []string{"changeset.create", "changeset.apply", "changeset.confirm"} {
		_, err := auditRepo.Append(ctx, store.AuditEntry{
			At: int64(1700000100 + i*10), Username: "root@pam", Action: action, Result: "success",
			ChangesetID: sql.NullString{String: "cs-1", Valid: true},
		})
		must("audit "+action, err)
	}

	// federation, OIDC, switches, WireGuard, k8s, webhooks, alerts, ingress
	// — one sealed row each.
	must("cluster", store.NewClusterRepo(db).Insert(ctx, store.Cluster{
		ID: "clu-1", Name: "dc2", APIURL: "https://dc2:8006", Status: "ok", AddedBy: "root@pam",
		CredentialEnc: seal("federation_credential"), AddedAt: 1700000000,
	}))
	must("oidc link", store.NewOIDCPVELinkRepo(db).Upsert(ctx, store.OIDCPVELink{
		ID: "oidc-1", ClusterID: "clu-1", OIDCGroup: "netops", PVEUsername: "automation@pve",
		CredentialEnc: seal("oidc_pve_credential"), CreatedBy: "root@pam", CreatedAt: 1700000000,
	}))
	must("switch", store.NewSwitchRepo(db).Insert(ctx, store.Switch{
		ID: "sw-1", Name: "tor-1", MgmtAddr: "10.0.0.9", DriverType: "gnmi", AddedBy: "root@pam",
		CredentialsEnc: seal("switch_credential"), AddedAt: 1700000000,
	}))
	wgRepo := store.NewWireGuardRepo(db)
	must("wg tunnel", wgRepo.InsertTunnel(ctx, store.WireGuardTunnel{
		ID: "wg-1", Node: seededNode, IfName: "wg0", PublicKey: "pub", Carrier: "vmbr0",
		CreatedBy: "root@pam", PrivateKeyEnc: seal("wireguard_private_key"),
		Addresses: []string{"10.9.0.1/24"}, ListenPort: 51820, MTU: 1420, CreatedAt: 1700000000,
	}))
	must("wg peer", wgRepo.AddPeer(ctx, store.WireGuardPeer{
		TunnelID: "wg-1", PublicKey: "peerpub", Endpoint: "203.0.113.5:51820",
		AllowedIPs: []string{"10.9.0.2/32"}, PresharedKeyEnc: seal("wireguard_preshared_key"),
		KeepaliveSec: 25,
	}))
	must("k8s cluster", store.NewK8sClusterRepo(db).Insert(ctx, store.K8sCluster{
		ID: "k8s-1", Name: "prod", AddedBy: "root@pam", CNIDetected: "cilium", Status: "ok",
		KubeconfigEnc: seal("k8s_kubeconfig"), AddedAt: 1700000000,
	}))
	must("webhook", store.NewWebhookRepo(db).Create(ctx, store.Webhook{
		ID: "wh-1", URL: "https://hooks.example/vnprox", CreatedBy: "root@pam",
		SecretEnc: seal("webhook_secret"), CreatedAt: 1700000000,
	}))
	must("alert rule", store.NewAlertRuleRepo(db).Insert(ctx, store.AlertRule{
		ID: "ar-1", Name: "drift", TargetKind: "webhook", TargetURL: "https://alerts.example",
		TargetSecretEnc: seal("alert_target_secret"), CreatedAt: 1700000000, UpdatedAt: 1700000000,
		Enabled: true,
	}))
	must("ingress target", store.NewIngressTargetRepo(db).Insert(ctx, store.IngressTarget{
		ID: "ing-1", Kind: "haproxy", Address: "10.0.0.10", AddedBy: "root@pam",
		CredentialEnc: seal("ingress_credential"), AddedAt: 1700000000,
	}))
	must("api token", store.NewAPITokenRepo(db).Create(ctx, store.APIToken{
		ID: "tok-1", Name: "ci", TokenHash: sha256Hex(secretMarkers["api_token"]),
		ScopesJSON: `["netRead"]`, CreatedBy: "root@pam", CreatedAt: 1700000000,
	}))
	must("push subscription", store.NewPushSubscriptionRepo(db).Create(ctx, store.PushSubscription{
		ID: "push-1", SessionID: "sess-1", Username: "root@pam",
		EndpointHash:   push.EndpointHash(secretMarkers["push_subscription_endpoint"]),
		EndpointEnc:    seal("push_subscription_endpoint"),
		P256dhEnc:      seal("push_subscription_p256dh"),
		AuthEnc:        seal("push_subscription_auth"),
		CategoriesJSON: `["critical","awaitingConfirm"]`,
		CreatedAt:      1700000000,
	}))

	// layout, tenants, blueprint state — the rest of what T-1901's
	// objective says is on the line.
	conn := db.Conn()
	seedSQL := []struct {
		what string
		q    string
		args []any
	}{
		{"layout", `INSERT INTO layouts (username, name, layout_json, updated_at) VALUES (?,?,?,?)`,
			[]any{"root@pam", "default", `{"node:pve1":{"x":10,"y":20}}`, 1700000000}},
		{"tenant", `INSERT INTO tenants (id, name, created_by, created_at) VALUES (?,?,?,?)`,
			[]any{"ten-1", "netops", "root@pam", 1700000000}},
		{"tenant scope", `INSERT INTO tenant_scopes (tenant_id, scope_ref) VALUES (?,?)`,
			[]any{"ten-1", "node:pve1"}},
		{"changeset schedule", `INSERT INTO changeset_schedules (changeset_id, window_start, window_end, confirm_timeout_sec, missed_window_policy, callback_token_hash, status, created_by, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			[]any{"cs-1", 1700000300, 1700000900, 120, "skip", sha256Hex(secretMarkers["schedule_callback_token"]), "pending", "root@pam", 1700000000}},
		{"tenant member", `INSERT INTO tenant_members (tenant_id, identity, role) VALUES (?,?,?)`,
			[]any{"ten-1", "alice@pve", "approver"}},
		{"blueprint", `INSERT INTO blueprints (id, name, blueprint_json, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			[]any{"bp-1", "three-tier", `{"kind":"blueprint","name":"three-tier"}`, "root@pam", 1700000000, 1700000000}},
		// The CONTROL: a genuinely non-secret operator note, stored
		// unsealed, carrying a marker AC2's scan must find.
		{"annotation (control)", `INSERT INTO annotations (id, ref, content, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			[]any{"ann-1", "node:pve1", unsealedMarker, "root@pam", 1700000000, 1700000000}},
	}
	for _, s := range seedSQL {
		if _, err := conn.ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seeding %s: %v", s.what, err)
		}
	}
}

// sha256Hex mirrors the one-way hashing api_tokens.token_hash stores.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestSeed_CoversEverySecretClass keeps the fixture honest as the product
// grows: a secret class added to the inventory without a seeded marker
// fails here, so AC2's table can never silently stop covering something.
func TestSeed_CoversEverySecretClass(t *testing.T) {
	for _, c := range SecretClasses() {
		marker, ok := secretMarkers[c.ID]
		if !ok {
			t.Errorf("secret class %q has no entry in secretMarkers — add one so AC2's scan covers it "+
				"(seed_test.go), or explain in the inventory why it can never appear in an archive", c.ID)
			continue
		}
		if c.Storage != StorageExternal && marker == "" {
			t.Errorf("secret class %q has an empty marker but is stored as %q — AC2 cannot scan for nothing", c.ID, c.Storage)
		}
	}
}

// TestSecretClasses_CoverEverySealedColumn checks the inventory against the
// real schema rather than against itself: every `*_enc` column in a
// migrated store must be named by some SecretClass. A new sealed column
// landing without an inventory entry fails here, which is what stops the
// declared inventory (and therefore T-1902's redaction list) from rotting.
func TestSecretClasses_CoverEverySealedColumn(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	declared := map[string]bool{}
	for _, c := range SecretClasses() {
		if c.Column != "" {
			declared[c.Column] = true
		}
	}

	rows, err := db.Conn().QueryContext(ctx,
		`SELECT m.name, p.name FROM sqlite_master m
		 JOIN pragma_table_info(m.name) p
		 WHERE m.type = 'table' AND (p.name LIKE '%_enc' OR p.name LIKE '%_hash')`)
	if err != nil {
		t.Fatalf("listing sealed columns: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := 0
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found++
		qualified := table + "." + column
		if !declared[qualified] {
			t.Errorf("column %s holds a secret but no SecretClass in internal/backup's inventory names it — "+
				"add one (secrets.go) so `backup --include-keys`'s warning and T-1902's redaction list both know about it", qualified)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	// If the query stopped finding anything, this test would pass
	// vacuously forever.
	if found < 10 {
		t.Fatalf("only %d sealed/hashed columns found in a fully migrated store — the discovery query is broken, "+
			"not the schema (there are at least 11 today)", found)
	}
}
