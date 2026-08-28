package peer

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// maxBodyRetentionWindow bounds how long a seen signature is remembered by
// the replay cache. It must be at least ReplayWindow*2 so a signature can
// never age out of the cache while its timestamp is still inside the
// window a fresh copy of the same request would be accepted in — i.e. an
// attacker cannot wait out the cache to replay a still-valid-looking
// request.
const replayCacheTTL = 2 * ReplayWindow

// replayCache rejects an exact repeat of a previously-accepted request seen
// within the last replayCacheTTL. It holds one map, but two entry points
// with different rules (T-3703):
//
//   - seenBefore is the legacy check: authMiddleware's fallback path calls
//     it with the plain HeaderSignature, both to decide and to record.
//   - seenBeforeNonce is the nonce path's check-and-record: authMiddleware
//     calls it with a request's HeaderNonce (the sole basis for the
//     accept/reject decision) *and* its legacy HeaderSignature (recorded,
//     but never consulted for the decision). See that method's doc
//     comment for why recording a second key on the nonce path is what
//     closes the strip-the-nonce-and-replay gap, and why it must not
//     affect the decision itself.
//
// The comment this one replaces argued that the signature alone was a
// sufficient replay key because two requests could only collide on it "by
// being byte-identical (cryptographically infeasible otherwise)". That
// reasoning is correct about two *different* requests forging the same
// signature — genuinely infeasible without the secret — but says nothing
// about the *same* request legitimately recurring. Before T-3703 the
// signature covered only method+path+bodyHash+ts, all of which are
// identical across two polls of the same idempotent GET inside the same
// wall-clock second (ts is unix seconds): a poller with a sub-second duty
// cycle reproduces the exact same signature on every such pair, and the
// second, entirely legitimate request was rejected as a "replay" — ~2,885
// times a day on the deployed pvecube instance (see
// planning/reports/audit-2026-08-21-peer-replay.md). Keying the decision
// on a random 128-bit nonce instead makes two legitimate polls
// distinguishable (each mints its own nonce, so both are accepted) while
// still rejecting an actual replay (the same nonce presented twice) — the
// standard construction, and the one that makes the original
// "byte-identical" argument actually true: a signature collision without
// a secret is still cryptographically infeasible, and it is now also the
// *only* way a decision-relevant collision can happen, because the
// decision itself never turns on a signature that two legitimate requests
// can legitimately share.
type replayCache struct {
	seen map[string]time.Time
	mu   sync.Mutex
}

func newReplayCache() *replayCache {
	return &replayCache{seen: make(map[string]time.Time)}
}

// sweep deletes every expired entry. Caller must hold mu.
func (c *replayCache) sweep(now time.Time) {
	for k, exp := range c.seen {
		if !now.Before(exp) {
			delete(c.seen, k)
		}
	}
}

// seenBefore reports whether key was already accepted within the TTL and,
// if not, records it as seen. Also opportunistically sweeps expired
// entries so the map doesn't grow unboundedly under sustained traffic.
// This is the legacy-path check: authMiddleware's fallback calls it with
// HeaderSignature, unchanged from before T-3703. It is also what makes
// seenBeforeNonce's extra bookkeeping effective — a legacy-path request
// presenting a signature seenBeforeNonce already recorded lands here and
// is rejected by this same check, with no coordination required beyond
// sharing the map.
func (c *replayCache) seenBefore(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweep(now)
	if exp, ok := c.seen[key]; ok && now.Before(exp) {
		return true
	}
	c.seen[key] = now.Add(replayCacheTTL)
	return false
}

