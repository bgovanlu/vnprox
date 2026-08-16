package auth

import (
	"context"
	"errors"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// RunRenewalLoop drives ticket renewal and hourly capability re-derivation
// for every session live in this process (docs/security.md: "vnproxd
// renews at ~1h30 while the session is active"; "re-derived hourly"). It
// ticks every s's TicketRenewCheckInterval (default 1 minute) and returns
// when ctx is cancelled — the signature matches cmd/vnproxd's runGroup
// actor type, so wiring it in is `g.add(authSvc.RunRenewalLoop)`.
//
// A renewal failure (the PVE ticket could not be refreshed — e.g. the
// user's password was changed, or PVE itself is unreachable) invalidates
// the session immediately: it is deleted from both the store and this
// process's live-session map, so a subsequent SessionMiddleware lookup
// reports "not authenticated" rather than serving a stale or broken
// session (T-105 acceptance criterion 5).
func (s *Service) RunRenewalLoop(ctx context.Context) error {
	ticker := time.NewTicker(s.renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.renewAndRefreshAll(ctx)
		}
	}
}

func (s *Service) renewAndRefreshAll(ctx context.Context) {
	s.sweepExpired(ctx)
	s.mu.Lock()
	ids := make([]string, 0, len(s.live))
	for id := range s.live {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		s.renewAndRefreshOne(ctx, id)
	}
}

func (s *Service) renewAndRefreshOne(ctx context.Context, sessionID string) {
	s.mu.Lock()
	live, ok := s.live[sessionID]
	s.mu.Unlock()
	if !ok {
		return // logged out / expired concurrently.
	}

	// T-2905: a session past the 12h hard cap is DROPPED, never renewed.
	// Before this check the loop happily kept calling identity.Renew forever
	// — a live PVE credential held past the documented hard lifetime, for a
	// session the middleware would refuse anyway on its next request.
	if rec, err := s.sessions.Get(ctx, sessionID); err == nil {
		if s.now().Unix()-rec.CreatedAt > int64(s.hardTimeout.Seconds()) {
			s.log.Info("auth: session reached its hard lifetime cap, dropping instead of renewing", "session_id", logSessionID(sessionID))
			s.invalidate(ctx, sessionID)
			return
		}
	}

	ticket, csrf, err := live.identity.Renew(ctx)
	if err != nil {
		s.log.Warn("auth: ticket renewal failed, invalidating session", "session_id", logSessionID(sessionID), "error", err)
		s.invalidate(ctx, sessionID)
		return
	}

	rec, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		// Session was deleted (logout, expiry sweep) concurrently with
		// this renewal tick — nothing left to update.
		s.mu.Lock()
		delete(s.live, sessionID)
		s.mu.Unlock()
		return
	}
	rec.PVETicket = ticket
	rec.CSRFToken = csrf

	now := s.now()
	if now.Sub(live.lastCapRefresh) >= s.capRefresh {
		caps, err := s.deriveCapabilities(ctx, live.identity)
		if err != nil {
			s.log.Warn("auth: hourly capability re-derivation failed, keeping previous capabilities", "session_id", logSessionID(sessionID), "error", err)
		} else if capsJSONStr, err := capsJSON(caps); err != nil {
			s.log.Error("auth: encoding refreshed capabilities", "session_id", logSessionID(sessionID), "error", err)
		} else {
			rec.CapsJSON = capsJSONStr
			s.mu.Lock()
			live.lastCapRefresh = now
			s.mu.Unlock()
		}
	}

	if err := s.sessions.Update(ctx, rec); err != nil {
		s.log.Error("auth: persisting renewed session", "session_id", logSessionID(sessionID), "error", err)
	}
}

// invalidate removes sessionID from both the store and the in-memory live
// registry, so any concurrent or subsequent SessionMiddleware lookup
// reports "not authenticated" instead of serving a session whose PVE
// ticket could no longer be renewed.
func (s *Service) invalidate(ctx context.Context, sessionID string) {
	if err := s.sessions.Delete(ctx, sessionID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("auth: deleting session after failed renewal", "session_id", logSessionID(sessionID), "error", err)
	}
	s.mu.Lock()
	delete(s.live, sessionID)
	s.mu.Unlock()
}

// sweepExpired is T-2905's periodic session sweep, run on the renewal
// loop's own ticker: rows past idle expiry or the hard cap are deleted in
// one statement (store.SessionRepo.DeleteExpired — ON DELETE CASCADE takes
// their push subscriptions, the cleanup docs/security.md's push section
// documents), and the in-memory live map is pruned to match. Before this,
// expired rows lingered until their next request happened to touch them —
// which for an abandoned session is never.
func (s *Service) sweepExpired(ctx context.Context) {
	n, err := s.sessions.DeleteExpired(ctx, s.now().Unix(), int64(s.hardTimeout.Seconds()))
	if err != nil {
		s.log.Error("auth: sweeping expired sessions", "error", err)
		return
	}
	if n > 0 {
		s.log.Info("auth: swept expired sessions", "count", n)
	}
	// Prune live entries whose rows are gone (swept here or deleted
	// elsewhere) so the renewal loop stops renewing tickets for them.
	s.mu.Lock()
	ids := make([]string, 0, len(s.live))
	for id := range s.live {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		if _, err := s.sessions.Get(ctx, id); errors.Is(err, store.ErrNotFound) {
			s.mu.Lock()
			delete(s.live, id)
			s.mu.Unlock()
		}
	}
}
