// SPDX-License-Identifier: Apache-2.0

package findings

// health_storecapacity.go implements T-1905's store_near_capacity finding:
// a warning when vnproxd's own SQLite app store (vnprox.db, including its
// WAL/SHM sidecars) approaches a configured size threshold. This is the
// daemon's own disk-headroom self-check — the failure mode the whole T-1905
// card opens with is a full root filesystem caused by vnprox's own
// unbounded data ("an outage caused by the tool meant to prevent one"), so
// a filling store should be a warning an operator sees in the findings
// stream, not a surprise discovered from `df` during an incident.
//
// Reuses T-1903's store.DB.SizeBytes() as the single size source
// (StoreCapacityProvider is a cmd/vnproxd adapter over the exact same *DB
// api.StoreInfoProvider already reads for vnprox_store_size_bytes) rather
// than a second measurement of the same on-disk footprint — the task card's
// explicit instruction. This package still does not import internal/store
// directly (the same decoupling PeerTrustProvider/WanProvider/every other
// Config seam in this package uses), so the report shape is declared here
// and adapted in cmd/vnproxd.

import "fmt"

// CheckStoreNearCapacity is this file's sole check name.
const CheckStoreNearCapacity = "store_near_capacity"

const storeNearCapacityDocsLink = "docs/deployment.md#sizing-and-retention"

// storeCapacityRise/Fall: 2 consecutive findings-cycle observations before
// the finding fires or clears — the same rise/fall peerUnreachableRise/Fall
// uses for a continuously-recomputed signal. A single noisy 30s-cycle
// reading (e.g. a stat() racing a WAL checkpoint mid-compaction) must not
// flap the finding.
const (
	storeCapacityRise = 2
	storeCapacityFall = 2
)

// StoreCapacityReport is a fresh reading of the app store's own on-disk
// size, from cmd/vnproxd's storeCapacityAdapter.
type StoreCapacityReport struct {
	// Node names the daemon reporting this reading. Unlike a peer/
	// federation finding, the app store is per-node — never cluster-shared
	// (docs/architecture.md) — so this always names exactly the local
	// node, never a remote one.
	Node string
	// SizeBytes is store.DB.SizeBytes()'s reading: the main database file
	// plus its WAL/SHM sidecars, summed.
	SizeBytes int64
}

// StoreCapacityProvider is the findings engine's seam onto the app store's
// own on-disk size (T-1905). A nil provider skips this check entirely, the
// same degradation every other optional Config field in this package uses.
type StoreCapacityProvider interface {
	StoreCapacity() (StoreCapacityReport, error)
}

// storeCapacityFindings raises store_near_capacity when the store's on-disk
// size meets or exceeds warnBytes ([retention] store_warn_bytes,
// HealthThresholds.StoreCapacityWarnBytes). warnBytes <= 0 disables the
// check entirely — an explicit config choice, never DefaultThresholds' own
// value, which is always positive.
func storeCapacityFindings(prov StoreCapacityProvider, warnBytes int64, db *debouncer) []Finding {
	if prov == nil || warnBytes <= 0 {
		return nil
	}
	rep, err := prov.StoreCapacity()
	if err != nil {
		// Can't measure this cycle (e.g. a transient stat() error racing a
		// WAL checkpoint) -> say nothing rather than risk a false breach;
		// the next cycle's fresh read decides. Debounce state is left
		// exactly as it was, matching every other producer's "no data this
		// cycle" handling in this package.
		return nil
	}

	breach := rep.SizeBytes >= warnBytes
	if !db.Evaluate("store", breach, storeCapacityRise, storeCapacityFall) {
		return nil
	}
	return []Finding{{
		ID:       "store:" + CheckStoreNearCapacity,
		Source:   SourceStore,
		Check:    CheckStoreNearCapacity,
		Severity: SeverityWarning,
		Detail: fmt.Sprintf("vnprox's own app store (vnprox.db, WAL included) is %s, at or above the configured warning threshold of %s ([retention] store_warn_bytes). This is vnprox's own app-owned data — audit history, changesets, snapshots, samples — never Proxmox's own config; see docs/deployment.md's sizing section for what to prune and docs/data-model.md's retention section for each table's policy. A store that keeps growing past this threshold risks filling the root filesystem it shares with pmxcfs and PVE's own writes.",
			humanBytes(rep.SizeBytes), humanBytes(warnBytes)),
		Nodes:    sortedUnique([]string{rep.Node}),
		DocsLink: storeNearCapacityDocsLink,
	}}
}

// humanBytes renders n as a short, binary-unit (KiB/MiB/GiB/...) size
// string — this finding describes a filesystem footprint, so binary units
// match what `df`/`du` and every other disk-space tool an operator already
// reaches for report.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
