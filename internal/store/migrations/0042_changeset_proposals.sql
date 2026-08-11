-- 0042_changeset_proposals.sql — T-2702 "Changeset → pull request".
--
-- A changeset proposed against the spec repository records WHERE it was
-- proposed and WHAT was opened, so the review surface can link the pull
-- request and a second propose can update that request instead of opening a
-- second one (T-2702 AC4).
--
-- Why a side table rather than columns on `changesets`: this is app-owned
-- bookkeeping about an EXTERNAL object, written by exactly one path
-- (internal/gitsync's Proposer) and read by the review surface. Keeping it
-- out of the changesets row means the ordinary changeset UPDATE — which
-- rewrites status/ops/plan on every lifecycle step — can never clobber it,
-- the same separation-of-writers reasoning 0033's revert-ticket columns
-- document from the other direction.
--
-- ONE ROW PER CHANGESET, by primary key. That is the AC4 invariant expressed
-- in the schema: proposing twice cannot accumulate proposals, because there
-- is nowhere to put a second one.
--
--   changeset_id  the proposed changeset (PK; no FK, matching the rest of
--                 this schema's convention of not cascading app-owned
--                 bookkeeping off the changesets table).
--   remote        credential-free description of the repository, exactly as
--                 `GET /gitsync/status` renders it. Never a URL with
--                 userinfo: the source refuses those at construction.
--   branch        the branch the spec commit was pushed to.
--   path          the spec document's path within the repository.
--   commit_sha    the commit the proposal's content landed as ('' when the
--                 branch already carried byte-identical content and no new
--                 commit was needed).
--   pr_id         the host's own identifier (GitHub pull number, GitLab
--                 merge-request iid), stored as text so neither host's shape
--                 leaks into the schema.
--   pr_url        the human-facing page the review surface links to.
--   proposed_by   the acting user, for the audit trail.
--
-- Nothing here is or contains a credential: the push token has no
-- representation in this table, and no writer of it ever sees one.
--
-- Migrations are forward-only: once released, never edit this file.

CREATE TABLE IF NOT EXISTS changeset_proposals (
    changeset_id TEXT PRIMARY KEY,
    remote       TEXT NOT NULL,
    branch       TEXT NOT NULL,
    path         TEXT NOT NULL,
    commit_sha   TEXT NOT NULL DEFAULT '',
    pr_id        TEXT NOT NULL DEFAULT '',
    pr_url       TEXT NOT NULL DEFAULT '',
    proposed_by  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
