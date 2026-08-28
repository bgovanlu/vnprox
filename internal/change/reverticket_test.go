// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// T-1805 / roadmap-proven D1 — the apply-time revert ticket.
//
// Every test here maps to a numbered claim in the card's safety analysis and
// to an acceptance criterion; planning/reports/T-1805.md carries the full
// cross-reference. The suite deliberately drives the *real* wire path
// end-to-end: a genuine pvemock session ticket is captured from the applying
// client, sealed with the real store.SessionCipher, written to a real SQLite
// row, unsealed on the unattended revert path, turned into a real
// non-renewing sealed-ticket *pve.Client, and used to make real firewall
// writes that pvemock authenticates. Nothing about the credential round trip
// is stubbed.

// --- harness ---------------------------------------------------------------

// revertTicketGateway is the test-side mirror of cmd/vnproxd's production
// pveGateway.RevertTicket: it hands the change engine the applying user's own
// PVE ticket out of the live *pve.Client, exactly as production does.
type revertTicketGateway struct {
	*fakePVEGateway
	client *pve.Client
	now    func() time.Time
	// expiresIn overrides the derived expiry when non-zero, so the
	// reduced-coverage case (AC6) can be produced without waiting two hours.
	expiresIn time.Duration
	// suppress makes RevertTicket report "no ticket", modelling a gateway with
	// no user credential (e.g. an API-token identity).
	suppress bool
}

func (g *revertTicketGateway) RevertTicket(ctx context.Context) (change.RevertTicket, bool) {
	if g.suppress {
		return change.RevertTicket{}, false
	}
	ticket, csrf, issuedAt, ok := g.client.RevertCredentials(ctx)
	if !ok {
		return change.RevertTicket{}, false
	}
	expires := change.TicketExpiryFrom(issuedAt, pve.TicketLifetime)
	if g.expiresIn != 0 {
		expires = g.now().Add(g.expiresIn).Unix()
	}
	return change.RevertTicket{Ticket: ticket, CSRF: csrf, ExpiresAt: expires}, true
}

// sealedTicketFactory is the test-side mirror of cmd/vnproxd's
// revertGatewayFactory: it builds a REAL non-renewing sealed-ticket
// *pve.Client from the unsealed credential and wraps it in a gateway. Because
// pvemock authenticates the PVEAuthCookie/CSRFPreventionToken pair against its
// own session table, a wrong or corrupted ticket would simply fail — so a
// passing revert here is evidence the exact credential survived the
// seal→store→unseal round trip, not merely that some gateway was produced.
type sealedTicketFactory struct {
	apiURL   string
	pollNode string
	// seen records every ticket the factory was asked to build a client from,
	// so a test can assert what was actually unsealed.
	seen []change.RevertTicket
	// fail makes the factory refuse, modelling a daemon that cannot build a
	// revert client.
	fail bool
}

func (f *sealedTicketFactory) GatewayForRevertTicket(_ context.Context, t change.RevertTicket) (change.PVEGateway, error) {
	f.seen = append(f.seen, t)
	if f.fail {
		return nil, &injectedError{"injected revert-gateway failure"}
	}
	client, err := pve.New(pve.Config{
		APIURL: f.apiURL, Auth: pve.AuthTicket, Ticket: t.Ticket, CSRFToken: t.CSRF,
	})
	if err != nil {
		return nil, err
	}
	return &fakePVEGateway{client: client, pollNode: f.pollNode}, nil
}

type revertHarness struct {
	*applyHarness
	gw      *revertTicketGateway
	factory *sealedTicketFactory
	cipher  *store.SessionCipher
	logBuf  *bytes.Buffer
	now     func() time.Time
	dbPath  string
}

