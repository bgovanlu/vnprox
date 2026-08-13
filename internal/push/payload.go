// payload.go builds the ONLY bytes this package ever sends as a push
// payload: a small, closed-schema Notification. This exists because a push
// notification can render on a phone's LOCK SCREEN before anyone
// authenticates on that device (docs/security.md's threat model for this
// feature, and T-2005's card: "must not leak the network's shape to a
// phone lock screen"). The producers this package listens to — changeset
// events, drift counts, finding transitions — routinely carry hostnames,
// guest names, IP addresses, and free-text detail strings (see e.g.
// internal/findings.Finding.Detail/Nodes/Refs, whose IDs themselves often
// embed a PVE node name — health_peertrust.go's peerFindingID(check,
// p.Node) is one of many). None of that ever reaches this file's output.
//
// Modeled on this codebase's other enumerated-outbound-payload precedent,
// internal/telemetry's Guard (docs/security.md "Compatibility telemetry":
// "it is off, and off is structural ... There is a fixed list of fields").
// The difference here is stricter, not looser: telemetry enumerates which
// FIELDS may travel; this package enumerates the entire FINITE SET of
// possible payload VALUES. There is no field anywhere in this package that
// accepts free text derived from a Finding/Changeset/drift result — every
// Title/Body string below is a Go string literal at a call site, and the
// only caller-supplied values that ever reach a Notification are: (a) a
// changeset id, which internal/store.NewULID documents as opaque random
// entropy with no embedded identity, and (b) small non-negative counts,
// which docs/security.md's telemetry precedent already treats as carrying
// no identity ("A count. Node names are never in the payload.").

package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// Category is one of the three push categories T-2005's card names:
// "per-category opt-in: critical findings, awaiting-confirm changesets,
// drift". A subscription opts into a subset of these (push.go's
// categories_json); Dispatcher filters delivery to subscriptions that
// opted into the category a given Notification belongs to.
type Category string

const (
	CategoryCritical        Category = "critical"
	CategoryAwaitingConfirm Category = "awaitingConfirm"
	CategoryDrift           Category = "drift"
)

// AllCategories is the closed, ordered vocabulary — internal/api's POST
// /push/subscriptions handler validates a request's `categories` field
// against exactly this list (the same "closed schema, no pass-through"
// discipline docs/security.md's telemetry Guard documents for its own
// field list), and web/src/push mirrors it for the opt-in UI.
var AllCategories = []Category{CategoryCritical, CategoryAwaitingConfirm, CategoryDrift}

// Valid reports whether c is one of AllCategories.
func (c Category) Valid() bool {
	for _, v := range AllCategories {
		if c == v {
			return true
		}
	}
	return false
}

