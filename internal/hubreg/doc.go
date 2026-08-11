// Package hubreg is T-2803's *registry* side of the T-1705 hub: the signed
// index format, the publisher/reviewer tooling that produces it, and the
// client-side gate that verifies it and enforces revocation.
//
// Boundary — read this before adding anything here:
//
//   - internal/hub is the registry **client** and is deliberately untouched by
//     this package (T-2803 AC1: "the existing client consumes the real index
//     unmodified — if the client needs changing, the index format is wrong").
//     Everything here is shaped to fit that client, never the other way round.
//     Document.HubIndex returns a hub.Index built from hub.Entry values, so the
//     generator and the client cannot drift apart without a compile error.
//   - There is no registry *service*. The index is a static, signed JSON file
//     served from object storage or GitHub Pages next to a tree of static
//     artifact files — the same "nothing to operate" posture T-2102 chose for
//     the apt repository. `vnproxctl hub publish|index|revoke|verify` is the
//     whole server side (see docs/hub-registry.md).
//   - This package makes no trust decision about an *artifact*. An artifact's
//     Ed25519 signature and the per-installation trust store remain the only
//     install gate (internal/api's importBundleCore / installPluginCore, over
//     blueprint.VerifyBundle / blueprint.VerifySignature). A signed index says
//     only "this catalog is what the registry published"; it never promotes an
//     artifact to trusted.
//
// What the index signature *does* buy, given artifacts are signed already:
//
//   - Catalog integrity: a tampered or truncated index fails verification
//     wholesale instead of partially loading (AC5), so an attacker in front of
//     the hosting cannot silently remove, downgrade, or re-point entries.
//   - Offline revocation (AC3): revocations ride inside the same signed
//     document, so refusing a revoked artifact needs no second endpoint, no
//     OCSP-style live call, and no network access at all beyond the index the
//     client already fetched. Gate enforces this from its cached document.
package hubreg