// newRevertHarness is newFwHarness plus everything T-1805 adds: a real
// SessionCipher as the Sealer, a RevertGatewayFactory, a captured log buffer
// (so the "never logged" claim is assertable), and a known DB file path (so
// the "never in the raw DB bytes" assertion can read it, mirroring
// TestWireGuardRepo_PrivateKeyEncryptedAtRest's shape).
func newRevertHarness(t *testing.T, opts ...func(*change.Config)) *revertHarness {
	t.Helper()
	base := newHarness(t, fixtureSingleNode)

	key := make([]byte, store.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("store.NewSessionCipher: %v", err)
	}

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	now := time.Now
	factory := &sealedTicketFactory{apiURL: base.server.URL, pollNode: "pve1"}
	gw := &revertTicketGateway{
		fakePVEGateway: &fakePVEGateway{client: base.client, pollNode: "pve1"},
		client:         base.client,
		now:            func() time.Time { return now() },
	}

	inv := liveFwInventorySource{client: base.client, target: fwGuestRef(), scope: fwGuestScope()}
	cfg := change.Config{
		Changesets: base.csRepo, Audit: base.auditRepo, WS: base.ws, Inventory: inv,
		Nodes: base.agent, Snapshots: base.snapRepo, Blobs: base.blobRepo, Refresher: base.refresher,
		TimerFunc: base.timers.New, Logger: logger,
		Sealer:         cipher,
		RevertGateways: factory,
		ProtectedPath:  filepath.Join(t.TempDir(), "protected.json"),
	}
	for _, o := range opts {
		o(&cfg)
	}
	base.svc = newService(t, cfg)

	return &revertHarness{applyHarness: base, gw: gw, factory: factory, cipher: cipher, dbPath: base.dbPath, logBuf: logBuf, now: now}
}

// fwOps is a firewall-ONLY changeset: no node-file step at all, so nothing in
// it can be reverted by the daemon's own root-level host writer. This is
// precisely the shape planning/reports/T-502.md flagged as never reverting.
func fwOps() []change.Op {
	return []change.Op{
		{Type: change.OpFwRuleCreate, Target: fwGuestRef(), Params: &change.FwRuleCreateParams{
			Direction: "in", Action: "DROP", Proto: "tcp", Dport: "3306", Comment: "t1805 lockout rule", Pos: 1, Enabled: true,
		}},
	}
}

// sealedTicketRow reads the raw revert-ticket columns straight out of SQLite,
// bypassing every repository/service layer — the "asserted directly against
// the stored row" the card requires for the wipe.
func sealedTicketRow(t *testing.T, h *revertHarness, id string) (sealed []byte, expiresAt sql.NullInt64) {
	t.Helper()
	sealed, exp, err := h.csRepo.RevertTicket(context.Background(), id)
	if err != nil {
		t.Fatalf("RevertTicket(%s): %v", id, err)
	}
	return sealed, sql.NullInt64{Int64: exp, Valid: exp != 0}
}

func assertNoSealedTicket(t *testing.T, h *revertHarness, id, afterWhat string) {
	t.Helper()
	sealed, exp := sealedTicketRow(t, h, id)
	if len(sealed) != 0 {
		t.Errorf("after %s: changesets.revert_ticket_enc still holds %d bytes, want none", afterWhat, len(sealed))
	}
	if exp.Valid {
		t.Errorf("after %s: changesets.revert_ticket_expires_at = %d, want NULL", afterWhat, exp.Int64)
	}
}

func fwRuleComments(t *testing.T, h *revertHarness) []string {
	t.Helper()
	rules, err := h.client.ListFirewallRules(context.Background(), fwGuestScope())
	if err != nil {
		t.Fatalf("ListFirewallRules: %v", err)
	}
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Comment
	}
	return out
}

func hasComment(comments []string, want string) bool {
	for _, c := range comments {
		if c == want {
			return true
		}
	}
	return false
}

// --- AC1: the firewall-only timeout case that fails today ------------------