// Notification is the complete, closed shape of every push payload this
// package ever sends — see this file's package doc comment for why no
// other field, or any caller-supplied free text, is ever added to it.
// json.Marshal of this struct IS the plaintext handed to encryptAES128GCM.
type Notification struct {
	Category Category `json:"category"`
	Event    string   `json:"event"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	// URL is an app-relative deep link (never absolute — the browser
	// resolves it against its own origin in the service worker's
	// notificationclick handler, web/public/sw.js), built only from fixed
	// path literals and, for the awaiting-confirm case, a changeset ULID
	// (opaque, no embedded identity — see the package doc comment).
	URL string `json:"url"`
}

// Marshal encodes n as the plaintext this package's Send/Dispatcher pass to
// encryptAES128GCM.
func (n Notification) Marshal() ([]byte, error) {
	b, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("push: encoding notification payload: %w", err)
	}
	return b, nil
}

// changesetEnvelope is the subset of internal/change's `changeset.status`
// WS event (internal/change/service.go's statusEvent) this package reads:
// just enough to decide "did this changeset just enter awaiting_confirm",
// and its opaque id for the deep link. Every other field that event
// carries (there are none identity-bearing today, but the point is this
// package would ignore them even if there were) is dropped by never being
// in this struct.
type eventEnvelope struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// statusAwaitingConfirm mirrors internal/change.StatusAwaitingConfirm's
// wire value ("awaiting_confirm") — spelled out as a literal here (like
// internal/topology/hub.go's topicChangesets/topicDrift/topicFindings
// re-declare their peers' wire-documented topic strings) so this package
// does not need to import internal/change just for one constant.
const statusAwaitingConfirm = "awaiting_confirm"

// BuildFromEvent inspects raw — the exact encoded payload
// internal/topology.Hub.SetEventSink hands its registered sink, per that
// method's doc comment, "the identical envelope an events-subscribed WS
// client would" receive — and returns the Notification it should become,
// if any. ok is false for every event this package does not push about:
// a changeset.status transition to anything other than awaiting_confirm,
// drift.changed with a zero/negative count (nothing to tell anyone),
// findings.changed (too coarse to know if a NEW finding is critical —
// internal/push's Notifier, wired separately against
// internal/findings.Notifier's per-transition hook, is what backs the
// "critical" category instead, see notifier.go), audit.appended, and
// anything unparseable.
func BuildFromEvent(raw []byte) (Notification, bool) {
	var env eventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Notification{}, false
	}

	switch env.Event {
	case "changeset.status":
		if env.Status != statusAwaitingConfirm || env.ID == "" {
			return Notification{}, false
		}
		return Notification{
			Category: CategoryAwaitingConfirm,
			Event:    env.Event,
			Title:    "Changeset awaiting confirm",
			Body:     "A changeset applied successfully and is waiting for confirmation before it becomes permanent.",
			URL:      "/changesets/" + url.PathEscape(env.ID) + "/review",
		}, true
	case "drift.changed":
		if env.Count <= 0 {
			return Notification{}, false
		}
		return Notification{
			Category: CategoryDrift,
			Event:    env.Event,
			Title:    "Configuration drift detected",
			Body:     fmt.Sprintf("%d change(s) were made outside vnprox and no longer match its record.", env.Count),
			URL:      "/",
		}, true
	default:
		return Notification{}, false
	}
}

// CriticalFindingNotification is the fixed Notification for T-2005's
// "critical findings" category — built from NOTHING but this function's
// own literals, deliberately: see this file's package doc comment for why
// no Finding field (ID, Detail, Nodes, Refs, or the Check name) is ever
// threaded through, even though internal/push.FindingNotifier (notifier.go)
// is called with a full findings.Finding on every qualifying transition.
func CriticalFindingNotification() Notification {
	return Notification{
		Category: CategoryCritical,
		Event:    "finding.critical",
		Title:    "New critical finding",
		Body:     "A new critical-severity finding needs attention.",
		URL:      "/tools?pushCategory=critical",
	}
}

// ErrUnknownCategory is returned by ParseCategories for any string outside
// AllCategories.
var ErrUnknownCategory = errors.New("push: unknown category")

// ParseCategories validates and de-duplicates raw (a subscription request's
// `categories` field) against AllCategories, preserving AllCategories'
// canonical order rather than the caller's — so two requests naming the
// same set in a different order produce identical stored JSON. Returns
// ErrUnknownCategory (wrapping the offending value in its message) for
// anything not in the closed vocabulary, and an error if raw is empty (a
// subscription with zero categories would never receive anything, which is
// almost certainly a client bug worth refusing rather than silently
// accepting).
func ParseCategories(raw []string) ([]Category, error) {
	if len(raw) == 0 {
		return nil, errors.New("push: at least one category is required")
	}
	requested := make(map[Category]bool, len(raw))
	for _, r := range raw {
		c := Category(r)
		if !c.Valid() {
			return nil, fmt.Errorf("%w: %q", ErrUnknownCategory, r)
		}
		requested[c] = true
	}
	out := make([]Category, 0, len(requested))
	for _, c := range AllCategories {
		if requested[c] {
			out = append(out, c)
		}
	}
	return out, nil
}
