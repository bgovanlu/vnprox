// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// External-IPAM bidirectional sync (T-1203).
//
// The NetBox/phpIPAM bridge is upgraded from read-merge to bidirectional
// sync: vnprox computes a diff between its own PVE-IPAM allocations and the
// external system's own records, previews it (dry-run, never writes), and —
// only on an explicit confirm — applies the additions/removals to the
// external system, auditing every write.
//
// These writes deliberately sit OUTSIDE internal/change: an external IPAM
// system is not Proxmox network config, so there is nothing to stage/apply
// through the change engine, and this package never constructs a change.Op.
// But "outside the change engine" must never mean "unstaged and unaudited":
// the preview→confirm→audit ceremony below mirrors the change engine's
// stage/review/confirm/audit contract (and POST /lldp/install's explicit
// confirm), so an external-IPAM write is exactly as reviewable and auditable
// as an in-cluster one.

var (
	// ErrSyncNotConfigured is returned by the sync methods when no external
	// IPAM client is wired (the bridge isn't configured for this deployment).
	ErrSyncNotConfigured = errors.New("ipam: external IPAM sync is not configured")
	// ErrSyncConfirmRequired is returned by ExternalSyncApply when confirm is
	// false — the explicit-confirm ceremony (mirrors POST /lldp/install). The
	// API layer maps it to a 400; the service refuses to write regardless, so
	// no code path can write without confirmation.
	ErrSyncConfirmRequired = errors.New("ipam: external IPAM sync apply requires confirm=true")
)

// ExternalSyncDocsLink is the remediation/design-note pointer carried by every
// external-sync finding (they are not fixable via a changeset — external IPAM
// is outside internal/change — so, like every other non-fixable producer,
// they link to the docs).
const ExternalSyncDocsLink = "docs/features/ipam.md#7-external-ipam-bidirectional-sync"

// ExternalRecord is one host-address record as an external IPAM system
// (NetBox/phpIPAM) models it. IP is the canonical key; Hostname is the
// human-facing name the two systems must agree on.
type ExternalRecord struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname,omitempty"`
}

// ExternalIPAMClient is the bridge to a NetBox/phpIPAM instance — the seam a
// real HTTP client (or the test double) satisfies. ListRecords is the read
// half of today's read-merge; Create/Update/Delete are the write half this
// task adds. A nil client disables sync entirely (ErrSyncNotConfigured).
type ExternalIPAMClient interface {
	ListRecords(ctx context.Context) ([]ExternalRecord, error)
	CreateRecord(ctx context.Context, rec ExternalRecord) error
	UpdateRecord(ctx context.Context, rec ExternalRecord) error
	DeleteRecord(ctx context.Context, ip string) error
}

// SyncChangeKind classifies one diff entry.
type SyncChangeKind string

const (
	// SyncAdd: an address vnprox has allocated that the external system does
	// not know about — the apply pushes a create to the external system.
	SyncAdd SyncChangeKind = "add"
	// SyncRemove: an address the external system holds that vnprox no longer
	// allocates — the apply pushes a delete to the external system.
	SyncRemove SyncChangeKind = "remove"
	// SyncConflict: an address both systems know about but disagree on (the
	// hostname differs). Never auto-written — a conflict is an operator
	// judgment call, surfaced as an external_ipam_conflict finding instead.
	SyncConflict SyncChangeKind = "conflict"
)

// SyncChange is one entry of a sync plan: what the apply would do (or, for a
// conflict, what it refuses to do automatically). Before is the external
// system's current record (nil for an add); After is vnprox's desired record
// (nil for a remove).
type SyncChange struct {
	Before *ExternalRecord `json:"before,omitempty"`
	After  *ExternalRecord `json:"after,omitempty"`
	Kind   SyncChangeKind  `json:"kind"`
	IP     string          `json:"ip"`
}

// SyncPlan is ExternalSyncPreview's dry-run result: every add/remove/conflict,
// deterministically ordered by IP then kind.
type SyncPlan struct {
	Changes     []SyncChange `json:"changes"`
	GeneratedAt int64        `json:"generatedAt"`
}

// SyncRecordResult is one applied change's outcome, carrying before/after for
// the audit trail (T-1203 AC3: "audits ipam.external_sync with before/after").
type SyncRecordResult struct {
	Before *ExternalRecord `json:"before,omitempty"`
	After  *ExternalRecord `json:"after,omitempty"`
	Error  string          `json:"error,omitempty"`
	Kind   SyncChangeKind  `json:"kind"`
	IP     string          `json:"ip"`
	OK     bool            `json:"ok"`
}

// SyncResult is ExternalSyncApply's result: one entry per attempted write.
// Conflicts are never in here (they are not written) — only adds/removes.
type SyncResult struct {
	Applied     []SyncRecordResult `json:"applied"`
	GeneratedAt int64              `json:"generatedAt"`
}

// SyncFinding is one external-sync finding in this package's own vocabulary,
// converted to the unified findings.Finding shape by cmd/vnproxd's adapter
// (the same decoupling ipamFindingsAdapter gives Conflict — internal/ipam
// never imports internal/findings). Check is external_ipam_drift (an add or
// remove the two systems disagree on) or external_ipam_conflict (both hold
// the address but disagree on its hostname).
type SyncFinding struct {
	Check    string
	Severity string
	Detail   string
	IP       string
	DocsLink string
}