// TestRevertTicket_AC1_FirewallOnlyChangesetRevertsOnConfirmTimeout is
// acceptance criterion 1 and the card's headline result: a `fw.*`-only
// changeset reaches awaiting_confirm, the confirm window elapses with **no
// live session anywhere** (Apply's gateway is never handed to the timer), and
// the firewall rule it added is gone afterwards. Before T-1805 the rule
// survived: autoRollback passed a nil gateway and doRollbackLocked had no
// firewall restore at all.
func TestRevertTicket_AC1_FirewallOnlyChangesetRevertsOnConfirmTimeout(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()

	before := fwRuleComments(t, h)
	if hasComment(before, "t1805 lockout rule") {
		t.Fatalf("fixture already contains the rule under test: %v", before)
	}

	cs := h.mustCreate(t, "root@pam", "firewall-only lockout", fwOps())
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}
	if !hasComment(fwRuleComments(t, h), "t1805 lockout rule") {
		t.Fatalf("apply did not add the firewall rule: %v", fwRuleComments(t, h))
	}
	// The credential is at rest for the window.
	sealed, exp := sealedTicketRow(t, h, cs.ID)
	if len(sealed) == 0 {
		t.Fatalf("no revert ticket was sealed for an fw-only changeset")
	}
	if !exp.Valid || exp.Int64 <= h.now().Unix() {
		t.Fatalf("sealed ticket expiry = %v, want a future instant", exp)
	}

	// The confirm window elapses. Nothing in this call has a user session:
	// the timer callback is the daemon's own, exactly as in production.
	h.timers.fireLatest(t)

	got, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get after timeout: %v", err)
	}
	if got.Status != change.StatusRolledBack {
		log := h.applyLog(t, cs.ID)
		t.Fatalf("status after confirm-timeout = %s, want rolled_back; rollback log: %+v", got.Status, log.Rollback)
	}
	if after := fwRuleComments(t, h); hasComment(after, "t1805 lockout rule") {
		t.Fatalf("firewall rule survived the unattended revert: %v", after)
	}
	if len(h.factory.seen) != 1 {
		t.Fatalf("revert-gateway factory was called %d times, want exactly 1 (the changeset's own revert)", len(h.factory.seen))
	}
	// AC4 (expiry leg): leaving awaiting_confirm wipes the credential.
	assertNoSealedTicket(t, h, cs.ID, "confirm-window expiry")
}

// TestRevertTicket_AC1_WithoutSealedTicketTheFirewallRuleSurvives is the
// control for the test above: the identical scenario with no ticket available
// at apply time reproduces the pre-T-1805 behaviour exactly — the rule is
// still there afterwards, and the apply response said so up front. Without
// this, AC1's test could pass for a reason unrelated to the sealed ticket.
func TestRevertTicket_AC1_WithoutSealedTicketTheFirewallRuleSurvives(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()
	h.gw.suppress = true // a gateway with no user ticket to give

	cs := h.mustCreate(t, "root@pam", "firewall-only, no sealed ticket", fwOps())
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.UnattendedRevert == nil || applied.UnattendedRevert.Available {
		t.Fatalf("unattendedRevert = %+v, want required-and-unavailable", applied.UnattendedRevert)
	}
	if !strings.Contains(applied.UnattendedRevert.Reason, "will not revert automatically") {
		t.Errorf("reason = %q, want a plain statement that it will not self-revert", applied.UnattendedRevert.Reason)
	}
	if sealed, _ := sealedTicketRow(t, h, cs.ID); len(sealed) != 0 {
		t.Fatalf("a ticket was sealed even though the gateway offered none")
	}

	h.timers.fireLatest(t)

	if after := fwRuleComments(t, h); !hasComment(after, "t1805 lockout rule") {
		t.Fatalf("control case: the rule was reverted without a sealed ticket — AC1's test may be passing for the wrong reason")
	}
	got, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The un-revertable firewall scope is reported as a failed rollback
	// action, so the changeset lands in the distinguishable
	// "rollback incomplete" state rather than a silent rolled_back.
	if got.Status != change.StatusFailed {
		t.Errorf("status = %s, want failed (rollback incomplete) when the firewall portion could not be reverted", got.Status)
	}
}

// --- AC2: crash recovery ----------------------------------------------------

// TestRevertTicket_AC2_CrashRecoveryUnsealsAndCompletesTheRevert is
// acceptance criterion 2: vnproxd is killed inside the confirm window and
// restarted. The replacement Service shares only the SQLite file — no
// in-memory timer, no gateway, no session — so the sealed ticket in the row is
// the *only* way the firewall portion can revert. ArmPendingRollbacks re-arms
// from the DB and the re-armed timer completes the revert unattended.
func TestRevertTicket_AC2_CrashRecoveryUnsealsAndCompletesTheRevert(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "firewall-only, daemon killed mid-window", fwOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !hasComment(fwRuleComments(t, h), "t1805 lockout rule") {
		t.Fatalf("apply did not add the firewall rule")
	}

	// --- the crash: drop every in-process timer, then stand up a brand new
	// Service over the same store, exactly as a daemon restart would.
	h.svc.StopTimers()
	restartTimers := &fakeTimers{}
	restarted := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Inventory: liveFwInventorySource{client: h.client, target: fwGuestRef(), scope: fwGuestScope()},
		Nodes:     h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: restartTimers.New,
		Sealer:    h.cipher, RevertGateways: h.factory,
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
	})
	if err := restarted.ArmPendingRollbacks(ctx); err != nil {
		t.Fatalf("ArmPendingRollbacks: %v", err)
	}
	if restartTimers.armedCount() != 1 {
		t.Fatalf("re-armed timers = %d, want 1", restartTimers.armedCount())
	}

	restartTimers.fireLatest(t)

	got, err := restarted.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusRolledBack {
		t.Fatalf("status after crash-recovery timeout = %s, want rolled_back", got.Status)
	}
	if after := fwRuleComments(t, h); hasComment(after, "t1805 lockout rule") {
		t.Fatalf("firewall rule survived the post-restart unattended revert: %v", after)
	}
	assertNoSealedTicket(t, h, cs.ID, "post-restart unattended revert")
}

