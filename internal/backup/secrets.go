package backup

import "sort"

// Storage says where a secret class physically lives, which is what decides
// whether an archive can possibly contain it in the clear.
type Storage string

const (
	// StorageSealedColumn: the secret lives in the SQLite store as
	// AES-256-GCM ciphertext under internal/store.SessionCipher, keyed from
	// [storage] session_key_file. Every backup contains the ciphertext (it
	// is part of the store); it is readable only by someone who also holds
	// the session key.
	StorageSealedColumn Storage = "sealed-column"

	// StorageHashedColumn: the store holds a one-way hash, never the
	// credential. Present in every backup and unrecoverable from it, with
	// or without the session key.
	StorageHashedColumn Storage = "hashed-column"

	// StorageKeyFile: the secret is a file on this node's disk, in the
	// clear. Collected ONLY under --include-keys.
	StorageKeyFile Storage = "key-file"

	// StorageExternal: the secret is not vnprox's to hold — it lives in
	// PVE's own replicated filesystem or on another system entirely. Never
	// collected, under any flag; listed here so the inventory is complete
	// and so "what must be re-established after a restore" has a source.
	StorageExternal Storage = "external"
)

// SecretClass is one named class of credential vnprox holds or depends on.
//
// This inventory is the single source of truth for three things that must
// not drift apart:
//
//   - the warning `vnproxctl backup --include-keys` prints, which has to
//     name what an operator is about to put in a file they may copy off the
//     box;
//   - T-1901 AC2's table-driven "no key material in a default backup"
//     scan, which asserts one case per class rather than spot-checking;
//   - T-1902's redaction inventory, which needs the same list with a
//     harsher policy (a support bundle must contain none of these,
//     including the sealed ciphertext).
//
// Adding a secret to vnprox without adding it here is the failure mode
// being designed against; TestSecretClasses_CoverEverySealedColumn walks
// the real migrations and fails if a `*_enc` column has no class.
type SecretClass struct {
	// ID is the stable identifier used in manifests, tests and reports.
	ID string
	// Name is the operator-facing name printed in warnings.
	Name string
	// Detail says what an attacker who obtains this class actually gets.
	Detail string
	// Storage says where it lives (and therefore how an archive could
	// expose it).
	Storage Storage
	// Column, for sealed/hashed classes, is the `table.column` it occupies.
	// Used by TestSecretClasses_CoverEverySealedColumn to check the
	// inventory against the real schema.
	Column string
}

