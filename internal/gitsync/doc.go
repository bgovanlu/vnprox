// SPDX-License-Identifier: Apache-2.0

// Package gitsync implements T-2701's git-backed spec sync: a repository
// becomes the source of *intent* for a cluster's declarative network spec
// (internal/spec), while Proxmox remains the source of *truth*.
//
// # The invariant
//
// Divergence between the repository and the live cluster is resolved by
// **opening a draft changeset a human reviews**, never by this daemon
// deciding the file wins. Sync stages and stops. That is enforced
// structurally, not by convention: the only change-engine surface this
// package holds is ChangesetStager (service.go), which has no Apply,
// Confirm, Rollback or Discard method at all — the same interface-surface
// boundary internal/mcp (T-1701) and internal/plugin (T-1702) already draw.
// An applying gitsync code path cannot be written without changing the type.
//
// # The git-access decision (T-2701, risk register)
//
// The arc's risk register asks for this to be decided explicitly rather than
// discovered in go.mod. Three options were sized:
//
//  1. **Shell out to the `git` binary.** Rejected. `packaging/debian/
//     control.tmpl` declares *no* `Depends:` at all and only
//     `Recommends: lldpd, ifupdown2`; git is not part of a Proxmox VE base
//     install, so this would add the first hard runtime dependency the .deb
//     has ever had. It would also add a subprocess whose argv carries an
//     operator-supplied remote URL, into a codebase whose security model
//     (docs/security.md, "Host footprint") enumerates every external command
//     and pins each to a fixed argv — and git remote URLs are a known
//     argument-injection surface (`--upload-pack=`, `ext::`, ssh
//     `ProxyCommand`). A read-only fetch of one file does not justify either
//     cost.
//
//  2. **A Go git library (go-git).** Rejected. It is precisely the "large
//     dependency" the risk register names — a module graph an order of
//     magnitude bigger than the requirement, carrying pack negotiation,
//     delta resolution, worktrees and a filesystem abstraction to fetch one
//     file. docs/development.md's dependency table does not list it and
//     "prefer stdlib" is the standing rule.
//
//  3. **A plain HTTPS fetch of one file at one ref, over net/http.** Chosen.
//     It is exactly the requirement, adds no dependency and no runtime
//     binary, is cancellable and testable against an httptest server, and
//     reuses the credential and TLS story the daemon already has.
//
// The consequence is stated rather than hidden: with no git object protocol
// there is no local object graph, so signature verification operates on the
// commit object the host reports for the resolved ref (source.go's
// CommitSignature), and vnprox cannot itself prove that the fetched file
// content is the blob under that commit's tree. See the "Residual" note on
// VerifyCommit in sshsig.go. What vnprox *does* prove is cryptographic: the
// commit object was signed by a key the operator listed in their own
// allowed-signers file, verified locally, never trusted from the host's own
// "verified: true" boolean.
//
// # Shape
//
//   - Source (source.go) resolves a ref to a Revision{SHA, Content,
//     Signature}. HTTPSource speaks GitHub's and GitLab's REST read surfaces
//     and a generic raw-file layout; nothing else in this package knows which.
//   - Service (service.go) polls: fetch → verify → spec.Parse → spec.Import →
//     open or update the one draft. It never applies.
//   - Everything is off until an operator sets `[gitsync] enabled = true`
//     with a URL: a disabled Service contacts no endpoint and writes nothing.
//
// # The write half (T-2702)
//
// The other direction — a changeset staged in the GUI becoming a pull request
// against the same repository — is a SIBLING of the read path, never an
// extension of it:
//
//   - Host (host.go) is the write seam: branches, one file commit, and open
//     or update a pull request. It has no merge, approve or poll verb,
//     because vnprox opens a request and stops.
//   - Proposer (propose.go) renders a changeset into the document
//     (spec.ApplyOps), CHECKS that the result re-imports to exactly that
//     changeset's ops before writing anything, and orders its host calls so a
//     failure never leaves an orphan branch.
//   - Source and Host are separate types with separate credentials
//     ([gitsync] token_file and push_token_file). The sync Service holds only
//     a Source, so an applying-or-pushing sync path cannot be written without
//     editing an interface — the same structural stance ChangesetStager takes
//     towards apply.
//   - The write half is off until an operator sets push_token_file, quite
//     separately from enabling the sync.
package gitsync
