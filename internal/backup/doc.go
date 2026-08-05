// Package backup implements vnprox's backup, restore and disaster-recovery
// path (T-1901): a versioned, integrity-checked archive of the daemon's own
// app-owned state, and an atomic, defensively-parsed restore of it.
//
// What it protects. vnprox never owns network configuration — that is
// Proxmox's, in /etc/network/interfaces and /etc/pve, and nothing here goes
// near it. What only vnprox holds is its SQLite store: every changeset and
// diff, every pre/post rollback snapshot, the whole audit trail, layouts,
// tenants and blueprint state. Those are exactly the artifacts an operator
// wants *after* an incident, and until this package existed there was no
// way to keep them off the box.
//
// The three properties the rest of the package exists to hold:
//
//   - **Key material is opt-in and loud.** A default backup contains the
//     store, which is full of AES-256-GCM ciphertext and one-way hashes,
//     and does NOT contain /etc/vnprox/keys/session.key. Such an archive is
//     safe to store anywhere and useless for reading a credential.
//     --include-keys produces the opposite — a complete compromise of every
//     credential this installation holds — so it warns naming every class
//     (secrets.go's declared inventory), marks the manifest, and suffixes
//     the filename.
//   - **On restore the archive is untrusted input.** archive.go parses it
//     hostilely: allowlisted entry names, regular files only, absolute
//     streamed byte/count budgets, and an exact match to a manifest read
//     first — all in a pass that writes nothing to disk.
//   - **Restore is atomic or it is worthless.** restore.go decides
//     everything (liveness, downgrade, digests, forward migration) against
//     a staged copy beside the target, and only then swaps by two renames
//     within one directory, putting the previous store back if the second
//     fails.
//
// Reuse. collect.go's Collector seam — a collector declares which secret
// classes its output can contain, and writes only through a Staging area
// that digests everything — and archive.go's container are deliberately
// shared machinery. T-1902's support bundle is structurally this with a
// harsher collection policy and a narrower scope, and should add collectors
// rather than a second archive format.
package backup