// secretClasses is the inventory. Order is deliberate: the session key
// first, because it is the one that makes all the sealed classes readable.
var secretClasses = []SecretClass{
	{
		ID:      "session_key",
		Name:    "session encryption key",
		Detail:  "the AES-256-GCM key that unseals EVERY sealed class below — PVE tickets, federation credentials, WireGuard private keys, switch credentials, webhook secrets and revert tickets all decrypt with this one file",
		Storage: StorageKeyFile,
	},
	{
		ID:      "pve_api_token",
		Name:    "PVE API token (vnprox@pve!daemon)",
		Detail:  "the daemon's own read-only Proxmox API credential; auditor-level read across the whole cluster",
		Storage: StorageKeyFile,
	},
	{
		ID:      "metrics_scrape_token",
		Name:    "metrics scrape token",
		Detail:  "grants unauthenticated-by-session access to GET /metrics",
		Storage: StorageKeyFile,
	},
	{
		ID:      "blueprint_signing_key",
		Name:    "blueprint signing key",
		Detail:  "signs blueprint bundles this installation publishes; holding it lets an attacker forge a bundle that passes the trust gate",
		Storage: StorageKeyFile,
	},
	{
		ID:      "oidc_client_secret",
		Name:    "OIDC client secret",
		Detail:  "this installation's confidential-client secret at the identity provider",
		Storage: StorageKeyFile,
	},
	{
		ID:      "pve_ticket",
		Name:    "PVE session ticket",
		Detail:  "a logged-in user's live Proxmox ticket and CSRF token",
		Storage: StorageSealedColumn,
		Column:  "sessions.pve_ticket_enc",
	},
	{
		ID:      "pve_csrf_token",
		Name:    "PVE CSRF token",
		Detail:  "the CSRF half of a live session's Proxmox credentials",
		Storage: StorageSealedColumn,
		Column:  "sessions.csrf_token_enc",
	},
	{
		ID:      "federation_credential",
		Name:    "federation cluster credential",
		Detail:  "the PVE API credential for each attached cluster in a federated deployment",
		Storage: StorageSealedColumn,
		Column:  "clusters.credential_enc",
	},
	{
		ID:      "oidc_pve_credential",
		Name:    "OIDC-mapped PVE credential",
		Detail:  "the PVE identity an OIDC group maps onto, credential included",
		Storage: StorageSealedColumn,
		Column:  "oidc_pve_links.credential_enc",
	},
	{
		ID:      "switch_credential",
		Name:    "switch driver credential",
		Detail:  "gNMI/OpenConfig auth for each registered physical switch",
		Storage: StorageSealedColumn,
		Column:  "switches.credentials_enc",
	},
	{
		ID:      "wireguard_private_key",
		Name:    "WireGuard tunnel private key",
		Detail:  "the X25519 private key of every site-to-site tunnel this node terminates",
		Storage: StorageSealedColumn,
		Column:  "wireguard_tunnels.private_key_enc",
	},
	{
		ID:      "wireguard_preshared_key",
		Name:    "WireGuard preshared key",
		Detail:  "the optional per-peer PSK layered on a tunnel",
		Storage: StorageSealedColumn,
		Column:  "wireguard_peers.preshared_key_enc",
	},
	{
		ID:      "k8s_kubeconfig",
		Name:    "Kubernetes cluster kubeconfig",
		Detail:  "the full kubeconfig — API server address and client credentials — of every attached Kubernetes cluster",
		Storage: StorageSealedColumn,
		Column:  "k8s_clusters.kubeconfig_enc",
	},
	{
		ID:      "webhook_secret",
		Name:    "webhook signing secret",
		Detail:  "the HMAC secret each registered webhook target's deliveries are signed with",
		Storage: StorageSealedColumn,
		Column:  "webhooks.secret_enc",
	},
	{
		ID:      "alert_target_secret",
		Name:    "alert-rule target secret",
		Detail:  "the credential an alert rule authenticates to its notification target with",
		Storage: StorageSealedColumn,
		Column:  "alert_rules.target_secret_enc",
	},
	{
		ID:      "ingress_credential",
		Name:    "ingress target credential",
		Detail:  "the credential vnprox uses to program an external ingress target",
		Storage: StorageSealedColumn,
		Column:  "ingress_targets.credential_enc",
	},
	{
		ID:      "revert_ticket",
		Name:    "sealed apply-time revert ticket (T-1805)",
		Detail:  "the applying user's PVE ticket, sealed for the length of one changeset's confirm window so an unattended firewall/SDN revert can act as them",
		Storage: StorageSealedColumn,
		Column:  "changesets.revert_ticket_enc",
	},
	{
		ID:      "push_subscription_endpoint",
		Name:    "web-push subscription endpoint",
		Detail:  "the push service URL a browser's PushManager.subscribe() returned; anyone holding it can push arbitrary notifications to that device until it unsubscribes",
		Storage: StorageSealedColumn,
		Column:  "push_subscriptions.endpoint_enc",
	},
	{
		ID:      "push_subscription_p256dh",
		Name:    "web-push subscription public key",
		Detail:  "the subscriber's ECDH public key (RFC 8291) used to encrypt push message payloads to that specific device",
		Storage: StorageSealedColumn,
		Column:  "push_subscriptions.p256dh_enc",
	},
	{
		ID:      "push_subscription_auth",
		Name:    "web-push subscription auth secret",
		Detail:  "the subscriber's authentication secret (RFC 8291); combined with the endpoint, it is what lets vnprox address a push to one specific device",
		Storage: StorageSealedColumn,
		Column:  "push_subscriptions.auth_enc",
	},
	{
		ID:      "push_subscription_endpoint_hash",
		Name:    "web-push subscription endpoint lookup hash",
		Detail:  "a one-way SHA-256 of the same endpoint URL, kept unencrypted so the daemon can recognize a resubscribe without decrypting; an archive can prove a device was subscribed, never reproduce the endpoint from it alone",
		Storage: StorageHashedColumn,
		Column:  "push_subscriptions.endpoint_hash",
	},
	{
		ID:      "api_token",
		Name:    "vnprox automation token",
		Detail:  "stored as a one-way SHA-256 hash only — an archive can prove a token existed, never reproduce it",
		Storage: StorageHashedColumn,
		Column:  "api_tokens.token_hash",
	},
	{
		ID:      "schedule_callback_token",
		Name:    "scheduled-apply callback token",
		Detail:  "the one-time token that confirms a scheduled changeset's apply; stored as a one-way SHA-256 hash only",
		Storage: StorageHashedColumn,
		Column:  "changeset_schedules.callback_token_hash",
	},
	{
		ID:      "peer_cluster_secret",
		Name:    "peer cluster secret",
		Detail:  "the HMAC secret authenticating cluster-internal peer API requests; lives on pmxcfs (/etc/pve/priv/vnprox/), is replicated by Proxmox itself, and is NEVER collected by vnprox backup under any flag",
		Storage: StorageExternal,
	},
}

// SecretClasses returns the full declared inventory, in the fixed order
// above. The slice is a copy: callers cannot mutate the inventory.
func SecretClasses() []SecretClass {
	out := make([]SecretClass, len(secretClasses))
	copy(out, secretClasses)
	return out
}

// SecretClassesBy returns every class with the given storage, in inventory
// order.
func SecretClassesBy(s Storage) []SecretClass {
	var out []SecretClass
	for _, c := range secretClasses {
		if c.Storage == s {
			out = append(out, c)
		}
	}
	return out
}

// SecretClassByID looks a class up by ID.
func SecretClassByID(id string) (SecretClass, bool) {
	for _, c := range secretClasses {
		if c.ID == id {
			return c, true
		}
	}
	return SecretClass{}, false
}

// secretClassIDs returns the sorted IDs of the given classes — the shape
// recorded in a manifest's secretClasses field, sorted so a manifest is
// byte-stable for a given set.
func secretClassIDs(classes []SecretClass) []string {
	out := make([]string, 0, len(classes))
	for _, c := range classes {
		out = append(out, c.ID)
	}
	sort.Strings(out)
	return out
}
