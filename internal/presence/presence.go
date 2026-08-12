package presence

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// TopicPrefix is the WS subscription-topic prefix that declares presence:
// subscribing to `presence:changeset:<id>` both says "I am looking at this"
// and is how the resulting `presence.changed` events are delivered. It is
// the same parameterised-topic shape `metrics:<ref>` already uses — presence
// adds no second push channel (docs/api.md's WebSocket section).
const TopicPrefix = "presence:"

// Scope prefixes. A scope is either one changeset or one entity; T-2805
// requires presence to be per-changeset AND per-entity, and nothing else is
// accepted — an unrecognised scope is dropped rather than tracked, so a
// client cannot mint arbitrary presence channels.
const (
	ScopeChangesetPrefix = "changeset:"
	ScopeEntityPrefix    = "entity:"
)

// EventName is the presence event's name on the wire.
const EventName = "presence.changed"

// Viewer is one operator currently looking at a scope.
//
// Sessions counts how many distinct sessions that person has open on it, so
// one person with two browser tabs reads as one viewer rather than two —
// the same "distinct principals" rule T-2604's approver counting uses.
type Viewer struct {
	User     string `json:"user"`
	Since    int64  `json:"since"`
	Sessions int    `json:"sessions"`
}

// ScopeState is who is present on one scope.
type ScopeState struct {
	Scope string `json:"scope"`
	// Viewers is every distinct person present, ordered by username.
	// Whether a caller is allowed to SEE it is decided by the read surface
	// that renders it (internal/api gates it on the `audit` capability);
	// this package always reports the truth.
	Viewers []Viewer `json:"viewers"`
	// Count is the number of distinct people present — the only thing the
	// `presence.changed` WS event ever carries.
	Count int `json:"count"`
}

// connState is one live WS connection's presence contribution.
type connState struct {
	scopes    map[string]bool
	username  string
	sessionID string
	since     int64
}

// ConnOpened registers a live WS connection. Called by internal/topology's
// hub at accept time with the already-authenticated identity; a connection
// with no resolvable session (an unauthenticated or pre-T-2805 caller)
// contributes no presence and holds no locks, which is the fail-closed
// default every other optional-identity lookup in this codebase takes.
func (s *Service) ConnOpened(connID, username, sessionID string) {
	if connID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.conns[connID]; exists {
		return
	}
	s.conns[connID] = &connState{
		username:  username,
		sessionID: sessionID,
		since:     s.now().Unix(),
		scopes:    map[string]bool{},
	}
	if sessionID != "" {
		s.sessions[sessionID]++
	}
}

// ConnTopics replaces connID's declared presence scopes from a subscribe
// message's full topic set (non-presence topics are ignored). It broadcasts
// `presence.changed` for every scope whose membership changed — joined and
// departed alike.
func (s *Service) ConnTopics(connID string, topics []string) {
	s.mu.Lock()
	conn, ok := s.conns[connID]
	if !ok {
		s.mu.Unlock()
		return
	}
	next := map[string]bool{}
	for _, t := range topics {
		if scope, valid := scopeFromTopic(t); valid {
			next[scope] = true
		}
	}
	changed := symmetricDifference(conn.scopes, next)
	conn.scopes = next
	states := s.statesLocked(changed)
	s.mu.Unlock()

	s.broadcast(states)
}

// ConnClosed deregisters a connection. When it was that session's LAST live
// connection, every advisory lock the session holds is released — T-2805
// AC3's closed laptop, which no release endpoint would ever cover.
//
// The release is done with a bounded background context rather than a
// request context, because by definition there is no request: the caller is
// the hub's own teardown path.
func (s *Service) ConnClosed(connID string) {
	s.mu.Lock()
	conn, ok := s.conns[connID]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.conns, connID)
	scopes := make(map[string]bool, len(conn.scopes))
	for scope := range conn.scopes {
		scopes[scope] = true
	}
	lastForSession := ""
	if conn.sessionID != "" {
		s.sessions[conn.sessionID]--
		if s.sessions[conn.sessionID] <= 0 {
			delete(s.sessions, conn.sessionID)
			lastForSession = conn.sessionID
		}
	}
	states := s.statesLocked(scopes)
	s.mu.Unlock()

	s.broadcast(states)

	if lastForSession == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := s.ReleaseSession(ctx, lastForSession)
	if err != nil {
		s.log.Error("presence: releasing a disconnected session's locks", "error", err)
		return
	}
	if n > 0 {
		s.log.Info("presence: released a disconnected session's advisory locks", "count", n)
	}
}

