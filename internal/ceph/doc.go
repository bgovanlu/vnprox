// Package ceph implements T-1503's Ceph network awareness: reading PVE's
// own knowledge of its Ceph public/cluster network declaration and per-OSD
// node placement (internal/pve.Client.CephConfig/CephOSDs — no new Ceph API
// client, no new credentials, the same "read the owning system's own
// knowledge of itself" boundary T-1206's PBS awareness pattern establishes
// for its domain), attributing which physical bond/NIC each OSD-hosting
// node's public/cluster traffic rides (Project, reusing
// internal/topology.ResolvePhysicalPath rather than a second path walker),
// and raising the three classic Ceph-networking footguns (CorosyncSharedLink,
// ClusterMTUMismatch, SingleNIC).
//
// READ-ONLY INVARIANT: this package contains no write path of any kind.
// pve.Client.CephConfig/CephOSDs (internal/pve/ceph.go) issue exclusively
// GET requests; nothing in this package, internal/change, or anywhere else
// in this codebase defines a `ceph.*` changeset op — PVE's own Ceph tooling
// (`pveceph`, the GUI's Ceph panel) keeps permanent, sole ownership of Ceph
// configuration. See this package's own regression test
// (TestRegression_NoWriteMethodsAnywhere) and internal/change's op-group
// grep-verifiable absence, both cited in this task's completion report.
//
// Traffic attribution (classifying a flow.Record as "ceph-public" or
// "ceph-cluster") is NOT implemented here: T-1503's card is explicit that
// this package supplies T-1504's flow.Classifier with Ceph's declared CIDRs
// via the generic flow.NewCIDRSource(flow.NetworkSourceKindCeph, ...)
// constructor (cmd/vnproxd's composition root calls it directly with this
// package's Status.PublicNetwork/ClusterNetwork) — it does not implement a
// second classification engine.
package ceph
