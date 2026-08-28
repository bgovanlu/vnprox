// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"testing"
	"time"
)

func TestWhatIf(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	latest := Aggregate{BucketAt: base, Ref: "vmbr0", Kind: KindLink, MaxUtil: 60, AvgUtil: 50}

	cases := []struct {
		wantBreaksAtN    *int
		name             string
		latest           Aggregate
		addedPctPerGuest float64
		n                int
		wantAlreadyOver  bool
	}{
		{
			name:             "fits comfortably within n",
			latest:           latest,
			addedPctPerGuest: 1, // 1% per guest
			n:                10,
			wantBreaksAtN:    nil, // 60 + 10*1 = 70, never reaches 100
		},
		{
			name:             "breaks partway through n",
			latest:           latest,
			addedPctPerGuest: 5, // 60 + n*5 crosses 100 at n=8
			n:                20,
			wantBreaksAtN:    intPtr(8),
		},
		{
			name:             "breaks exactly at n",
			latest:           latest,
			addedPctPerGuest: 4, // 60 + 10*4 = 100
			n:                10,
			wantBreaksAtN:    intPtr(10),
		},
		{
			name:             "breaks on the very first guest",
			latest:           latest,
			addedPctPerGuest: 45, // 60 + 45 = 105 already over by guest 1
			n:                10,
			wantBreaksAtN:    intPtr(1),
		},
		{
			name:             "n=1, does not break",
			latest:           latest,
			addedPctPerGuest: 5,
			n:                1,
			wantBreaksAtN:    nil,
		},
		{
			name:             "already full today",
			latest:           Aggregate{BucketAt: base, Ref: "vmbr0", Kind: KindLink, MaxUtil: 100},
			addedPctPerGuest: 5,
			n:                10,
			wantAlreadyOver:  true,
		},
		{
			name:             "zero load per guest never breaks",
			latest:           latest,
			addedPctPerGuest: 0,
			n:                1000,
			wantBreaksAtN:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WhatIf(tc.latest, tc.addedPctPerGuest, tc.n)
			if got.AlreadyOverToday != tc.wantAlreadyOver {
				t.Fatalf("AlreadyOverToday = %v, want %v", got.AlreadyOverToday, tc.wantAlreadyOver)
			}
			if (got.BreaksAtN == nil) != (tc.wantBreaksAtN == nil) {
				t.Fatalf("BreaksAtN = %v, want %v", got.BreaksAtN, tc.wantBreaksAtN)
			}
			if got.BreaksAtN != nil && *got.BreaksAtN != *tc.wantBreaksAtN {
				t.Fatalf("BreaksAtN = %d, want %d", *got.BreaksAtN, *tc.wantBreaksAtN)
			}
		})
	}
}

func intPtr(n int) *int { return &n }
