// SPDX-License-Identifier: Apache-2.0

package powerdns

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Every case below is stated as "what PowerdnsPlugin.pm does", because that
// is the only reason to prefer one shape over another: PVE and vnprox write
// into the same zones, and a difference between them shows up as drift no
// operator asked for.

func TestAddContent_AppendsWithoutDisturbingSiblings(t *testing.T) {
	existing := RRSet{
		Name: "web.example.com.", Type: "A", TTL: 300,
		Records: []Record{{Content: "10.0.0.5"}, {Content: "10.0.0.6"}},
	}

	got, write := AddContent(existing, "web.example.com", "A", "10.0.0.7", 300)
	if !write {
		t.Fatal("adding a new address should need a write")
	}
	if got.ChangeType != ChangeReplace {
		t.Errorf("changetype = %q, want REPLACE (PowerDNS has no CREATE)", got.ChangeType)
	}
	want := []Record{{Content: "10.0.0.5"}, {Content: "10.0.0.6"}, {Content: "10.0.0.7"}}
	if !reflect.DeepEqual(got.Records, want) {
		t.Errorf("records = %+v, want the existing two plus the new one", got.Records)
	}
}

// add_a_record's `return; # the record already exist so return early`. vnprox
// skips the PATCH entirely, so re-applying a realized changeset writes
// nothing rather than writing an identical rrset.
func TestAddContent_AlreadyPresentIsNoWrite(t *testing.T) {
	existing := RRSet{Name: "web.example.com.", Type: "A", TTL: 300, Records: []Record{{Content: "10.0.0.5"}}}

	if _, write := AddContent(existing, "web.example.com", "A", "10.0.0.5", 300); write {
		t.Error("adding an address that is already in the rrset should need no write")
	}
}

func TestAddContent_CreatesTheRRSetWhenThereIsNone(t *testing.T) {
	got, write := AddContent(RRSet{}, "new.example.com", "A", "10.0.0.1", 600)
	if !write {
		t.Fatal("a first record should need a write")
	}
	if got.Name != "new.example.com." {
		t.Errorf("name = %q, want the canonical trailing-dot form", got.Name)
	}
	if got.TTL != 600 || len(got.Records) != 1 {
		t.Errorf("rrset = %+v, want one record at ttl 600", got)
	}
}

// del_a_record: with records left over it REPLACEs the remainder, and it
// carries the EXISTING ttl rather than re-TTLing what it leaves behind.
func TestRemoveContent_KeepsTheRemainderAtItsOriginalTTL(t *testing.T) {
	existing := RRSet{
		Name: "web.example.com.", Type: "A", TTL: 900,
		Records: []Record{{Content: "10.0.0.5"}, {Content: "10.0.0.6"}},
	}

	got, write := RemoveContent(existing, "web.example.com", "A", "10.0.0.5")
	if !write {
		t.Fatal("removing a present address should need a write")
	}
	if got.ChangeType != ChangeReplace {
		t.Errorf("changetype = %q, want REPLACE while records remain", got.ChangeType)
	}
	if got.TTL != 900 {
		t.Errorf("ttl = %d, want the existing 900 — a delete must not re-TTL the survivors", got.TTL)
	}
	if len(got.Records) != 1 || got.Records[0].Content != "10.0.0.6" {
		t.Errorf("records = %+v, want only 10.0.0.6", got.Records)
	}
}

// del_a_record's other branch: removing the last record must DELETE the
// rrset. A REPLACE with an empty record list is not how PowerDNS removes one,
// so getting this wrong leaves the record in place while reporting success.
func TestRemoveContent_LastRecordDeletesTheRRSet(t *testing.T) {
	existing := RRSet{Name: "web.example.com.", Type: "A", TTL: 300, Records: []Record{{Content: "10.0.0.5"}}}

	got, write := RemoveContent(existing, "web.example.com", "A", "10.0.0.5")
	if !write {
		t.Fatal("removing the only address should need a write")
	}
	if got.ChangeType != ChangeDelete {
		t.Errorf("changetype = %q, want DELETE when nothing is left", got.ChangeType)
	}
	if got.Records == nil {
		t.Error("records must be present and empty on a DELETE, not null")
	}
	if len(got.Records) != 0 {
		t.Errorf("records = %+v, want empty", got.Records)
	}
}

func TestRemoveContent_AbsentIsNoWrite(t *testing.T) {
	existing := RRSet{Name: "web.example.com.", Type: "A", Records: []Record{{Content: "10.0.0.5"}}}

	if _, write := RemoveContent(existing, "web.example.com", "A", "10.0.0.9"); write {
		t.Error("removing an address that is not there should need no write")
	}
}

// add_ptr_record replaces the whole rrset with exactly one record: an address
// has one canonical name.
func TestSetSingle_ReplacesTheWholeRRSet(t *testing.T) {
	got := SetSingle("5.0.0.10.in-addr.arpa.", "PTR", "web.example.com.", 300)

	if got.ChangeType != ChangeReplace {
		t.Errorf("changetype = %q, want REPLACE", got.ChangeType)
	}
	if len(got.Records) != 1 || got.Records[0].Content != "web.example.com." {
		t.Errorf("records = %+v, want exactly the one PTR target", got.Records)
	}
}

