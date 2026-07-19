-- 0011_k8s_clusters.sql — T-1501 "Kubernetes overlay mapping engine":
-- app-owned registration intent only. Per CLAUDE.md's storage rule /
-- docs/architecture.md §7's new-domain invariant, vnprox persists which
-- k8s clusters to poll and how to authenticate to them — never a shadow
-- copy of the cluster's own live state (nodes/pods/services/overlay are
-- always recomputed fresh by GET /k8s/{clusterId}/overlay, exactly the
-- same boundary T-1401/T-1403/T-1406 already established for their own
-- domains). Kubernetes integration is READ-ONLY FOREVER (docs/roadmap-
-- universal.md's Phase 15 Invariants section): this table holds no field
-- that could ever back a write to the cluster itself.
--
-- `kubeconfig_enc` is AES-256-GCM ciphertext (nonce||ciphertext||tag), the
-- IDENTICAL session-secret encryption-at-rest primitive
-- sessions.pve_ticket_enc / alert_rules.target_secret_enc /
-- wireguard_tunnels.private_key_enc / ingress_targets.credential_enc use —
-- see internal/store/cipher.go's SessionCipher, reused here, NOT a second
-- cipher or key pair. It holds the entire parsed kubeconfig's credential
-- material (bearer token or client cert+key) sealed as one blob; it is
-- never returned by any API response — GET /k8s/clusters only ever
-- reports whether a cluster has usable credential material stored.
--
-- `cni_detected`/`status` are the last poll's own best-effort results
-- (internal/k8s.CNI value / "ok"|"unreachable"|"auth_failed"|"unpolled"),
-- cached here purely so GET /k8s/clusters can render a summary without a
-- live poll on every list call — GET /k8s/{clusterId}/overlay itself
-- always recomputes fresh, never trusting this cache as authoritative.
--
-- Migrations are forward-only: once released, never edit this file; a
-- schema change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS k8s_clusters (
  id             TEXT PRIMARY KEY,   -- ULID
  name           TEXT NOT NULL,
  kubeconfig_enc BLOB NOT NULL,      -- AES-256-GCM ciphertext of the full parsed kubeconfig
  added_by       TEXT NOT NULL,
  added_at       INTEGER NOT NULL,
  cni_detected   TEXT NOT NULL DEFAULT '',   -- last poll's internal/k8s.CNI value, '' = never polled
  status         TEXT NOT NULL DEFAULT 'unpolled'
);
