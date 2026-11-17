package stocks

import (
	"fmt"
	"time"
)

// rule is one gain or loss level worth flagging. Threshold is a percent:
// positive for a gain level, negative for a loss level. RearmGap is how far
// the position has to pull back past the level before the same rule can
// fire again, so it doesn't re-fire every poll while hovering right at the
// line.
type rule struct {
	Key       string
	Threshold float64
	RearmGap  float64
}

type tierConfig struct {
	Rules    []rule
	Cooldown time.Duration // minimum time between fires of the same rule
}

// alert is one rule crossing, for one lot.
type alert struct {
	PositionID int64
	Ticker     string
	Rule       string
	Message    string
}

type ruleState struct {
	Fired     bool
	LastFired time.Time
}

var tiers = map[string]tierConfig{
	"steady": {
		Cooldown: 30 * 24 * time.Hour,
		Rules: []rule{
			{"gain_5", 5, 2},
			{"gain_10", 10, 2},
			{"gain_20", 20, 2},
			{"loss_5", -5, 2},
			{"loss_10", -10, 2},
			{"loss_20", -20, 2},
		},
	},
	"volatile": {
		Cooldown: 10 * 24 * time.Hour,
		Rules: []rule{
			{"gain_15", 15, 4},
			{"gain_25", 25, 4},
			{"gain_50", 50, 4},
			{"loss_15", -15, 4},
			{"loss_25", -25, 4},
			{"loss_40", -40, 4},
		},
	},
}

// tierByName falls back to "steady" for an unknown or empty tier name.
func tierByName(name string) tierConfig {
	if c, ok := tiers[name]; ok {
		return c
	}
	return tiers["steady"]
}

// tierFor picks the alert tier for a ticker: "volatile" if it's in the
// configured list, "steady" otherwise.
func tierFor(ticker string, volatile map[string]bool) tierConfig {
	if volatile[ticker] {
		return tierByName("volatile")
	}
	return tierByName("steady")
}

// evaluate returns the alerts to raise now and the updated per-rule state.
// It never modifies the state map it is given.
func evaluate(v positionView, state map[string]ruleState, cfg tierConfig, now time.Time) ([]alert, map[string]ruleState) {
	next := make(map[string]ruleState, len(state))
	for k, s := range state {
		next[k] = s
	}

	var alerts []alert
	for _, r := range cfg.Rules {
		value := v.GainPct

		crossed := value >= r.Threshold
		rearmed := value <= r.Threshold-r.RearmGap
		if r.Threshold < 0 {
			crossed = value <= r.Threshold
			rearmed = value >= r.Threshold+r.RearmGap
		}

		s := next[r.Key]
		switch {
		case !s.Fired && crossed && (s.LastFired.IsZero() || now.Sub(s.LastFired) >= cfg.Cooldown):
			s.Fired = true
			s.LastFired = now
			next[r.Key] = s
			alerts = append(alerts, alert{
				PositionID: v.ID,
				Ticker:     v.Ticker,
				Rule:       r.Key,
				Message: fmt.Sprintf("%s is %+.1f%% ($%+.2f) vs your cost, crossed %s",
					v.Ticker, v.GainPct, v.GainUSD, r.Key),
			})
		case s.Fired && rearmed:
			s.Fired = false
			next[r.Key] = s
		}
	}
	return alerts, next
}
