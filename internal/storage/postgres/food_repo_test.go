package postgres

import (
	"math"
	"testing"
)

func TestAffinityBonus(t *testing.T) {
	const tolerance = 1e-9
	yearAgoBonus := AffinityWeight * RecencyFloor

	tests := []struct {
		name     string
		logs     int
		daysAgo  float64
		wantMin  float64
		wantMax  float64
		wantZero bool
	}{
		{name: "never logged", logs: 0, daysAgo: 0, wantZero: true},
		{name: "logged once today", logs: 1, daysAgo: 0,
			wantMin: AffinityWeight * math.Log(2) / math.Log(1+FamiliarAtLogs),
			wantMax: AffinityWeight * math.Log(2) / math.Log(1+FamiliarAtLogs)},
		{name: "logged ten times today", logs: 10, daysAgo: 0,
			wantMin: AffinityWeight, wantMax: AffinityWeight},
		{name: "logged ten times a year ago", logs: 10, daysAgo: 365,
			wantMin: yearAgoBonus, wantMax: yearAgoBonus + 0.0001},
		{name: "logged far above the familiar count", logs: 1000, daysAgo: 0,
			wantMin: AffinityWeight, wantMax: AffinityWeight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := affinityBonus(tt.logs, tt.daysAgo)
			if tt.wantZero {
				if got != 0 {
					t.Fatalf("got %v, want 0", got)
				}
				return
			}
			if got < tt.wantMin-tolerance || got > tt.wantMax+tolerance {
				t.Fatalf("got %v, want within [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestAffinityBonusNeverExceedsWeight(t *testing.T) {
	for _, logs := range []int{0, 1, 2, 5, 10, 11, 100, 100000} {
		for _, days := range []float64{0, 1, 30, 365, 10000} {
			got := affinityBonus(logs, days)
			if got < 0 || got > AffinityWeight {
				t.Errorf("affinityBonus(%d, %v) = %v, outside [0, %v]", logs, days, got, AffinityWeight)
			}
		}
	}
}
