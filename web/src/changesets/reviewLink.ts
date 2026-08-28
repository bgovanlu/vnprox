// SPDX-License-Identifier: Apache-2.0

// T-2003's shareable review link: a stable, bookmarkable/copyable URL that
// opens a changeset directly in review mode (ChangesetReviewPage.tsx, routed
// at /changesets/:id/review in App.tsx). Opening it still requires an
// authenticated session with netRead — the link carries no credential of
// its own (RequireAuth gates the route exactly like every other page), so
// sharing it is only ever as risky as sharing any other in-app URL.
export function reviewLinkFor(changesetId: string, origin: string = window.location.origin): string {
  return `${origin}/changesets/${encodeURIComponent(changesetId)}/review`;
}
