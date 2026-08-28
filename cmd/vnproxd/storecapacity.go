// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// storeCapacityAdapter (T-1905) adapts *store.DB.SizeBytes — T-1903's
// existing on-disk size source, also what GET /metrics'
// vnprox_store_size_bytes renders — plus the daemon's own node identity
// into findings.StoreCapacityProvider, backing the store_near_capacity
// finding. Deliberately not a second measurement of the store's footprint:
// see internal/findings/health_storecapacity.go's package doc comment.
type storeCapacityAdapter struct {
	db        *store.DB
	localNode func() string
}

func (a storeCapacityAdapter) StoreCapacity() (findings.StoreCapacityReport, error) {
	size, err := a.db.SizeBytes()
	if err != nil {
		return findings.StoreCapacityReport{}, err
	}
	return findings.StoreCapacityReport{Node: a.localNode(), SizeBytes: size}, nil
}
