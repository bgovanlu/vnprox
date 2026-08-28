# `flowIngestor`

See [plugin-development.md](../plugin-development.md) for the SDK overview,
the stage-only boundary, and the security section this page does not repeat.

## Interface (`internal/plugin/interfaces.go`)

```go
// FlowIngestor is the flow/telemetry ingestion extension point (T-1002 becomes
// pluggable). A plugin decodes one raw datagram from a flow exporter — a vendor
// export format the built-in NetFlow/IPFIX/sFlow decoders do not speak — into
// zero or more normalized flow.Record values. It is a pure read/decode seam: it
// is handed bytes and returns records, and has no access to the store, the
// change engine, or the network beyond the datagram it is given. node is the
// cluster node the datagram was observed on (stamped onto each Record.Node);
// src is the exporter's source address, for the ingestor's own template/context
// keying.
type FlowIngestor interface {
	// Ingest decodes one exporter datagram into normalized records. Returning a
	// nil slice with a nil error (e.g. a template-only packet that produces no
	// samples) is valid and expected. An error is logged and the datagram
	// dropped; it never propagates as a daemon fault.
	Ingest(ctx context.Context, node, src string, payload []byte) ([]flow.Record, error)
}
```

`flow.Record` (`internal/flow/record.go`) is the value you return per sample:
`Node`, `SrcIP`, `DstIP`, `SrcRef`/`DstRef` (populated by the host's resolver,
never by you), `Source`, `At`, `Bytes`, `Packets`, `SrcPort`, `DstPort`,
`Proto`, `Vlan`, ingress/egress interface indices.

Minimum capability to attach this point: `netRead`.

## What the host guarantees

- **You are handed exactly the bytes of one exporter datagram** — `node`
  (which cluster node's listener observed it) and `src` (the exporter's
  source address), never a socket, a listening port, or any network access
  of your own. The host owns the listener; you own the decode.
- **An error drops the datagram and is logged, nothing more.** A malformed
  or unrecognized datagram from a flaky exporter never becomes a daemon
  fault — it is exactly as if the datagram had been lost in transit.
- **A nil slice with a nil error is a normal, expected return** — e.g. a
  NetFlow/IPFIX template-definition packet that carries no flow samples of
  its own. Do not treat "nothing to report" as an error.
- **Every enabled `flowIngestor` plugin runs**, in id-sorted order
  (`plugin.Registry.FlowIngestors`); the host does not pick one winner among
  several plugins claiming to understand the same exporter.

## What the plugin must not do

- **Cannot open its own socket or listener.** The whole point of this seam
  is that vnprox owns the network-facing listener and hands you only the
  payload already received; opening a competing listener defeats the design
  and duplicates the built-in NetFlow/IPFIX/sFlow decoders' own binding.
- **Do not populate `SrcRef`/`DstRef` yourself.** Those fields are filled in
  by the host's own inventory resolver after your `Ingest` returns, by
  matching `SrcIP`/`DstIP` against the live inventory graph — a plugin that
  guesses at a `Ref` string risks producing one the resolver would never
  have generated, silently wrong.
- **Do not assume decode order or batching** across calls — each `Ingest`
  call is one datagram; correlate multi-packet exporter state (e.g. NetFlow
  templates) inside your own implementation's state, not by relying on the
  host to buffer or reorder calls for you.
- **Must decode defensively.** `payload` is attacker-reachable in the sense
  that it comes off the network from whatever is configured to export flow
  data to this node — treat it as untrusted input, the same posture the
  built-in decoders take.

## Minimal working example

From `internal/plugin/plugintest/samples.go` — the SDK's own fixture:

```go
type sampleFlowIngestor struct{}

// Ingest turns one datagram into a single deterministic record whose Bytes is
// the payload length — enough for a parity comparison across transports.
func (sampleFlowIngestor) Ingest(_ context.Context, node, src string, payload []byte) ([]flow.Record, error) {
	if node == "" {
		node = SampleNode
	}
	return []flow.Record{{
		Node:   node,
		SrcIP:  src,
		Source: flow.Source("plugin-sample"),
		Bytes:  int64(len(payload)),
		At:     1700000000,
		Proto:  6,
	}}, nil
}
```

Wire it into a `plugin.Manifest` declaring `plugin.ExtFlowIngestor` and
`netRead`, and a `plugin.Registration.FlowIngestor` field — the same shape
[`examples/plugin-template/`](https://github.com/bgovanlu/vnprox/tree/main/examples/plugin-template)
uses for `findingProducer`; swap the extension point and the implementation
field.