// Scope reports presence on exactly one scope (empty viewers and a zero
// count when nobody is there — an answer, not an absence).
func (s *Service) Scope(scope string) ScopeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scopeStateLocked(scope)
}

// Scopes reports every scope currently being viewed, ordered by scope.
func (s *Service) Scopes() []ScopeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, conn := range s.conns {
		for scope := range conn.scopes {
			seen[scope] = true
		}
	}
	out := make([]ScopeState, 0, len(seen))
	for scope := range seen {
		out = append(out, s.scopeStateLocked(scope))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

// scopeStateLocked builds one scope's state; s.mu must be held.
func (s *Service) scopeStateLocked(scope string) ScopeState {
	byUser := map[string]*Viewer{}
	sessionsSeen := map[string]map[string]bool{}
	for _, conn := range s.conns {
		if !conn.scopes[scope] {
			continue
		}
		v, ok := byUser[conn.username]
		if !ok {
			v = &Viewer{User: conn.username, Since: conn.since}
			byUser[conn.username] = v
			sessionsSeen[conn.username] = map[string]bool{}
		}
		if conn.since < v.Since {
			v.Since = conn.since
		}
		// A session with two tabs is one session; a person with two
		// sessions is one viewer with two of them.
		key := conn.sessionID
		if key == "" {
			key = conn.username
		}
		if !sessionsSeen[conn.username][key] {
			sessionsSeen[conn.username][key] = true
			v.Sessions++
		}
	}
	state := ScopeState{Scope: scope, Viewers: make([]Viewer, 0, len(byUser))}
	for _, v := range byUser {
		state.Viewers = append(state.Viewers, *v)
	}
	sort.Slice(state.Viewers, func(i, j int) bool { return state.Viewers[i].User < state.Viewers[j].User })
	state.Count = len(state.Viewers)
	return state
}

// statesLocked snapshots the given scopes' states; s.mu must be held.
func (s *Service) statesLocked(scopes map[string]bool) []ScopeState {
	if len(scopes) == 0 {
		return nil
	}
	out := make([]ScopeState, 0, len(scopes))
	for scope := range scopes {
		out = append(out, s.scopeStateLocked(scope))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

// presenceEvent is the wire shape of a presence.changed push: the flat
// `{"event": ..., ...payload}` envelope every producer in this codebase
// keeps (internal/topology/hub.go's deltaEvent).
//
// It carries a COUNT AND NO IDENTITIES, deliberately. The hub fans one
// pre-encoded payload out to every subscriber of a topic and has no
// per-subscriber filter, so any name in here would reach subscribers
// regardless of their capabilities. Names live in GET /presence, which is
// gated — the same split `drift.changed`/`findings.changed` already use
// ("{count}", then fetch the detail).
type presenceEvent struct {
	Event string `json:"event"`
	Scope string `json:"scope"`
	Count int    `json:"count"`
}

func (s *Service) broadcast(states []ScopeState) {
	if s.ws == nil {
		return
	}
	for _, st := range states {
		payload, err := json.Marshal(presenceEvent{Event: EventName, Scope: st.Scope, Count: st.Count})
		if err != nil {
			s.log.Error("presence: marshaling presence.changed", "error", err, "scope", st.Scope)
			continue
		}
		s.ws.Broadcast(TopicPrefix+st.Scope, payload)
	}
}

// scopeFromTopic extracts a presence scope from a subscription topic,
// reporting whether the topic named a valid one. Anything that is not
// `presence:changeset:<id>` or `presence:entity:<ref>` is not a scope.
func scopeFromTopic(topic string) (string, bool) {
	scope, ok := strings.CutPrefix(topic, TopicPrefix)
	if !ok {
		return "", false
	}
	return scope, ValidScope(scope)
}

// ValidScope reports whether scope is one this package tracks: exactly one
// changeset or exactly one entity, with a non-empty identifier.
func ValidScope(scope string) bool {
	for _, prefix := range []string{ScopeChangesetPrefix, ScopeEntityPrefix} {
		if rest, ok := strings.CutPrefix(scope, prefix); ok && rest != "" {
			return true
		}
	}
	return false
}

// ChangesetScope/EntityScope build the two scope spellings, so no caller
// concatenates the prefixes by hand.
func ChangesetScope(id string) string { return ScopeChangesetPrefix + id }

// EntityScope names one entity's presence scope by its Ref string.
func EntityScope(ref string) string { return ScopeEntityPrefix + ref }

func symmetricDifference(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if !b[k] {
			out[k] = true
		}
	}
	for k := range b {
		if !a[k] {
			out[k] = true
		}
	}
	return out
}
