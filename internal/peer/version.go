package peer

// ProtocolVersion is this build's peer wire-protocol version. It is
// intentionally independent of the daemon's human-readable release version
// (cmd/vnproxd's -ldflags-injected `version`) and of internal/store's
// SQLite schema version: it exists solely to let two vnproxd builds detect
// an incompatible peer API shape (docs/architecture.md §5: "Version skew:
// peers exchange versions; a daemon refuses to coordinate changes involving
// a peer with an incompatible schema version") without having to parse or
// compare semver release strings. Bump it whenever a change to this
// package's wire format (request/response shapes, signature scheme) would
// break a peer running the previous value.
const ProtocolVersion = 1

// VersionInfo is GET /api/peer/version's response body and the payload
// Client.CheckCompatible compares against ProtocolVersion.
type VersionInfo struct {
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocolVersion"`
}