// seenBeforeNonce is the nonce path's check-and-record. The accept/reject
// decision is made on nonce alone — legacySig is never consulted for it —
// but a successful (non-replay) call also records legacySig, not just
// nonce, so that a later legacy-only presentation of the same signature
// (an attacker who captured this exact request and stripped its
// HeaderNonce/HeaderNonceSignature before replaying it) is caught by
// seenBefore's check rather than sailing through as "first-seen".
//
// legacySig must never gate this method's own decision, and that isn't
// an accident to be careful not to regress: two *legitimate* requests
// issued inside the same wall-clock second by a nonce-capable client
// carry the exact same legacySig (method+path+body+ts, all identical —
// this is the T-3703 bug in the first place) but two distinct nonces. If
// this method rejected on "legacySig already recorded", the second
// request would be refused again — reintroducing precisely the bug this
// task exists to fix. So the presence check below is nonce-only;
// legacySig is written, never read, here.
//
// A repeat legitimate poll therefore calls c.seen[legacySig] = ... a
// second (or Nth) time with the same value it already held modulo TTL —
// that's a refresh (pushes its expiry out to now+replayCacheTTL), not a
// decision, and refreshing it is correct: it keeps the strip-and-replay
// defense for that signature alive for as long as this client keeps
// polling, rather than letting it expire mid-poll-storm.
func (c *replayCache) seenBeforeNonce(nonce, legacySig string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweep(now)
	if exp, ok := c.seen[nonce]; ok && now.Before(exp) {
		return true
	}
	c.seen[nonce] = now.Add(replayCacheTTL)
	c.seen[legacySig] = now.Add(replayCacheTTL)
	return false
}

// errBodyTooLarge distinguishes an over-size body (413) from any other
// body-read failure (400) in the auth middleware.
var errBodyTooLarge = errors.New("peer: request body exceeds limit")

// readLimitedBody reads at most max+1 bytes from r.Body so a body exactly
// at the limit is accepted but anything larger is detected (and rejected)
// without buffering an unbounded amount of attacker-controlled data first.
func readLimitedBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	defer func() { _ = r.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errBodyTooLarge
	}
	return data, nil
}

