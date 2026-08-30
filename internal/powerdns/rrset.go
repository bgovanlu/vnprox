// SPDX-License-Identifier: Apache-2.0

package powerdns

import (
	"fmt"
	"sort"
	"strings"
)

// PowerDNS's write unit is the rrset — every record sharing a name and a
// type — and there is no per-record route. vnprox's change-engine ops
// (sdn.dns.record.create/update/delete) are per-record, so each one becomes a
// read-modify-write here. The three builders below are deliberately the same
// three operations PowerdnsPlugin.pm performs, with the same early-return and
// DELETE-when-empty behaviour, so a zone vnprox writes and a zone PVE writes
// end up in the same state:
//
//   - AddContent      <- add_a_record       (append, no-op if already present)
//   - RemoveContent   <- del_a_record       (REPLACE the remainder, or DELETE)
//   - SetSingle       <- add_ptr_record     (REPLACE with exactly one record)
//
// The one place vnprox does NOT follow the plugin is ReplaceSingle: see its
// comment for why an ambiguous update refuses rather than guesses.

// AddContent returns the REPLACE rrset that adds content to existing, and
// reports whether a write is needed at all. existing may be the zero RRSet
// (the name/type has no records yet).
//
// The false return is PowerdnsPlugin.pm's `return; # the record already exist
// so return early` — vnprox skips the PATCH entirely in that case rather than
// writing an identical rrset, so re-applying a changeset that is already
// realized touches nothing.
func AddContent(existing RRSet, name, typ, content string, ttl int) (RRSet, bool) {
	records := make([]Record, 0, len(existing.Records)+1)
	for _, r := range existing.Records {
		if r.Content == content {
			return RRSet{}, false
		}
		records = append(records, r)
	}
	records = append(records, Record{Content: content, Disabled: false})
	return RRSet{
		Name:       zoneID(name),
		Type:       typ,
		TTL:        ttl,
		ChangeType: ChangeReplace,
		Records:    records,
	}, true
}

// RemoveContent returns the rrset change that drops content from existing,
// and reports whether a write is needed.
//
// The DELETE-vs-REPLACE split is PowerdnsPlugin.pm's del_a_record exactly:
// removing the last record of an rrset must DELETE the rrset, because a
// REPLACE with an empty record list is not how PowerDNS removes one. Keeping
// existing.TTL on the REPLACE branch is also the plugin's behaviour — a
// deletion must not silently re-TTL the records it leaves behind.
func RemoveContent(existing RRSet, name, typ, content string) (RRSet, bool) {
	kept := make([]Record, 0, len(existing.Records))
	for _, r := range existing.Records {
		if r.Content == content {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == len(existing.Records) {
		// Not found — nothing to do, the same early return del_a_record makes.
		return RRSet{}, false
	}
	if len(kept) == 0 {
		return DeleteRRSet(name, typ), true
	}
	return RRSet{
		Name:       zoneID(name),
		Type:       typ,
		TTL:        existing.TTL,
		ChangeType: ChangeReplace,
		Records:    kept,
	}, true
}

// SetSingle returns a REPLACE rrset holding exactly one record, discarding
// whatever was there. This is add_ptr_record's shape, and it is correct for a
// PTR (one address has one canonical name) and wrong as a general update —
// which is what ReplaceSingle exists to enforce.
func SetSingle(name, typ, content string, ttl int) RRSet {
	return RRSet{
		Name:       zoneID(name),
		Type:       typ,
		TTL:        ttl,
		ChangeType: ChangeReplace,
		Records:    []Record{{Content: content, Disabled: false}},
	}
}

// DeleteRRSet returns the change that removes a name/type entirely.
// PowerDNS requires the records list to be present and empty on a DELETE.
func DeleteRRSet(name, typ string) RRSet {
	return RRSet{
		Name:       zoneID(name),
		Type:       typ,
		ChangeType: ChangeDelete,
		Records:    []Record{},
	}
}

// ErrAmbiguousUpdate is returned by ReplaceSingle when the rrset it was asked
// to edit holds more than one record.
//
// vnprox's sdn.dns.record.update op carries one Value for one (zone, name,
// type) — a model that has no way to say WHICH of an rrset's records it
// means. PowerDNS's write unit is the whole rrset, so honouring such an
// update would replace every record under that name with the single new
// value: a round-robin A record with four addresses would silently become
// one, and the changeset would report success.
//
// Refusing is the only honest answer available at this layer. The operator
// can still delete and re-create the records individually, which says what
// they mean.
type ErrAmbiguousUpdate struct {
	Name    string
	Type    string
	Records []string
}

func (e *ErrAmbiguousUpdate) Error() string {
	return fmt.Sprintf("powerdns: %s/%s holds %d records (%s) and this update names only one value — "+
		"delete and re-create the records individually rather than replacing the whole set",
		e.Name, e.Type, len(e.Records), strings.Join(e.Records, ", "))
}

// ReplaceSingle returns the REPLACE rrset that changes a single-valued
// name/type to content, and reports whether a write is needed. It refuses,
// with *ErrAmbiguousUpdate, if the existing rrset is multi-valued — see that
// type's comment.
//
// A missing rrset is not an error: an update against a name/type PowerDNS
// does not have creates it, matching the plugin's own REPLACE-is-upsert
// behaviour. Whether that op should have been a create is a question for the
// change engine's validators, not for the wire client.
func ReplaceSingle(existing RRSet, name, typ, content string, ttl int) (RRSet, bool, error) {
	if len(existing.Records) > 1 {
		contents := make([]string, 0, len(existing.Records))
		for _, r := range existing.Records {
			contents = append(contents, r.Content)
		}
		sort.Strings(contents)
		return RRSet{}, false, &ErrAmbiguousUpdate{Name: name, Type: typ, Records: contents}
	}
	if len(existing.Records) == 1 && existing.Records[0].Content == content && existing.TTL == ttl {
		return RRSet{}, false, nil
	}
	return SetSingle(name, typ, content, ttl), true, nil
}

// Contents returns an rrset's record data in a stable order, skipping none —
// including disabled records, which a resolver will not serve but which are
// emphatically not absent. Callers that must distinguish them read Records
// directly; this is for the common case where vnprox needs the value set.
func (r RRSet) Contents() []string {
	out := make([]string, 0, len(r.Records))
	for _, rec := range r.Records {
		out = append(out, rec.Content)
	}
	sort.Strings(out)
	return out
}

// HasContent reports whether this rrset already contains content.
func (r RRSet) HasContent(content string) bool {
	for _, rec := range r.Records {
		if rec.Content == content {
			return true
		}
	}
	return false
}
