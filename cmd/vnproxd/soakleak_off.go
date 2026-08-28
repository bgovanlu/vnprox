//go:build !soakleak

// SPDX-License-Identifier: Apache-2.0

// soakleak_off.go is the production half of T-2504's leak-fixture seam: in
// every build that does NOT carry the `soakleak` tag — which is every build
// this repo ever ships, tests, lints, or packages — the two hooks below are
// the identity function and an empty actor list. There is no runtime flag,
// no environment variable, and no code path that can turn a leak on in a
// real binary; the leaky implementations do not exist in it at all.
//
// See soakleak.go for the fixtures themselves and for why a gate ships with
// something that makes it fire.

package main

import (
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// soakLeakPollHook returns next unchanged.
func soakLeakPollHook(next func(source, node string, dur time.Duration, err error)) func(source, node string, dur time.Duration, err error) {
	return next
}

// soakLeakActors returns no actors.
func soakLeakActors(*store.DB, *slog.Logger) []actor { return nil }