// authMiddleware verifies every request against the cluster-secret HMAC
// scheme (docs/security.md "Transport") before next (the route handler)
// ever runs: missing signature headers, an unparsable or out-of-window
// timestamp, a body over the size cap, a signature computed with the wrong
// secret, and an exact replay of a previously-accepted request are all
// rejected with 401 (413 for an oversize body) and next is never called.
// SPA session cookies are never inspected here — the cluster secret is the
// only trust root for anything under /api/peer/*.
//
// Rolling-upgrade compatibility (T-3703), revised. The first version of
// this fix put the nonce inside HeaderSignature's own HMAC input (a
// single, nonce-bound signature). That's the textbook construction and it
// is exactly as secure as this one, but it has one deployment property
// that ruled it out here: pvecube's live peer, pve001, is *permanently*
// out of reach — this project has root SSH to pvecube and *no
// credentials for pve001 at all* — so "both ends upgrade together" is not
// a plan, it's a wish. Under the single-signature design, the moment
// pvecube's client started sending a nonce-bound HeaderSignature, every
// outbound call from pvecube to pve001 would fail pve001's (unmodified,
// unmodifiable) four-field check — breaking exactly the call path T-3702
// just fixed (pve001's consecutive_failures counter), via a different
// mechanism, for as long as pve001 exists. That is not an acceptable
// trade for fixing a WARN-level over-rejection.
//
// So the client (client.go's do()) now sends *two* signatures every time:
// HeaderSignature is always the plain, unchanged, pre-T-3703 four-field
// signature — the one header a build older than T-3703 already knows how
// to check — and HeaderNonceSignature is an additive, nonce-bound
// signature over HeaderNonce that only a T-3703-or-later verifier looks
// for. This is what pve001 needs: it validates HeaderSignature exactly as
// it does today and simply never notices the two headers it doesn't
// recognize, so pvecube's outbound calls to it keep working, unchanged,
// indefinitely.
//
// On the verifying side (this function, only reachable by a T-3703-or-
// later build):
//
//   - A request with a HeaderNonce + a HeaderNonceSignature that verifies
//     against the five-field formula is authenticated via the nonce, and
//     the accept/reject decision is made on the nonce alone
//     (seenBeforeNonce): two legitimate polls inside one wall-clock second
//     mint different nonces, so both pass, even though they share the
//     exact same HeaderSignature (method+path+body+ts, all identical —
//     this is the bug T-3703 fixes). But acceptance also *records*
//     HeaderSignature, not only the nonce — see the next paragraph for
//     why.
//   - Anything else — no nonce headers at all (an old peer, or this
//     node's own pre-T-3703 past), or a HeaderNonceSignature that fails to
//     verify — falls back to checking HeaderSignature against the legacy
//     four-field formula, and the decision is made by seenBefore against
//     that same signature. The identical-second collision this task
//     fixes simply isn't fixed on this path, by construction; it can't
//     be, since there is no trustworthy nonce to key on.
//
// The nonce path recording HeaderSignature (not just the nonce) on accept
// is what closes a gap the first version of this fix left open: without
// it, an attacker holding a captured, genuinely-nonce'd request could
// strip HeaderNonce/HeaderNonceSignature and resend just HeaderSignature+
// HeaderTimestamp+body — that stripped copy still verifies (HeaderSignature
// was never bound to the nonce), and would have looked "first-seen" on the
// legacy path, because the original acceptance had only touched the nonce
// half of the cache. That is a real replay pre-T-3703 code would have
// blocked (it recorded every accepted signature, unconditionally) and
// this design must not reopen it. seenBeforeNonce closes it by writing
// HeaderSignature into the same cache the legacy path reads, on every
// nonce-path acceptance — so the stripped replay lands on seenBefore,
// finds that signature already recorded, and is refused. The window is
// closed, not merely bounded. This is also exactly why the nonce path's
// own accept/reject decision must never consult that signature (see
// seenBeforeNonce's doc comment): doing so would reject the second of two
// legitimate same-second polls, reintroducing the original bug —
// TestAuthMiddleware_SameSecondDuplicateBothAccepted guards this, and
// TestAuthMiddleware_StrippedNonceReplayRejected guards the closed
// window.
//
// Nothing above applies once RequireNonce (ServerOptions) is true: a
// request with no usable nonce is refused outright, the legacy fallback
// (and therefore also the question of what it does or doesn't defend
// against) never runs. That's the switch to flip once every peer this
// daemon talks to is known to send a nonce — dual-format acceptance ends
// there. Nothing in this package flips it automatically.
//
// internal/peer/version.go's ProtocolVersion — the existing negotiation
// mechanism — is deliberately *not* used to gate any of this:
// CheckCompatible's GET /api/peer/version round trip is itself a request
// that must pass this same authMiddleware to be answered at all
// (MountRoutes mounts the whole /api/peer subtree, version route
// included, behind authMiddleware), so negotiating the signing format via
// a call that requires the signing format already agree is circular.
// Per-request, per-header verification (as above) needs no prior
// handshake and sidesteps that entirely.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tsHeader := r.Header.Get(HeaderTimestamp)
		sigHeader := r.Header.Get(HeaderSignature)
		nonceHeader := r.Header.Get(HeaderNonce)
		nonceSigHeader := r.Header.Get(HeaderNonceSignature)
		if tsHeader == "" || sigHeader == "" {
			s.rejectUnauthorized(w, r, "missing peer signature headers")
			return
		}

		ts, err := strconv.ParseInt(tsHeader, 10, 64)
		if err != nil {
			s.rejectUnauthorized(w, r, "malformed peer timestamp")
			return
		}

		now := s.opts.Now()
		skew := now.Unix() - ts
		if skew > int64(ReplayWindow.Seconds()) || skew < -int64(ReplayWindow.Seconds()) {
			s.rejectUnauthorized(w, r, "peer timestamp outside replay window")
			return
		}

		secret := s.opts.Secrets.Current()
		if len(secret) == 0 {
			s.rejectUnauthorized(w, r, "no cluster secret loaded")
			return
		}

		body, err := readLimitedBody(r, s.opts.MaxBodyBytes)
		if err != nil {
			if errors.Is(err, errBodyTooLarge) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "peer_request_too_large", "peer request body too large")
			} else {
				writeJSONError(w, http.StatusBadRequest, "peer_bad_request", "could not read peer request body")
			}
			return
		}
		// Handlers still need the body: replace it with a fresh reader over
		// the bytes already consumed for hashing.
		r.Body = io.NopCloser(bytes.NewReader(body))

		requestURI := r.URL.RequestURI()

		// nonceOK is true only for a request whose HeaderNonceSignature
		// actually verifies against the five-field formula bound to
		// HeaderNonce — see authMiddleware's doc comment for why arriving
		// at "no usable nonce" this way (rather than just checking
		// nonceHeader != "") is what keeps a tampered nonce from being
		// treated any differently than a missing one.
		nonceOK := nonceHeader != "" && nonceSigHeader != "" &&
			verifySignature(secret, r.Method, requestURI, body, ts, nonceHeader, nonceSigHeader)

		// Replay checks run only after signature verification, so an
		// attacker probing with garbage signatures/nonces can never
		// poison the cache or observe cache state through timing.
		switch {
		case nonceOK:
			// The T-3703 fix: the decision is made on nonce alone (never
			// sigHeader — see seenBeforeNonce's doc comment for why), so
			// two distinct nonces let two legitimately identical-within-
			// one-second polls both through. sigHeader is also recorded
			// here (not just nonceHeader), which is what makes the legacy
			// path below refuse a copy of this same request with its
			// nonce headers stripped — see authMiddleware's doc comment.
			if s.replay.seenBeforeNonce(nonceHeader, sigHeader, now) {
				s.rejectReplay(w, r, "nonce")
				return
			}
		case s.opts.RequireNonce:
			// The switch is flipped: a request with no usable nonce is
			// refused outright, never falling back to the legacy check
			// below — see ServerOptions.RequireNonce and authMiddleware's
			// doc comment.
			s.rejectUnauthorized(w, r, "peer request missing a valid nonce")
			return
		default:
			// Lenient (default) mode: fall back to the pre-T-3703
			// four-field signature and its signature-keyed replay cache.
			// Also catches a nonce-path request replayed with its nonce
			// headers stripped, since seenBeforeNonce above already
			// recorded sigHeader on the original, genuine acceptance —
			// see authMiddleware's doc comment.
			if !verifySignature(secret, r.Method, requestURI, body, ts, "", sigHeader) {
				s.rejectUnauthorized(w, r, "invalid peer signature")
				return
			}
			if s.replay.seenBefore(sigHeader, now) {
				// T-3712: this is the path an older, pre-T-3703 peer's own
				// client-side duplicate-poll bug lands on — see
				// rejectReplay's doc comment for why it must not be logged
				// with rejectUnauthorized's wording.
				s.rejectReplay(w, r, "legacy")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) rejectUnauthorized(w http.ResponseWriter, r *http.Request, reason string) {
	s.opts.Logger.Warn("peer: rejected unauthorized request",
		"method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr, "reason", reason)
	writeJSONError(w, http.StatusUnauthorized, "peer_unauthorized", "invalid or missing peer signature")
}

// rejectReplay logs and answers a request that failed *only* the replay
// check — never the signature or timestamp check — so by construction the
// request already carried a valid HMAC over the live cluster secret
// (verifySignature succeeded, either via the nonce-bound formula or the
// legacy four-field one, before either call site below is reached). Its
// origin is therefore a KNOWN, authenticated cluster peer, not an unknown
// or unauthenticated one.
//
// T-3712 found that logging this exact case with rejectUnauthorized's
// wording ("peer: rejected unauthorized request") is what let a real
// client-side bug — nothing deduped peer reads, so two consumers on
// overlapping timers sent the same signed request within the same replay
// window — hide in plain sight for a week: ~2,885 rejections/day on
// pvecube, all from an authenticated peer's own legitimate (if redundant)
// traffic, read by every on-call skim as "someone is attacking us" and
// dismissed as security noise rather than investigated
// (planning/tasks/T-3712-duplicate-peer-neighbor-polls.md). via names
// which check accepted the request's signature before the replay cache
// rejected it: "nonce" for the T-3703 path (genuinely the same nonce
// presented twice — the one case that actually can be an attempted
// replay), or "legacy" for the pre-T-3703 four-field path (which cannot
// distinguish a genuine replay from two legitimate same-second polls at
// all — see authMiddleware's doc comment). So the two are distinguishable
// from each other in the journal too, not just from an auth failure.
func (s *Server) rejectReplay(w http.ResponseWriter, r *http.Request, via string) {
	s.opts.Logger.Warn("peer: rejected replayed request from an authenticated peer",
		"method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr, "via", via)
	writeJSONError(w, http.StatusUnauthorized, "peer_unauthorized", "invalid or missing peer signature")
}
