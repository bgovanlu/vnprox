// Package reconcile executes T-2703's two symmetric answers to a drift
// finding, and executes them ONLY when an operator asks.
//
// # The split, and why it is a package boundary
//
// internal/drift decides what diverged and what could be done about it; this
// package does it. That is not layering for its own sake. The drift service
// runs on a 30-second timer, and the timer must never be able to reach a
// change engine or a git host — so the package the timer lives in holds
// neither. It holds values (a finding, two booleans, an op list looked up by
// id) and nothing that can act. Everything that can act lives here, behind
// methods only an HTTP handler calls.
//
// # The two actions
//
//	RestoreIntent  stages a DRAFT changeset carrying the ops that bring the
//	               cluster back to what the spec declares. It goes through the
//	               ordinary change engine — stage -> validate -> apply ->
//	               confirm — and this package can only do the first of those,
//	               because Stager has one method.
//	AdoptReality   opens a pull request against the spec repository proposing
//	               the document that matches the cluster (internal/gitsync's
//	               Proposer). It changes nothing about the cluster at all.
//
// Both produce a reviewable artifact and neither is ever taken automatically,
// at any severity. There is no timer here, no RunLoop, and no severity
// threshold above which something happens on its own — the absence is
// structural, not a policy that could be reconfigured.
//
// # What the seams deliberately omit
//
// Stager has Create and nothing else: no Apply, Confirm, Approve, Validate or
// Discard. A reconcile path that applied a changeset could not be written
// without editing that interface, which is a reviewable event — the same
// structural stance internal/gitsync's ChangesetStager and internal/mcp's take.
// Adopter has ProposeAdoption and Enabled: no merge, no approve, no poll,
// because the Host seam underneath it has no verb for any of them.
package reconcile
