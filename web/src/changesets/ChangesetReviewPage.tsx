// T-2003's shareable review link target: /changesets/:id/review
// (App.tsx). Opens a changeset directly in the review screen without
// requiring the visitor to already have it "active" in their drawer — a
// colleague following a shared link (docs/api.md's changesets section; the
// exit demo's "a colleague reviews and comments on the resulting changeset
// from a phone") only has the id, not an in-progress drawer session. Still
// requires an authenticated session (RequireAuth, App.tsx) — the link
// carries no credential of its own (reviewLink.ts's doc comment).
//
// Deliberately does NOT render its own <ReviewApplyScreen/>: the drawer
// (ChangesetDrawer.tsx, mounted once app-wide in AppShell) is the single
// place that ever renders one, and it already supports being driven to any
// changeset + review state via useChangesetDrawerStore (the same store
// T-909's narrow-viewport work already made reachable from a phone for an
// already-active draft). Rendering a second, independent ReviewApplyScreen
// here would risk two simultaneous review dialogs for the same changeset
// whenever a visitor already has it active in their own drawer — this page
// instead sets the shared store's activeId + requests review, and lets the
// one drawer instance take it from there.
import { useEffect } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useChangesetDrawerStore } from "./store";

export function ChangesetReviewPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const openReview = useChangesetDrawerStore((s) => s.openReview);

  useEffect(() => {
    if (!id) return;
    setActiveId(id);
    openReview();
    // Hand off to the drawer immediately — nothing left for this route to
    // render once the store is set, and staying on /changesets/:id/review
    // would otherwise leave a dead page behind the drawer's modal overlay.
    void navigate("/", { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- run once per changeset id
  }, [id]);

  return null;
}