// --- AC3 / claim 1: the credential never leaks ------------------------------

// TestRevertTicket_AC3_NeverLeaksToAnySurface is acceptance criterion 3, one
// table-driven case per surface, mirroring
// TestWireGuardRepo_PrivateKeyEncryptedAtRest's shape — including the raw-DB
// -bytes assertion that the plaintext ticket is nowhere in the file.
func TestRevertTicket_AC3_NeverLeaksToAnySurface(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "leak surfaces", fwOps())
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The exact plaintext now at rest, obtained the same way the engine did.
	ticket, csrf, _, ok := h.client.RevertCredentials(ctx)
	if !ok || ticket == "" || csrf == "" {
		t.Fatalf("could not read the applying client's ticket for the leak assertions")
	}
	secrets := map[string]string{"ticket": ticket, "csrf": csrf}

	sealed, _ := sealedTicketRow(t, h, cs.ID)
	if len(sealed) == 0 {
		t.Fatalf("no sealed ticket to test against")
	}

	fetched, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	listed, err := h.svc.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	audit, err := h.auditRepo.List(ctx, cs.ID, 500)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	dbBytes, err := os.ReadFile(h.dbPath)
	if err != nil {
		t.Fatalf("reading the DB file: %v", err)
	}

	surfaces := []struct {
		name  string
		bytes []byte
	}{
		{"sealed ciphertext at rest", sealed},
		{"Apply response", mustJSON(t, applied)},
		{"GET /changesets/{id} model", mustJSON(t, fetched)},
		{"GET /changesets list model", mustJSON(t, listed)},
		{"audit log", mustJSON(t, audit)},
		{"daemon log", h.logBuf.Bytes()},
		{"raw SQLite file bytes", dbBytes},
		{"WS changeset.status broadcasts", mustJSON(t, h.ws.messages())},
	}
	for _, s := range surfaces {
		for label, secret := range secrets {
			if bytes.Contains(s.bytes, []byte(secret)) {
				t.Errorf("%s contains the plaintext PVE %s", s.name, label)
			}
		}
	}

	// Positive control: the cipher — and only the cipher — recovers it, so
	// the assertions above are about encryption, not about the value being
	// absent from the DB for some unrelated reason.
	plain, err := h.cipher.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt(sealed): %v", err)
	}
	var rt change.RevertTicket
	if err := json.Unmarshal(plain, &rt); err != nil {
		t.Fatalf("decoding unsealed ticket: %v", err)
	}
	if rt.Ticket != ticket || rt.CSRF != csrf {
		t.Fatalf("unsealed credential does not round-trip: got ticket=%q csrf=%q", rt.Ticket, rt.CSRF)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling for leak assertion: %v", err)
	}
	return b
}

// --- AC4: wiped on every exit from the window -------------------------------