// The one deliberate divergence from the plugin. vnprox's update op names one
// value for one (zone, name, type); PowerDNS's write unit is the whole rrset.
// Honouring the update against a round-robin A record would delete three of
// four addresses and report success.
func TestReplaceSingle_RefusesAMultiValuedRRSet(t *testing.T) {
	existing := RRSet{
		Name: "web.example.com.", Type: "A", TTL: 300,
		Records: []Record{{Content: "10.0.0.5"}, {Content: "10.0.0.6"}, {Content: "10.0.0.7"}},
	}

	_, write, err := ReplaceSingle(existing, "web.example.com", "A", "10.0.0.9", 300)
	if err == nil {
		t.Fatal("want a refusal, not a silent three-record deletion")
	}
	if write {
		t.Error("a refused update must not also report that a write is needed")
	}
	var ambiguous *ErrAmbiguousUpdate
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %#v, want *ErrAmbiguousUpdate", err)
	}
	// The message has to name the records that would have been destroyed —
	// an operator cannot act on "ambiguous update".
	for _, want := range []string{"10.0.0.5", "10.0.0.6", "10.0.0.7"} {
		if !strings.Contains(ambiguous.Error(), want) {
			t.Errorf("message %q does not name %s", ambiguous.Error(), want)
		}
	}
}

func TestReplaceSingle_SingleValued(t *testing.T) {
	existing := RRSet{Name: "web.example.com.", Type: "A", TTL: 300, Records: []Record{{Content: "10.0.0.5"}}}

	got, write, err := ReplaceSingle(existing, "web.example.com", "A", "10.0.0.9", 300)
	if err != nil {
		t.Fatalf("ReplaceSingle: %v", err)
	}
	if !write {
		t.Fatal("changing the value should need a write")
	}
	if len(got.Records) != 1 || got.Records[0].Content != "10.0.0.9" {
		t.Errorf("records = %+v, want the new value", got.Records)
	}
}

func TestReplaceSingle_IdenticalIsNoWrite(t *testing.T) {
	existing := RRSet{Name: "web.example.com.", Type: "A", TTL: 300, Records: []Record{{Content: "10.0.0.5"}}}

	_, write, err := ReplaceSingle(existing, "web.example.com", "A", "10.0.0.5", 300)
	if err != nil {
		t.Fatalf("ReplaceSingle: %v", err)
	}
	if write {
		t.Error("an update to the value and ttl already in place should need no write")
	}
}

// A TTL-only change is still a change. Getting this wrong makes a changeset
// that edits nothing but the TTL report success without touching the server.
func TestReplaceSingle_TTLOnlyChangeStillWrites(t *testing.T) {
	existing := RRSet{Name: "web.example.com.", Type: "A", TTL: 300, Records: []Record{{Content: "10.0.0.5"}}}

	got, write, err := ReplaceSingle(existing, "web.example.com", "A", "10.0.0.5", 900)
	if err != nil {
		t.Fatalf("ReplaceSingle: %v", err)
	}
	if !write {
		t.Fatal("a ttl-only change should still need a write")
	}
	if got.TTL != 900 {
		t.Errorf("ttl = %d, want 900", got.TTL)
	}
}

// REPLACE is an upsert: an update against a name PowerDNS does not have
// creates it. Whether the op should have been a create is the change engine's
// question, not the wire client's.
func TestReplaceSingle_MissingRRSetUpserts(t *testing.T) {
	got, write, err := ReplaceSingle(RRSet{}, "new.example.com", "A", "10.0.0.1", 300)
	if err != nil {
		t.Fatalf("ReplaceSingle: %v", err)
	}
	if !write || got.ChangeType != ChangeReplace {
		t.Errorf("got %+v (write=%v), want a REPLACE", got, write)
	}
}

func TestZoneFind_MatchesAcrossTheTrailingDot(t *testing.T) {
	z := Zone{RRSets: []RRSet{{Name: "web.example.com.", Type: "A", Records: []Record{{Content: "10.0.0.5"}}}}}

	for _, name := range []string{"web.example.com", "web.example.com."} {
		if _, ok := z.Find(name, "A"); !ok {
			t.Errorf("Find(%q, A) missed the rrset", name)
		}
	}
	if _, ok := z.Find("web.example.com", "AAAA"); ok {
		t.Error("Find matched on name alone, ignoring the type")
	}
}

// A disabled record is not an absent one — a resolver will not serve it, but
// the PTR audit must be able to tell "no PTR" from "a PTR that is switched
// off", which is the same missing-vs-unknown distinction the audit was built
// around.
func TestContents_IncludesDisabledRecords(t *testing.T) {
	rr := RRSet{Records: []Record{{Content: "10.0.0.6"}, {Content: "10.0.0.5", Disabled: true}}}

	got := rr.Contents()
	want := []string{"10.0.0.5", "10.0.0.6"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Contents = %v, want %v (sorted, disabled included)", got, want)
	}
}