// vnproxRecords flattens every configured SDN subnet's PVE-IPAM allocation set
// into a canonical ip->record map (gateway pseudo-entries dropped — they are
// not host allocations an external IPAM tracks). A duplicate IP across plugins
// keeps the first (named) record seen.
func (s *Service) vnproxRecords(ctx context.Context) (map[string]ExternalRecord, error) {
	byCIDR, err := s.allocationsByCIDR(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]ExternalRecord{}
	for _, allocs := range byCIDR {
		for _, a := range allocs {
			if a.Gateway || a.IP == "" {
				continue
			}
			if existing, ok := out[a.IP]; ok && existing.Hostname != "" {
				continue
			}
			out[a.IP] = ExternalRecord{IP: a.IP, Hostname: a.Hostname}
		}
	}
	return out, nil
}

// diff computes the add/remove/conflict set between vnprox's desired records
// and the external system's current records.
func diffRecords(vnprox map[string]ExternalRecord, external []ExternalRecord) []SyncChange {
	ext := make(map[string]ExternalRecord, len(external))
	for _, r := range external {
		ext[r.IP] = r
	}
	var changes []SyncChange
	for ip, want := range vnprox {
		have, ok := ext[ip]
		if !ok {
			w := want
			changes = append(changes, SyncChange{Kind: SyncAdd, IP: ip, After: &w})
			continue
		}
		if have.Hostname != want.Hostname {
			b, a := have, want
			changes = append(changes, SyncChange{Kind: SyncConflict, IP: ip, Before: &b, After: &a})
		}
	}
	for ip, have := range ext {
		if _, ok := vnprox[ip]; !ok {
			b := have
			changes = append(changes, SyncChange{Kind: SyncRemove, IP: ip, Before: &b})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].IP != changes[j].IP {
			return changes[i].IP < changes[j].IP
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}

// ExternalSyncPreview computes the sync plan without writing anything (T-1203
// AC3: "surfaces additions/removals/conflicts without writing"). The external
// client's read is the only call it makes; no Create/Update/Delete is ever
// invoked.
func (s *Service) ExternalSyncPreview(ctx context.Context) (SyncPlan, error) {
	if s.syncClient == nil {
		return SyncPlan{}, ErrSyncNotConfigured
	}
	want, err := s.vnproxRecords(ctx)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("ipam: reading vnprox allocations for sync preview: %w", err)
	}
	external, err := s.syncClient.ListRecords(ctx)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("ipam: reading external IPAM records for sync preview: %w", err)
	}
	return SyncPlan{Changes: diffRecords(want, external), GeneratedAt: s.now().Unix()}, nil
}

// ExternalSyncApply applies the current plan's additions/removals to the
// external system, but only when confirm is true (ErrSyncConfirmRequired
// otherwise, before any write). Conflicts are never written — they stay
// findings for manual resolution. Each attempted write's before/after is
// returned so the caller can audit ipam.external_sync per record.
func (s *Service) ExternalSyncApply(ctx context.Context, confirm bool) (SyncResult, error) {
	if s.syncClient == nil {
		return SyncResult{}, ErrSyncNotConfigured
	}
	if !confirm {
		return SyncResult{}, ErrSyncConfirmRequired
	}
	plan, err := s.ExternalSyncPreview(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{GeneratedAt: s.now().Unix()}
	for _, ch := range plan.Changes {
		switch ch.Kind {
		case SyncAdd:
			werr := s.syncClient.CreateRecord(ctx, *ch.After)
			res.Applied = append(res.Applied, applyResult(ch, werr))
		case SyncRemove:
			werr := s.syncClient.DeleteRecord(ctx, ch.IP)
			res.Applied = append(res.Applied, applyResult(ch, werr))
		case SyncConflict:
			// Never auto-written — a conflict is a manual-resolution finding.
			continue
		}
	}
	return res, nil
}

func applyResult(ch SyncChange, err error) SyncRecordResult {
	r := SyncRecordResult{Kind: ch.Kind, IP: ch.IP, Before: ch.Before, After: ch.After, OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

// ExternalSyncFindings recomputes the sync plan and projects it into the
// unified findings vocabulary (T-1203 AC4): one external_ipam_conflict per
// address the two systems disagree on, plus one external_ipam_drift per
// pending add/remove. All non-fixable, all carrying ExternalSyncDocsLink.
// Nil client → no findings (sync not configured), never an error.
func (s *Service) ExternalSyncFindings(ctx context.Context) ([]SyncFinding, error) {
	if s.syncClient == nil {
		return nil, nil
	}
	plan, err := s.ExternalSyncPreview(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SyncFinding, 0, len(plan.Changes))
	for _, ch := range plan.Changes {
		switch ch.Kind {
		case SyncConflict:
			out = append(out, SyncFinding{
				Check:    "external_ipam_conflict",
				Severity: SeverityWarning,
				Detail: fmt.Sprintf("external IPAM and vnprox disagree on %s: vnprox has %q, external IPAM has %q",
					ch.IP, hostnameOf(ch.After), hostnameOf(ch.Before)),
				IP:       ch.IP,
				DocsLink: ExternalSyncDocsLink,
			})
		case SyncAdd:
			out = append(out, SyncFinding{
				Check:    "external_ipam_drift",
				Severity: "info",
				Detail:   fmt.Sprintf("%s is allocated in vnprox but absent from external IPAM (sync would add it)", ch.IP),
				IP:       ch.IP,
				DocsLink: ExternalSyncDocsLink,
			})
		case SyncRemove:
			out = append(out, SyncFinding{
				Check:    "external_ipam_drift",
				Severity: "info",
				Detail:   fmt.Sprintf("%s exists in external IPAM but is not allocated in vnprox (sync would remove it)", ch.IP),
				IP:       ch.IP,
				DocsLink: ExternalSyncDocsLink,
			})
		}
	}
	return out, nil
}

func hostnameOf(r *ExternalRecord) string {
	if r == nil {
		return ""
	}
	return r.Hostname
}
