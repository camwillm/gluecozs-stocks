package stocks

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, nyc)
	day := 24 * time.Hour

	tests := []struct {
		name      string
		tier      string
		gainPct   float64
		state     map[string]ruleState
		wantRules []string
		wantFired map[string]bool
	}{
		{"steady below everything", "steady", 3, nil, nil, nil},
		{"steady crosses +5 only", "steady", 6, nil,
			[]string{"gain_5"}, map[string]bool{"gain_5": true}},
		{"steady crosses two at once", "steady", 12, nil,
			[]string{"gain_5", "gain_10"}, nil},
		{"steady loss levels", "steady", -11, nil,
			[]string{"loss_5", "loss_10"}, nil},
		{"volatile only fires its own levels", "volatile", 16, nil,
			[]string{"gain_15"}, nil},
		{"already fired stays quiet", "steady", 6,
			map[string]ruleState{"gain_5": {Fired: true, LastFired: now.Add(-1 * day)}},
			nil, map[string]bool{"gain_5": true}},
		{"hovering in the gap stays fired", "steady", 4,
			map[string]ruleState{"gain_5": {Fired: true, LastFired: now.Add(-1 * day)}},
			nil, map[string]bool{"gain_5": true}},
		{"re-arms after retreating", "steady", 2.5,
			map[string]ruleState{"gain_5": {Fired: true, LastFired: now.Add(-1 * day)}},
			nil, map[string]bool{"gain_5": false}},
		{"loss rule re-arms", "steady", -4,
			map[string]ruleState{"loss_10": {Fired: true, LastFired: now.Add(-1 * day)}},
			nil, map[string]bool{"loss_10": false}},
		{"steady cooldown blocks at 10 days", "steady", 6,
			map[string]ruleState{"gain_5": {Fired: false, LastFired: now.Add(-10 * day)}},
			nil, map[string]bool{"gain_5": false}},
		{"steady fires again after 31 days", "steady", 6,
			map[string]ruleState{"gain_5": {Fired: false, LastFired: now.Add(-31 * day)}},
			[]string{"gain_5"}, map[string]bool{"gain_5": true}},
		{"volatile fires again after 11 days", "volatile", 16,
			map[string]ruleState{"gain_15": {Fired: false, LastFired: now.Add(-11 * day)}},
			[]string{"gain_15"}, map[string]bool{"gain_15": true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := positionView{position: position{ID: 1, Ticker: "TEST"}, GainPct: tc.gainPct}
			alerts, next := evaluate(v, tc.state, tierByName(tc.tier), now)

			if len(alerts) != len(tc.wantRules) {
				t.Fatalf("got %d alerts, want %d", len(alerts), len(tc.wantRules))
			}
			for i, a := range alerts {
				if a.Rule != tc.wantRules[i] {
					t.Errorf("alert %d: rule %q, want %q", i, a.Rule, tc.wantRules[i])
				}
			}
			for k, want := range tc.wantFired {
				if next[k].Fired != want {
					t.Errorf("state %q Fired = %v, want %v", k, next[k].Fired, want)
				}
			}
		})
	}
}
