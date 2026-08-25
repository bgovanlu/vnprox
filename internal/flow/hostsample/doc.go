// Package hostsample implements T-1004's host-local flow sampling: two
// samplers for nodes that have no external sFlow/NetFlow/IPFIX exporter
// pointed at them, both feeding the exact same internal/flow ring store
// T-1002 built (flow.Service.Ingest -> store.FlowSampleRepo's flow_samples
// table) — the flow explorer and map painting need no awareness of which
// source produced a given flow.Record, only its Source field
// (flow.SourceConntrack here; sFlow/NetFlow/IPFIX are internal/flow's own
// concern).
//
// # Both samplers are opt-in, per node, off by default
//
// Matching T-1002's own [flows] listener convention (docs/architecture.md
// §2/§7's "everything is cluster-aware" rule): neither sampler starts
// unless explicitly enabled in vnprox.toml's [flows] section on that
// specific node — conntrack_sampling_enabled / ebpf_sampling_enabled, both
// false by default. cmd/vnproxd only registers a sampler's Run loop with
// the daemon's supervised run group when its own config flag is set; this
// package itself never decides to start on its own.
//
// # conntrack.go / conntrack_netlink_linux.go: netlink conntrack polling
//
// Periodically reads and diffs the kernel's connection-tracking table via
// the netlink conntrack socket (netlink.ConntrackTableList,
// NewNetlinkConntrackReader) — true by default on any node running PVE's
// firewall or SDN NAT/masquerade zones has the nf_conntrack module loaded.
// This used to read /proc/net/nf_conntrack directly; T-3711 found that path
// does not exist on PVE 9 kernels (CONFIG_NF_CONNTRACK_PROCFS=n) even
// though the module is loaded and netlink works fine, and switched the
// default reader to netlink (NewFileConntrackReader remains as a secondary
// text-format path — see ConntrackReader's doc comment). Reading the
// netlink conntrack table needs CAP_NET_ADMIN; this works with the
// capabilities vnproxd already holds — packaging/systemd/vnprox.service's
// CapabilityBoundingSet already grants CAP_NET_ADMIN for internal/host's
// own rtnetlink link/address work, no new capability needed — see this
// package's ebpf.go for the higher-fidelity, higher-privilege alternative.
// Poll interval: [flows] host_sample_interval_sec, default 10
// (DefaultHostSampleInterval) — coarser than T-1002's live UDP ingestion by
// design, since conntrack sampling is inherently a periodic snapshot/diff,
// not a per-packet stream. A genuinely unavailable conntrack interface
// (no CAP_NET_ADMIN, or no netlink conntrack support at all) is reported
// once and stops the sampler (ConntrackSampler.Run) rather than retrying
// forever — see ErrConntrackUnavailable.
//
// Attribution to a particular bridge/subnet is not done inside this
// sampler at all: every Record it emits is fed through the same
// flow.Service.Ingest -> flow.ResolveRecord path every UDP-sourced Record
// goes through, so SrcRef/DstRef (and therefore "which bridge") are filled
// in by the one shared flow.GraphResolver already indexing bridge/SDN
// subnet CIDRs from the live inventory graph — never a second, sampler-
// private resolution scheme.
//
// # ebpf.go: kernel-feature probe + (future) per-bridge sampler
//
// The eBPF-based sampler promises better fidelity (per-packet observation
// via a bridge-attached BPF program, rather than a periodic conntrack
// counter snapshot) at a real cost: it needs CAP_BPF and CAP_PERFMON
// (Linux 5.8+; CAP_PERFMON did not exist before that release, and
// CAP_SYS_ADMIN was the only way to load BPF programs on older kernels —
// not a capability this package ever requests), plus BTF (CO-RE) support at
// /sys/kernel/btf/vmlinux for a kernel-version-portable program. Both are
// capabilities beyond the six currently in packaging/systemd/vnprox.
// service's CapabilityBoundingSet (docs/security.md's Host footprint
// section documents this): the systemd unit only grants them when [flows]
// ebpf_sampling_enabled = true is set at install/upgrade time (packaging/
// debian/postinst), never unconditionally.
//
// The real attachment code lives behind the "ebpf" Go build tag
// (ebpf.go); a binary built without -tags ebpf (the default `go test
// ./...`/`make build` matrix — see the Makefile's test target comment)
// compiles ebpf_stub.go instead, whose Probe always fails with a clear
// "not compiled into this binary" reason. Either way, Probe failing (build
// without the tag, or a kernel/capability check failing even with the tag)
// logs a structured slog warning naming the missing capability/feature and
// the daemon falls back to conntrack-only (or fully disabled, if conntrack
// sampling is also unset) sampling — never a fatal error. This package does
// not vendor a third-party eBPF loader (e.g. cilium/ebpf): actual BPF
// program attachment is out of scope for this task (flagged in
// planning/reports/T-1004.md) pending that dependency decision; the probe
// and build-tag scaffolding are real, the attachment step is not yet
// implemented even when the probe would otherwise pass.
package hostsample