// TestRevertTicket_AC4_WipedOnEveryTerminalPath is acceptance criterion 4:
// confirm, rollback, expiry, and changeset deletion each leave no ticket bytes
// in the stored row, asserted directly against that row.
func TestRevertTicket_AC4_WipedOnEveryTerminalPath(t *testing.T) {
	cases := []struct {
		// leave drives the changeset out of awaiting_confirm (or, for the
		// deletion case, out of existence as an applicable changeset).
		leave func(t *testing.T, h *revertHarness, id string)
		name  string
		// seedApplied is false for the deletion case, which operates on a
		// draft (the only state DELETE /changesets/{id} accepts).
		seedApplied bool
	}{
		{
			name:        "confirm",
			seedApplied: true,
			leave: func(t *testing.T, h *revertHarness, id string) {
				if _, err := h.svc.Confirm(context.Background(), id, "root@pam"); err != nil {
					t.Fatalf("Confirm: %v", err)
				}
			},
		},
		{
			name:        "manual rollback",
			seedApplied: true,
			leave: func(t *testing.T, h *revertHarness, id string) {
				if _, err := h.svc.Rollback(context.Background(), id, "root@pam", h.gw); err != nil {
					t.Fatalf("Rollback: %v", err)
				}
			},
		},
		{
			name:        "confirm-window expiry",
			seedApplied: true,
			leave: func(t *testing.T, h *revertHarness, _ string) {
				h.timers.fireLatest(t)
			},
		},
		{
			name:        "changeset deletion (discard)",
			seedApplied: false,
			leave: func(t *testing.T, h *revertHarness, id string) {
				if err := h.svc.Discard(context.Background(), id, "root@pam"); err != nil {
					t.Fatalf("Discard: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRevertHarness(t)
			ctx := context.Background()
			cs := h.mustCreate(t, "root@pam", "wipe-"+tc.name, fwOps())

			if tc.seedApplied {
				if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0); err != nil {
					t.Fatalf("Apply: %v", err)
				}
			} else {
				// A draft never reaches Apply, so seal directly against the row:
				// the point of this case is that the delete path wipes
				// unconditionally rather than relying on "a draft can't have one".
				if err := h.csRepo.SealRevertTicket(ctx, cs.ID, []byte("sealed-bytes-for-the-discard-case"), h.now().Add(time.Hour).Unix()); err != nil {
					t.Fatalf("SealRevertTicket: %v", err)
				}
			}
			if sealed, _ := sealedTicketRow(t, h, cs.ID); len(sealed) == 0 {
				t.Fatalf("precondition: expected a sealed ticket before %s", tc.name)
			}

			tc.leave(t, h, cs.ID)

			assertNoSealedTicket(t, h, cs.ID, tc.name)
		})
	}
}

// TestRevertTicket_AC4_WipedWhenTheApplyItselfFails covers the fifth exit the
// card's "whichever comes first" wording implies: an apply that never reaches
// the confirm window at all still seals a ticket (it is sealed before the
// first mutating step, so crash recovery can use it) and so must still wipe.
func TestRevertTicket_AC4_WipedWhenTheApplyItselfFails(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()
	h.gw.failFwTarget = fwGuestRef().String()

	cs := h.mustCreate(t, "root@pam", "failing fw apply", fwOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0); err == nil {
		t.Fatalf("Apply succeeded, want the injected firewall failure")
	}
	got, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	assertNoSealedTicket(t, h, cs.ID, "failed apply")
}

// TestRevertTicket_AC4_ExpiredTicketIsSweptAndRefused covers the ticket's own
// expiry (as distinct from the confirm window's): a sealed credential PVE will
// no longer honour is wiped rather than left at rest, both by the startup
// sweep and by the revert path itself when it finds one.
func TestRevertTicket_AC4_ExpiredTicketIsSweptAndRefused(t *testing.T) {
	t.Run("startup sweep", func(t *testing.T) {
		h := newRevertHarness(t)
		ctx := context.Background()
		cs := h.mustCreate(t, "root@pam", "expired ticket", fwOps())
		if err := h.csRepo.SealRevertTicket(ctx, cs.ID, []byte("stale"), h.now().Add(-time.Minute).Unix()); err != nil {
			t.Fatalf("SealRevertTicket: %v", err)
		}
		h.svc.SweepExpiredRevertTickets(ctx)
		assertNoSealedTicket(t, h, cs.ID, "expired-ticket sweep")
	})

	t.Run("revert path refuses and wipes", func(t *testing.T) {
		h := newRevertHarness(t)
		ctx := context.Background()
		// The ticket expires one second into a 30s window: coverage is
		// reduced, and the timeout finds a dead credential.
		h.gw.expiresIn = time.Second
		cs := h.mustCreate(t, "root@pam", "ticket expires mid-window", fwOps())
		applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 30*time.Second)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if applied.UnattendedRevert == nil || applied.UnattendedRevert.FullWindow {
			t.Fatalf("unattendedRevert = %+v, want reduced coverage", applied.UnattendedRevert)
		}
		// Let the ticket lapse, then fire the window.
		time.Sleep(1100 * time.Millisecond)
		h.timers.fireLatest(t)

		if len(h.factory.seen) != 0 {
			t.Errorf("an expired ticket was unsealed into a gateway (%d calls); it must be refused", len(h.factory.seen))
		}
		assertNoSealedTicket(t, h, cs.ID, "expired ticket on the revert path")
	})
}

