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

// replayCache rejects an exact repeat of a previously-accepted signature
// seen within the last replayCacheTTL. Because the signature already
// covers method+path+body+timestamp, two requests can only collide on it
// by being byte-identical (cryptographically infeasible otherwise), so
// keying purely on the signature string is sufficient to catch "replay the
// exact same signed request" without needing a separate nonce.
type replayCache struct {
	seen map[string]time.Time
	mu   sync.Mutex
}

func newReplayCache() *replayCache {
	return &replayCache{seen: make(map[string]time.Time)}
}

// seenBefore reports whether sig was already accepted within the TTL and,
// if not, records it as seen. Also opportunistically sweeps expired
// entries so the map doesn't grow unboundedly under sustained traffic.
func (c *replayCache) seenBefore(sig string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, exp := range c.seen {
		if !now.Before(exp) {
			delete(c.seen, k)
		}
	}

	if exp, ok := c.seen[sig]; ok && now.Before(exp) {
		return true
	}
	c.seen[sig] = now.Add(replayCacheTTL)
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
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tsHeader := r.Header.Get(HeaderTimestamp)
		sigHeader := r.Header.Get(HeaderSignature)
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

		if !verifySignature(secret, r.Method, r.URL.RequestURI(), body, ts, sigHeader) {
			s.rejectUnauthorized(w, r, "invalid peer signature")
			return
		}

		// Replay check runs only after the signature is confirmed valid, so
		// an attacker probing with garbage signatures can never poison the
		// cache or observe cache state through timing.
		if s.replay.seenBefore(sigHeader, now) {
			s.rejectUnauthorized(w, r, "replayed peer request")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) rejectUnauthorized(w http.ResponseWriter, r *http.Request, reason string) {
	s.opts.Logger.Warn("peer: rejected unauthorized request",
		"method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr, "reason", reason)
	writeJSONError(w, http.StatusUnauthorized, "peer_unauthorized", "invalid or missing peer signature")
}
