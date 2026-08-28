// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/ifcounters"
	"github.com/bgovanlu/vnprox/internal/store"
)

// ifCounterDecrypter is the subset of *store.SessionCipher setupIfCounters
// needs — declared as an interface (the same seam pattern every other
// cross-package dependency in this file uses) so a test can substitute a
// fake without real AES-GCM key material.
type ifCounterDecrypter interface {
	Decrypt(sealed []byte) ([]byte, error)
}

// ifCounterTargetStoreAdapter adapts *store.SwitchSNMPTargetRepo plus a
// decryption cipher into ifcounters.TargetStore — decrypting each row's
// community string fresh on every call, never caching plaintext anywhere
// longer-lived than one Tick, the same "decrypt only for the duration of
// use" discipline internal/ifcounters/types.go's Target doc comment
// describes for the guarded-push driver factory. A row whose community
// string fails to decrypt (a corrupted row, or a session key rotated out
// from under it) is logged and skipped for this tick rather than aborting
// every other switch's poll.
type ifCounterTargetStoreAdapter struct {
	repo   *store.SwitchSNMPTargetRepo
	cipher ifCounterDecrypter
	logger *slog.Logger
}

func (a *ifCounterTargetStoreAdapter) ListEnabled(ctx context.Context) ([]ifcounters.Target, error) {
	rows, err := a.repo.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ifcounters.Target, 0, len(rows))
	for _, row := range rows {
		var community []byte
		if len(row.CommunityEnc) > 0 {
			plain, decErr := a.cipher.Decrypt(row.CommunityEnc)
			if decErr != nil {
				a.logger.Warn("ifcounters: decrypting community string failed, skipping switch this tick",
					"chassisId", row.ChassisID, "error", decErr)
				continue
			}
			community = plain
		}
		out = append(out, ifcounters.Target{
			ChassisID: row.ChassisID, MgmtAddr: row.MgmtAddr, Community: community, Port: row.Port,
		})
	}
	return out, nil
}

// setupIfCounters builds T-4013's *ifcounters.Service — a node-local
// scheduler that polls exactly the switches BOTH currently LLDP-discovered
// (neighbors, in production the same *topology.Service instance server.go
// already built) AND explicitly opted in via an enabled
// switch_snmp_targets row (repo, decrypted through cipher). Returns the
// Service itself, wired into api.Options.IfCounters (GET /snmp/counters)
// and the map-edge annotation query, plus the single supervised poll-loop
// actor cmd/vnproxd's run group registers alongside every other owned
// goroutine — mirrors setupMTUProbe's identical shape (that function's own
// doc comment).
func setupIfCounters(cfg *config.Config, neighbors ifcounters.NeighborLister, repo *store.SwitchSNMPTargetRepo, cipher ifCounterDecrypter, logger *slog.Logger) (*ifcounters.Service, []func(context.Context) error) {
	svc := ifcounters.New(ifcounters.Config{
		Neighbors:       neighbors,
		Targets:         &ifCounterTargetStoreAdapter{repo: repo, cipher: cipher, logger: logger},
		Logger:          logger,
		PollIntervalSec: cfg.IfCounters.PollIntervalSec,
	})
	actors := []func(context.Context) error{svc.RunLoop}
	return svc, actors
}