// --- AC6: reduced coverage is reported at apply time ------------------------

// TestRevertTicket_AC6_CoverageReportedAtApplyTime is acceptance criterion 6:
// a changeset whose confirm window would outlive its PVE ticket says so in the
// apply response, and the statement survives a reload (GET /changesets/{id}
// recomputes it from the stored expiry).
func TestRevertTicket_AC6_CoverageReportedAtApplyTime(t *testing.T) {
	cases := []struct {
		name           string
		ops            []change.Op
		expiresIn      time.Duration
		confirmTimeout time.Duration
		wantRequired   bool
		wantAvailable  bool
		wantFullWindow bool
	}{
		{
			name:           "fw changeset, ticket outlives the window",
			ops:            fwOps(),
			expiresIn:      time.Hour,
			confirmTimeout: 30 * time.Second,
			wantRequired:   true, wantAvailable: true, wantFullWindow: true,
		},
		{
			name:           "fw changeset, ticket expires mid-window",
			ops:            fwOps(),
			expiresIn:      45 * time.Second,
			confirmTimeout: 600 * time.Second,
			wantRequired:   true, wantAvailable: true, wantFullWindow: false,
		},
		{
			name: "node-file changeset needs no ticket at all",
			ops: []change.Op{{
				Type:   change.OpBridgeCreate,
				Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"},
				Params: &change.BridgeCreateParams{},
			}},
			expiresIn:      time.Hour,
			confirmTimeout: 600 * time.Second,
			wantRequired:   false, wantAvailable: true, wantFullWindow: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRevertHarness(t)
			ctx := context.Background()
			h.gw.expiresIn = tc.expiresIn

			cs := h.mustCreate(t, "root@pam", tc.name, tc.ops)
			applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, tc.confirmTimeout)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got := applied.UnattendedRevert
			if got == nil {
				t.Fatalf("apply response carries no unattendedRevert report")
			}
			if got.Required != tc.wantRequired || got.Available != tc.wantAvailable || got.FullWindow != tc.wantFullWindow {
				t.Fatalf("unattendedRevert = %+v, want required=%v available=%v fullWindow=%v",
					got, tc.wantRequired, tc.wantAvailable, tc.wantFullWindow)
			}
			if !tc.wantFullWindow {
				if got.CoversUntil >= *applied.ConfirmDeadline {
					t.Errorf("coversUntil %d should precede the confirm deadline %d", got.CoversUntil, *applied.ConfirmDeadline)
				}
				if got.Reason == "" {
					t.Errorf("reduced coverage reported with no reason")
				}
			}

			// The statement survives a page reload.
			reread, err := h.svc.Get(ctx, cs.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if reread.UnattendedRevert == nil {
				t.Fatalf("GET does not recompute the coverage report")
			}
			if *reread.UnattendedRevert != *got {
				t.Errorf("re-read coverage = %+v, want the apply-time %+v", reread.UnattendedRevert, got)
			}
		})
	}
}

// --- AC7: sdn.apply gets the same treatment ---------------------------------

