// Package procshim is T-1702's out-of-process plugin transport: the adapter that
// lets a plugin implement the exact same extension interfaces as an in-process
// plugin, but from a separate OS process vnproxd spawns and supervises.
//
// # Why this is the "grpc" transport, implemented over stdlib framing
//
// The task card names an out-of-process "gRPC" option with a `.proto` mirroring
// each Go interface. CLAUDE.md's standard-library-first rule and the hard
// make-check bar (govulncheck over the whole dependency tree) make pulling in
// google.golang.org/grpc + protoc-generated code a poor trade for what the
// boundary actually needs: a supervised subprocess, a typed request/response for
// each interface method, and graceful degradation when the process dies. So the
// wire contract is specified as proto3 messages (wire.proto) but carried as
// length-delimited proto3-JSON over the subprocess's own stdin/stdout — no
// HTTP/2 runtime, no new module. The Manifest transport is still recorded as
// "grpc" (the out-of-process class); the report states this substitution
// explicitly as the card's one deviation.
//
// # Shape
//
//   - Server (server.go) is the guest side: a plugin binary's main() calls
//     Serve(rwc, Impls) with its concrete implementations; Serve reads framed
//     requests, dispatches to the matching implementation, and writes framed
//     responses until the pipe closes.
//   - Host (client.go) is the vnprox side: it starts the subprocess, owns the
//     pipe, and exposes typed adapters (SwitchDriver/FlowIngestor/... ) that each
//     satisfy the identical plugin interface by forwarding one framed call. A
//     dead subprocess surfaces as an ordinary error from the adapter method, so
//     the registry's graceful-degradation path (a skipped tile/finding pack)
//     handles it without crashing vnproxd (T-1702 AC5).
//
// The subprocess is never handed a DB handle, a file path, or any host object —
// only the pipe. Its residual risk (unconstrained OS-level network egress from
// its own process) is stated in the report, not engineered away, mirroring
// T-1205's stated-not-hidden residual-risk pattern.
package procshim