// TestRevertTicket_AC7_SDNChangesetRevertsOnConfirmTimeout is acceptance
// criterion 7: an `sdn.*` changeset (stage ops plus the trailing sdn.apply)
// reverts unattended on the same sealed ticket. Before T-1805, restoreSDN's own
// doc comment recorded that a nil gateway on this path was reported as a
// failed-but-non-fatal rollback entry and the zone simply stayed.
func TestRevertTicket_AC7_SDNChangesetRevertsOnConfirmTimeout(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()

	zoneRef := inventory.Ref{Kind: inventory.KindSDNZone, ID: "t1805z"}
	ops := []change.Op{
		{Type: change.OpSdnZoneCreate, Target: zoneRef, Params: &change.SdnZoneCreateParams{Type: "vlan", Bridge: "vmbr0"}},
		{Type: change.OpSdnApply, Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: "t1805z"}, Params: &change.SdnApplyParams{}},
	}
	cs := h.mustCreate(t, "root@pam", "sdn zone + apply", ops)
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !sdnZoneExists(t, h, "t1805z") {
		t.Fatalf("apply did not create the SDN zone")
	}
	if sealed, _ := sealedTicketRow(t, h, cs.ID); len(sealed) == 0 {
		t.Fatalf("no revert ticket sealed for an sdn changeset")
	}

	h.timers.fireLatest(t)

	got, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusRolledBack {
		log := h.applyLog(t, cs.ID)
		t.Fatalf("status = %s, want rolled_back; rollback log: %+v", got.Status, log.Rollback)
	}
	if sdnZoneExists(t, h, "t1805z") {
		t.Fatalf("SDN zone survived the unattended revert")
	}
	assertNoSealedTicket(t, h, cs.ID, "sdn unattended revert")
}

func sdnZoneExists(t *testing.T, h *revertHarness, id string) bool {
	t.Helper()
	zones, err := h.client.ListSDNZones(context.Background())
	if err != nil {
		t.Fatalf("ListSDNZones: %v", err)
	}
	for _, z := range zones {
		if z.ID == id {
			return true
		}
	}
	return false
}

// --- claim 5: not a second mutation path ------------------------------------

// TestRevertTicket_Claim5_RevertIsTheExistingRollbackMachinery asserts the
// sealed ticket introduces no second mutation path: the unattended revert
// produces the ordinary rollback log entries and the ordinary
// `changeset.rollback` audit row (attributed to system:rollback), with the one
// additive, non-secret detail recording that a sealed credential was used.
func TestRevertTicket_Claim5_RevertIsTheExistingRollbackMachinery(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "audit shape", fwOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	h.timers.fireLatest(t)

	entries, err := h.auditRepo.List(ctx, cs.ID, 200)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var rollback *store.AuditEntry
	for i := range entries {
		if entries[i].Action == "changeset.rollback" && entries[i].ChangesetID.String == cs.ID {
			rollback = &entries[i]
		}
	}
	if rollback == nil {
		t.Fatalf("no changeset.rollback audit entry for the unattended revert")
	}
	if rollback.Username != "system:rollback" {
		t.Errorf("audit actor = %q, want system:rollback (docs/security.md)", rollback.Username)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(rollback.DetailJSON.String), &detail); err != nil {
		t.Fatalf("decoding audit detail: %v", err)
	}
	if used, _ := detail["sealedRevertTicketUsed"].(bool); !used {
		t.Errorf("audit detail = %v, want sealedRevertTicketUsed=true", detail)
	}

	// The rollback log names the firewall scope it restored — the ordinary
	// RollbackLog shape every other op family already produces.
	log := h.applyLog(t, cs.ID)
	sawFw := false
	for _, rb := range log.Rollback {
		if strings.Contains(rb.Summary, "Restore firewall scope") {
			sawFw = true
			if rb.Status != change.StepOK {
				t.Errorf("firewall rollback entry failed: %s", rb.Error)
			}
		}
	}
	if !sawFw {
		t.Errorf("rollback log has no firewall restore entry: %+v", log.Rollback)
	}
}

// TestRevertTicket_Claim3_SealingIsSkippedWhenNothingNeedsATicket asserts the
// credential is only ever created when it can actually be used: a changeset
// with no ticket-scoped op seals nothing at all, so the population of
// at-rest credentials stays as small as the feature allows.
func TestRevertTicket_Claim3_SealingIsSkippedWhenNothingNeedsATicket(t *testing.T) {
	h := newRevertHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "node-file only", []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr8"},
		Params: &change.BridgeCreateParams{},
	}})
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", h.gw, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if sealed, _ := sealedTicketRow(t, h, cs.ID); len(sealed) != 0 {
		t.Fatalf("a ticket was sealed for a changeset that needs none (%d bytes)", len(sealed))
	}
	if applied.UnattendedRevert == nil || applied.UnattendedRevert.Required {
		t.Fatalf("unattendedRevert = %+v, want required=false", applied.UnattendedRevert)
	}
	if !applied.UnattendedRevert.Available || !applied.UnattendedRevert.FullWindow {
		t.Errorf("a node-file changeset must report full unattended coverage, got %+v", applied.UnattendedRevert)
	}
}
